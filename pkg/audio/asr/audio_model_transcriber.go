package asr

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	audioutil "github.com/sipeed/picoclaw/pkg/audio"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type AudioModelTranscriber struct {
	provider providers.LLMProvider
	modelID  string
	prompt   string
	noPrompt bool
	// transcodeTo, when set, converts the audio to this format before sending.
	// MiMo's ASR endpoint only accepts wav and mp3, while chat channels commonly
	// deliver ogg/opus voice notes.
	transcodeTo string
}

const (
	defaultTranscriptionPrompt = "Transcribe this audio."
)

func NewAudioModelTranscriber(modelCfg *config.ModelConfig) *AudioModelTranscriber {
	if modelCfg == nil {
		return nil
	}

	logger.DebugCF("voice", "Creating audio model transcriber", map[string]any{
		"has_api_key": modelCfg.APIKey() != "",
		"api_base":    modelCfg.APIBase,
		"model":       modelCfg.Model,
	})

	protocol, _ := providers.ExtractProtocol(modelCfg)

	provider, modelID, err := providers.CreateProviderFromConfig(modelCfg)
	if err != nil {
		logger.ErrorCF("voice", "Failed to create audio model provider", map[string]any{"error": err})
		return nil
	}

	tr := &AudioModelTranscriber{
		provider:    provider,
		modelID:     modelID,
		prompt:      defaultTranscriptionPrompt,
		noPrompt:    protocol == "mimo",
		transcodeTo: "",
	}
	if protocol == "mimo" {
		tr.transcodeTo = "mp3"
	}
	return tr
}

func (t *AudioModelTranscriber) Transcribe(ctx context.Context, audioFilePath string) (*TranscriptionResponse, error) {
	logger.InfoCF("voice", "Starting audio model transcription", map[string]any{
		"audio_file": audioFilePath,
		"model":      t.modelID,
	})

	format, err := utils.AudioFormat(audioFilePath)
	if err != nil {
		logger.ErrorCF("voice", "Failed to detect audio format", map[string]any{"path": audioFilePath, "error": err})
		return nil, err
	}

	sourcePath := audioFilePath
	if t.transcodeTo != "" && format != t.transcodeTo {
		converted, cerr := transcodeAudio(ctx, audioFilePath, t.transcodeTo)
		if cerr != nil {
			logger.ErrorCF("voice", "Failed to transcode audio for transcription", map[string]any{
				"path": audioFilePath, "from": format, "to": t.transcodeTo, "error": cerr,
			})
			return nil, cerr
		}
		defer os.Remove(converted)
		sourcePath = converted
		format = t.transcodeTo
	}

	audioBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		logger.ErrorCF("voice", "Failed to read audio file", map[string]any{"path": sourcePath, "error": err})
		return nil, fmt.Errorf("failed to read audio file: %w", err)
	}

	content := t.prompt
	if t.noPrompt {
		// MiMo's ASR endpoint rejects any text part in the request: "ASR request
		// must not include text parts; text prompt is injected by the gateway".
		// The transcription prompt is only useful for chat-style audio models, so
		// send a bare input_audio message for providers that require it.
		content = ""
	}

	resp, err := t.provider.Chat(ctx, []providers.Message{
		{
			Role:    "user",
			Content: content,
			Media: []string{
				fmt.Sprintf("data:audio/%s;base64,%s", format, base64.StdEncoding.EncodeToString(audioBytes)),
			},
		},
	}, nil, t.modelID, map[string]any{
		"temperature": 0,
	})
	if err != nil {
		logger.ErrorCF("voice", "Audio model transcription request failed", map[string]any{"error": err})
		return nil, fmt.Errorf("transcription request failed: %w", err)
	}

	text := strings.TrimSpace(resp.Content)
	logger.InfoCF("voice", "Audio model transcription completed successfully", map[string]any{
		"text_length":           len(text),
		"transcription_preview": utils.Truncate(text, 50),
	})

	return &TranscriptionResponse{Text: text}, nil
}

func (t *AudioModelTranscriber) Name() string {
	return "audio-model"
}

const audioTranscodeTimeout = 60 * time.Second

// transcodeAudio converts src to the requested container with ffmpeg, which is
// the only decoder available across the supported platforms. The output lives
// in the managed media temp dir and the caller removes it.
func transcodeAudio(ctx context.Context, src, format string) (string, error) {
	bin, err := audioutil.FFmpegPath()
	if err != nil {
		return "", fmt.Errorf("transcoding %q audio needs ffmpeg, which is not installed", format)
	}

	if err := os.MkdirAll(media.TempDir(), 0o700); err != nil {
		return "", fmt.Errorf("failed to create media temp dir: %w", err)
	}
	outPath := filepath.Join(media.TempDir(), fmt.Sprintf("asr-%d.%s", time.Now().UnixNano(), format))
	args := []string{"-y", "-loglevel", "error", "-i", src, "-ar", "16000", "-ac", "1"}
	if format == "mp3" {
		args = append(args, "-b:a", "32k")
	}
	args = append(args, outPath)

	runCtx, cancel := context.WithTimeout(ctx, audioTranscodeTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := isolation.Run(cmd); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("ffmpeg transcode failed: %w: %s", err, strings.TrimSpace(combined.String()))
	}
	return outPath, nil
}
