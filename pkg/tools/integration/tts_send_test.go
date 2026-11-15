package integrationtools

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/media"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

type stubTTS struct{}

func (stubTTS) Name() string { return "stub-tts" }
func (stubTTS) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(strings.Repeat("x", 2048))), nil
}

func TestSendTTSToolDeliversVoiceNoteWithoutText(t *testing.T) {
	store := media.NewFileMediaStore()
	tool := NewSendTTSTool(stubTTS{}, store)
	ctx := toolshared.WithToolContext(context.Background(), "telegram", "chat-1")
	res := tool.Execute(ctx, map[string]any{"text": "hello"})
	if res.Err != nil {
		t.Fatalf("Execute error: %v", res.Err)
	}
	if res.ForUser != "" {
		t.Fatalf("ForUser = %q, want empty (no duplicate text)", res.ForUser)
	}
	if len(res.Media) != 1 {
		t.Fatalf("media parts = %d", len(res.Media))
	}
	path, meta, err := store.ResolveWithMeta(res.Media[0])
	if err != nil {
		t.Fatal(err)
	}
	name := strings.ToLower(filepath.Base(path))
	if meta.Filename != "" && strings.Contains(strings.ToLower(meta.Filename), "voice") {
		name = strings.ToLower(meta.Filename)
	}
	if !strings.HasSuffix(name, ".ogg") {
		t.Fatalf("voice filename = %q, want .ogg", name)
	}
}
