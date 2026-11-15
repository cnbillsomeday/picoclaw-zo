package audio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// FFmpegPath locates the ffmpeg binary. Deployments that ship ffmpeg next to the
// picoclaw executable (or set PICOCLAW_FFMPEG) must not depend on the process
// PATH: on Windows the launcher may start the gateway with a PATH that lacks
// the install directory, which silently disabled voice-note conversion.
func FFmpegPath() (string, error) {
	if configured := os.Getenv("PICOCLAW_FFMPEG"); configured != "" {
		return configured, nil
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		names := []string{"ffmpeg"}
		if runtime.GOOS == "windows" {
			names = []string{"ffmpeg.exe", "ffmpeg"}
		}
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
				return candidate, nil
			}
		}
	}
	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		return "", fmt.Errorf("ffmpeg not found: install it, put it next to the picoclaw binary, or set PICOCLAW_FFMPEG")
	}
	return path, nil
}
