package mcpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
)

// TestAppendMessagesConcurrentConsistent: concurrent appends must all be
// counted; the locked read-modify-write of meta.json is what guarantees it.
func TestAppendMessagesConcurrentConsistent(t *testing.T) {
	workspace := t.TempDir()
	meta, err := BeginSession(workspace, "claude-code", "topic")
	if err != nil {
		t.Fatal(err)
	}
	id := meta.ID

	const goroutines = 4
	const perGoroutine = 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				msg := TranscriptMessage{Role: "user", Content: fmt.Sprintf("g%d m%d", g, i)}
				if _, err := AppendMessages(workspace, "claude-code", id, []TranscriptMessage{msg}); err != nil {
					t.Errorf("append: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()

	final, err := readMeta(sessionsRoot(workspace, "claude-code") + "/" + id)
	if err != nil {
		t.Fatal(err)
	}
	want := goroutines * perGoroutine
	if final.Messages != want {
		t.Fatalf("meta.Messages = %d, want %d (lost appends)", final.Messages, want)
	}
}

// TestEndSessionNoResurrection: once closed, appends must fail and the meta
// must stay closed with a consistent count. Before the session lock an
// append that read meta as open could write it back after EndSession,
// resurrecting the session.
func TestEndSessionNoResurrection(t *testing.T) {
	workspace := t.TempDir()
	meta, err := BeginSession(workspace, "claude-code", "topic")
	if err != nil {
		t.Fatal(err)
	}
	id := meta.ID

	for i := 0; i < 5; i++ {
		if _, err := AppendMessages(workspace, "claude-code", id, []TranscriptMessage{
			{Role: "user", Content: "msg"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EndSession(workspace, "claude-code", id, "done"); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendMessages(workspace, "claude-code", id, []TranscriptMessage{
		{Role: "user", Content: "too late"},
	}); err == nil {
		t.Fatal("append to closed session must fail")
	}

	final, err := readMeta(sessionsRoot(workspace, "claude-code") + "/" + id)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != sessionStatusClosed {
		t.Fatalf("status = %q, want closed (resurrected)", final.Status)
	}
	if final.Messages != 5 {
		t.Fatalf("messages = %d, want 5", final.Messages)
	}
}

// TestSessionDirRejectsTraversal: ids that escape the agent directory are
// rejected outright.
func TestSessionDirRejectsTraversal(t *testing.T) {
	workspace := t.TempDir()
	for _, bad := range []string{".", "..", "../evil", "a/b", "a\\b"} {
		if _, err := sessionDir(workspace, "agent", bad); err == nil {
			t.Fatalf("sessionDir accepted %q", bad)
		}
		if _, err := AppendMessages(workspace, "agent", bad, []TranscriptMessage{{Role: "user"}}); err == nil {
			t.Fatalf("AppendMessages accepted id %q", bad)
		}
	}
}

// TestLoadConfigRejectsDotNames: "." and ".." sanitize to themselves and
// used to pass validation, escaping sessionsRoot to the workspace root.
func TestLoadConfigRejectsDotNames(t *testing.T) {
	dir := t.TempDir()
	mcpDir := dir + "/mcp"
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(names ...string) {
		var agents string
		for _, n := range names {
			agents += fmt.Sprintf(`{"name":%q,"token":"tok"},`, n)
		}
		body := fmt.Sprintf(`{"agents":[%s]}`, agents[:len(agents)-1])
		if err := os.WriteFile(mcpDir+"/config.json", []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("..")
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("config with agent name '..' accepted")
	}
	write(".")
	if _, err := LoadConfig(dir); err == nil {
		t.Fatal("config with agent name '.' accepted")
	}
	write("ok-agent")
	if _, err := LoadConfig(dir); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

// TestBearerParsingStrict: the authenticate middleware must require the
// canonical "Bearer <token>" form (space mandatory); "Bearer<token>" and a
// bare token must 401. Verified end-to-end over httptest through the real
// Handler so the middleware wiring is covered too.
func TestBearerParsingStrict(t *testing.T) {
	cfg := Config{
		Host: "127.0.0.1", Port: 0,
		Agents: []Agent{{Name: "agent", Token: "secret-token"}},
	}
	srv := &Server{cfg: cfg}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"bare token", "secret-token", http.StatusUnauthorized},
		{"no space", "Bearersecret-token", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		// Authenticated GET is rejected by the MCP protocol layer itself
		// (400), which proves the request passed auth.
		{"valid", "Bearer secret-token", http.StatusBadRequest},
		{"valid padded", "Bearer  secret-token", http.StatusBadRequest},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/mcp", nil)
		if tc.header != "" {
			req.Header.Set("Authorization", tc.header)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
}

// TestAppendContentVerbatim: transcript content must be stored exactly as
// sent; trimming whitespace is batch-3 regression behavior.
func TestAppendContentVerbatim(t *testing.T) {
	workspace := t.TempDir()
	meta, err := BeginSession(workspace, "agent", "t")
	if err != nil {
		t.Fatal(err)
	}
	raw := "  padded line with trailing spaces   "
	if _, err := AppendMessages(workspace, "agent", meta.ID, []TranscriptMessage{
		{Role: "user", Content: raw},
	}); err != nil {
		t.Fatal(err)
	}
	_, lines, err := ReadSession(workspace, "agent", meta.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0].Content != raw {
		t.Fatalf("content altered: %q", lines[0].Content)
	}
}

// TestNewSessionIDUniqueness: ids carry 8 random bytes; 1000 ids generated
// in the same second must all differ.
func TestNewSessionIDUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := newSessionID()
		if seen[id] {
			t.Fatalf("duplicate session id %q at i=%d", id, i)
		}
		seen[id] = true
	}
}
