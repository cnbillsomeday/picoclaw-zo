package integrationtools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/audio/tts"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
)

type SendTTSTool struct {
	provider   tts.TTSProvider
	mediaStore media.MediaStore
}

func NewSendTTSTool(provider tts.TTSProvider, store media.MediaStore) *SendTTSTool {
	return &SendTTSTool{
		provider:   provider,
		mediaStore: store,
	}
}

func (t *SendTTSTool) Name() string { return "send_tts" }

func (t *SendTTSTool) Description() string {
	return "Synthesize speech from text and send it as an audio file to the user."
}

func (t *SendTTSTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"text": map[string]any{
				"type":        "string",
				"description": "The text to synthesize into speech. NOTE: Reply in a highly concise, conversational, oral style suitable for text-to-speech. Do not use markdown, emojis, asterisks, or code blocks. Speak naturally.",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Optional filename for the audio file (e.g., response.ogg).",
			},
		},
		"required": []string{"text"},
	}
}

func (t *SendTTSTool) SetMediaStore(store media.MediaStore) {
	t.mediaStore = store
}

func (t *SendTTSTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	text, _ := args["text"].(string)
	text = strings.TrimSpace(text)
	if text == "" {
		return ErrorResult("text is required")
	}

	channel := ToolChannel(ctx)
	chatID := ToolChatID(ctx)
	filename, _ := args["filename"].(string)

	ref, err := tts.SynthesizeAndStore(
		ctx,
		t.provider,
		t.mediaStore,
		text,
		filename,
		channel,
		chatID,
	)
	if err != nil {
		return ErrorResult(err.Error()).WithError(err)
	}

	// Chat apps only render a voice bubble for Ogg/Opus (Telegram SendVoice,
	// Feishu file_type=opus); TTS providers return mp3, which arrives as a file
	// attachment. Convert here so tool-triggered speech looks like a voice note.
	if srcPath, resolveErr := t.mediaStore.Resolve(ref); resolveErr == nil {
		if opusPath, transcodeErr := tts.TranscodeToOpus(ctx, srcPath); transcodeErr == nil {
			mediaScope := fmt.Sprintf("tool:send_tts:%s:%s:%d", channel, chatID, time.Now().UnixNano())
			if opusRef, storeErr := t.mediaStore.Store(opusPath, media.MediaMeta{
				Filename:    filepath.Base(opusPath),
				ContentType: "audio/ogg",
				Source:      "tool:send_tts",
			}, mediaScope); storeErr == nil {
				ref = opusRef
			} else {
				_ = os.Remove(opusPath)
				logger.WarnCF("voice-tts", "Failed to store opus voice note", map[string]any{"error": storeErr.Error()})
			}
		} else {
			logger.WarnCF("voice-tts", "Sending TTS audio without opus conversion", map[string]any{"error": transcodeErr.Error()})
		}
	}

	// Return with Media containing the voice note and mark ResponseHandled so it
	// is delivered immediately without LLM intervention. ForUser stays empty: the
	// audio is the reply, so no duplicate text bubble is published.
	return &ToolResult{
		ForLLM:          "TTS audio sent",
		Media:           []string{ref},
		ResponseHandled: true,
	}
}
