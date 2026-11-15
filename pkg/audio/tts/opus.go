package tts

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/media"
)

const opusTranscodeTimeout = 60 * time.Second

// TranscodeToOpus converts src to an Ogg/Opus file and returns its path; the
// caller owns the file. Chat channels only render voice bubbles for opus:
// Telegram keys off a "voice" filename ending in .ogg/.oga, and Feishu uploads
// audio parts with file_type=opus. MiMo TTS only emits mp3/wav/pcm, so a
// conversion is required for a real voice note.
func TranscodeToOpus(ctx context.Context, src string) (string, error) {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("voice notes need ffmpeg, which is not installed")
	}
	if err := os.MkdirAll(media.TempDir(), 0o700); err != nil {
		return "", fmt.Errorf("failed to create media temp dir: %w", err)
	}

	// The name matters: channels detect voice notes by the "voice" prefix and
	// the .ogg suffix.
	outPath := filepath.Join(media.TempDir(), fmt.Sprintf("voice_%d.ogg", time.Now().UnixNano()))
	args := []string{
		"-y", "-loglevel", "error",
		"-i", src,
		"-vn",
		"-c:a", "libopus",
		"-b:a", "32k",
		"-ar", "48000",
		"-ac", "1",
		"-f", "ogg",
		outPath,
	}

	runCtx, cancel := context.WithTimeout(ctx, opusTranscodeTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args...)
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined
	if err := isolation.Run(cmd); err != nil {
		os.Remove(outPath)
		return "", fmt.Errorf("ffmpeg opus transcode failed: %w: %s", err, strings.TrimSpace(combined.String()))
	}
	return outPath, nil
}
