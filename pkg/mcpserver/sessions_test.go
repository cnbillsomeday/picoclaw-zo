package mcpserver

import (
	"strings"
	"testing"
)

func TestExternalSessionRoundtrip(t *testing.T) {
	ws := t.TempDir()

	meta, err := BeginSession(ws, "claude", "写周报")
	if err != nil {
		t.Fatalf("BeginSession() error = %v", err)
	}
	if meta.Status != sessionStatusOpen || meta.Agent != "claude" {
		t.Fatalf("unexpected meta: %+v", meta)
	}

	n, err := AppendMessages(ws, "claude", meta.ID, []TranscriptMessage{
		{Role: "user", Content: "帮我写周报"},
		{Role: "assistant", Content: "好的"},
	})
	if err != nil || n != 2 {
		t.Fatalf("AppendMessages() = %d, %v", n, err)
	}

	got, msgs, err := ReadSession(ws, "claude", meta.ID, 0)
	if err != nil {
		t.Fatalf("ReadSession() error = %v", err)
	}
	if got.Messages != 2 || len(msgs) != 2 {
		t.Fatalf("messages = %d/%d, want 2/2", got.Messages, len(msgs))
	}
	if msgs[0].TS == "" {
		t.Fatal("timestamp was not filled in")
	}

	_, limited, err := ReadSession(ws, "claude", meta.ID, 1)
	if err != nil || len(limited) != 1 || limited[0].Content != "好的" {
		t.Fatalf("limit read = %+v, %v", limited, err)
	}

	closed, err := EndSession(ws, "claude", meta.ID, "周报已写")
	if err != nil {
		t.Fatalf("EndSession() error = %v", err)
	}
	if closed.Status != sessionStatusClosed || closed.Summary != "周报已写" {
		t.Fatalf("unexpected closed meta: %+v", closed)
	}

	if _, err := AppendMessages(ws, "claude", meta.ID, []TranscriptMessage{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("append to closed session should fail")
	}
}

func TestSessionAccessIsScopedToAgent(t *testing.T) {
	ws := t.TempDir()
	meta, err := BeginSession(ws, "claude", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadSession(ws, "other", meta.ID, 0); err == nil {
		t.Fatal("another agent must not read this session")
	}
	if _, err := AppendMessages(ws, "claude", "../escape", []TranscriptMessage{{Role: "user", Content: "x"}}); err == nil {
		t.Fatal("path traversal session id must be rejected")
	}
}

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"claude-code":  "claude-code",
		"hao":          "hao",
		"a b/c":        "a_b_c",
		"../../etc":    ".._.._etc",
		"agent@tail.1": "agent_tail.1",
	} {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeName(strings.Repeat("a", 100)); len(got) != 64 {
		t.Errorf("long name not truncated: %d", len(got))
	}
}
