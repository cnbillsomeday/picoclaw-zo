package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/audio/tts"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/constants"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/utils"
)

func (al *AgentLoop) voiceMode(sessionKey string) string {
	if value, ok := al.voiceModes.Load(strings.TrimSpace(sessionKey)); ok {
		if mode, ok := value.(string); ok {
			return mode
		}
	}
	if al.voiceDefaultMode != "" {
		return al.voiceDefaultMode
	}
	return "off"
}

func (al *AgentLoop) currentTTSProvider() tts.TTSProvider {
	al.mu.RLock()
	defer al.mu.RUnlock()
	return al.ttsProvider
}

func (al *AgentLoop) setVoiceMode(sessionKey, mode string) error {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return fmt.Errorf("voice mode unavailable in current session")
	}
	if mode != "off" && mode != "on" && mode != "tts" {
		return fmt.Errorf("unknown voice mode: %s", mode)
	}
	if mode != "off" && al.currentTTSProvider() == nil {
		return fmt.Errorf("TTS unavailable: enable tools.send_tts and configure a TTS model")
	}
	al.voiceModes.Store(sessionKey, mode)
	return nil
}

// sendVoiceResponseIfEnabled synthesizes the response as a voice note and sends
// it. It returns true when a voice note was actually sent; callers use that to
// skip the plain-text message so a voice session does not get both.
func (al *AgentLoop) sendVoiceResponseIfEnabled(
	ctx context.Context,
	agent *AgentInstance,
	opts processOptions,
	text string,
) bool {
	mode := al.voiceMode(opts.Dispatch.SessionKey)
	if mode == "off" || (mode == "on" && opts.Dispatch.InboundContext.Raw[metadataKeyInputAudio] != "true") {
		return false
	}
	provider := al.currentTTSProvider()
	if provider == nil || al.mediaStore == nil || al.bus == nil {
		logger.WarnCF("voice-tts", "Cannot synthesize response: TTS runtime is not ready", nil)
		return false
	}

	channel, chatID := opts.Dispatch.Channel(), opts.Dispatch.ChatID()
	ref, err := tts.SynthesizeAndStore(ctx, provider, al.mediaStore, text, "", channel, chatID)
	if err != nil {
		logger.WarnCF("voice-tts", "Failed to synthesize response", map[string]any{"error": err.Error()})
		return false
	}

	// TTS providers usually return mp3, but chat apps only show a voice bubble
	// for Ogg/Opus (Telegram SendVoice, Feishu file_type=opus). Convert when
	// possible and fall back to the original audio when ffmpeg is unavailable.
	if srcPath, resolveErr := al.mediaStore.Resolve(ref); resolveErr == nil {
		if opusPath, transcodeErr := tts.TranscodeToOpus(ctx, srcPath); transcodeErr == nil {
			mediaScope := fmt.Sprintf("voice-reply:%s:%s:%d", channel, chatID, time.Now().UnixNano())
			if opusRef, storeErr := al.mediaStore.Store(opusPath, media.MediaMeta{
				Filename:    filepath.Base(opusPath),
				ContentType: "audio/ogg",
				Source:      "tts:voice",
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

	part := bus.MediaPart{Ref: ref}
	if _, meta, resolveErr := al.mediaStore.ResolveWithMeta(ref); resolveErr == nil {
		part.Filename = meta.Filename
		part.ContentType = meta.ContentType
		part.Type = inferMediaType(meta.Filename, meta.ContentType)
	}
	outbound := bus.OutboundMediaMessage{
		Channel: channel,
		ChatID:  chatID,
		Context: outboundContextFromInbound(
			opts.Dispatch.InboundContext,
			channel,
			chatID,
			opts.Dispatch.ReplyToMessageID(),
		),
		AgentID:    agent.ID,
		SessionKey: opts.Dispatch.SessionKey,
		Scope:      outboundScopeFromSessionScope(opts.Dispatch.SessionScope),
		Parts:      []bus.MediaPart{part},
	}
	if al.channelManager != nil && channel != "" && !constants.IsInternalChannel(channel) {
		err = al.channelManager.SendMedia(ctx, outbound)
	} else {
		err = al.bus.PublishOutboundMedia(ctx, outbound)
	}
	if err != nil {
		logger.WarnCF("voice-tts", "Failed to send synthesized response", map[string]any{"error": err.Error()})
		return false
	}
	return true
}

func (al *AgentLoop) hasAudioInput(msg bus.InboundMessage) bool {
	if audioAnnotationRe.MatchString(msg.Content) {
		return true
	}
	if al.mediaStore == nil {
		return false
	}
	for _, ref := range msg.Media {
		_, meta, err := al.mediaStore.ResolveWithMeta(ref)
		if err == nil && utils.IsAudioFile(meta.Filename, meta.ContentType) {
			return true
		}
	}
	return false
}
