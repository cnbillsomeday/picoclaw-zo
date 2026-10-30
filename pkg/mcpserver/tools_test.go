package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/fish"
	"github.com/sipeed/picoclaw/pkg/memory"
)

func agentCtx(a Agent) context.Context {
	return context.WithValue(context.Background(), agentContextKey, a)
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	if res == nil || len(res.Content) == 0 {
		t.Fatal("empty tool result")
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	return text.Text
}

func newToolServer(t *testing.T) (*Server, Agent) {
	t.Helper()
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "AGENT.md"), []byte("你是郝，zlin 的姐姐。"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(ws, DefaultConfig())
	return srv, Agent{Name: "claude", Token: "secret"}
}

func TestProfileAndRememberFlow(t *testing.T) {
	srv, agent := newToolServer(t)
	ctx := agentCtx(agent)

	res, _, err := srv.toolProfile(ctx, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, res), "你是郝") {
		t.Fatal("profile should include AGENT.md")
	}

	// A single-card corpus gives every token zero IDF, so seed two unrelated
	// cards first; otherwise recall legitimately returns nothing.
	for _, text := range []string{"今天的天气不错", "冰箱里有牛奶"} {
		if _, _, err := fish.AddCard(srv.workspace, fish.NewCard{Text: text}); err != nil {
			t.Fatal(err)
		}
	}

	res, _, err = srv.toolRemember(ctx, nil, map[string]any{
		"text": "zlin 喜欢 durian 味的咖啡",
		"key":  "zlin.咖啡",
		"kind": "fact",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, res), `"src": "claude"`) {
		t.Fatalf("card source should be the agent: %s", resultText(t, res))
	}

	res, _, err = srv.toolMemorySearch(ctx, nil, map[string]any{"query": "zlin durian 咖啡"})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "durian") || !strings.Contains(text, `"src": "claude"`) {
		t.Fatalf("search should return the new card with source: %s", text)
	}

	res, _, err = srv.toolMemoryContext(ctx, nil, map[string]any{"query": "zlin durian 咖啡"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, res), "durian") {
		t.Fatal("context block should mention the card")
	}
}

func TestWritePermissions(t *testing.T) {
	srv, agent := newToolServer(t)

	deny := false
	res, _, err := srv.toolRemember(agentCtx(Agent{Name: "reader", WriteMemory: &deny}), nil, map[string]any{"text": "x"})
	if err != nil || !res.IsError {
		t.Fatal("write_memory=false must be rejected")
	}

	res, _, err = srv.toolMoodAdd(agentCtx(agent), nil, map[string]any{"why": "test"})
	if err != nil || !res.IsError {
		t.Fatal("mood_add should be off by default")
	}

	allow := true
	res, _, err = srv.toolMoodAdd(agentCtx(Agent{Name: "moody", WriteMood: &allow}), nil, map[string]any{
		"d": 4.0, "why": "测试开心", "emotions": map[string]any{"开心": 4.0},
	})
	if err != nil || res.IsError {
		t.Fatalf("mood_add with permission failed: %v %s", err, resultText(t, res))
	}

	res, _, err = srv.toolMoodShow(agentCtx(agent), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, res), "测试开心") {
		t.Fatal("mood_show should include the latest event")
	}
}

func TestExternalSessionTools(t *testing.T) {
	srv, agent := newToolServer(t)
	ctx := agentCtx(agent)

	res, _, err := srv.toolSessionBegin(ctx, nil, map[string]any{"topic": "周报"})
	if err != nil {
		t.Fatal(err)
	}
	text := resultText(t, res)
	if !strings.Contains(text, "session_id") || !strings.Contains(text, "你是郝") {
		t.Fatalf("begin should return id and context: %s", text)
	}
	var begun struct {
		SessionID string `json:"session_id"`
	}
	if err := jsonUnmarshal(text, &begun); err != nil {
		t.Fatal(err)
	}

	res, _, err = srv.toolSessionAppend(ctx, nil, map[string]any{
		"session_id": begun.SessionID,
		"messages": []any{
			map[string]any{"role": "user", "content": "写周报"},
			map[string]any{"role": "assistant", "content": "已完成"},
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("append failed: %v %s", err, resultText(t, res))
	}

	res, _, err = srv.toolSessionRead(ctx, nil, map[string]any{"session_key": "ext/" + begun.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, res), "已完成") {
		t.Fatal("transcript should be readable")
	}

	res, _, err = srv.toolSessionEnd(ctx, nil, map[string]any{"session_id": begun.SessionID, "summary": "done"})
	if err != nil || res.IsError {
		t.Fatalf("end failed: %v %s", err, resultText(t, res))
	}
}

func TestMainSessionRead(t *testing.T) {
	srv, agent := newToolServer(t)
	store, err := memory.NewJSONLStore(filepath.Join(srv.workspace, "sessions"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddMessage(context.Background(), "agent:main:s1", "user", "主会话里的一句话"); err != nil {
		t.Fatal(err)
	}
	_ = store.Close()

	res, _, err := srv.toolSessionRead(agentCtx(agent), nil, map[string]any{"session_key": "agent:main:s1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resultText(t, res), "主会话里的一句话") {
		t.Fatal("main session should be readable by default")
	}

	deny := false
	res, _, err = srv.toolSessionRead(agentCtx(Agent{Name: "limited", ReadMainSession: &deny}), nil, map[string]any{"session_key": "agent:main:s1"})
	if err != nil || !res.IsError {
		t.Fatal("read_main_session=false must be rejected")
	}
}

func TestHTTPAuthentication(t *testing.T) {
	ws := t.TempDir()
	srv := New(ws, Config{Host: "127.0.0.1", Port: 18901, Agents: []Agent{{Name: "claude", Token: "secret"}}})
	handler := srv.Handler()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token should be 401, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer secret")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("valid token should pass authentication")
	}

	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestCardsLockAcrossProcesses(t *testing.T) {
	ws := t.TempDir()
	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(i int) {
			_, _, err := fish.AddCard(ws, fish.NewCard{Text: "并发卡片"})
			done <- err
		}(i)
	}
	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent AddCard failed: %v", err)
		}
	}
	cards, err := fish.LoadCards(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 20 {
		t.Fatalf("cards = %d, want 20 (lost updates)", len(cards))
	}
}

func jsonUnmarshal(text string, v any) error {
	return json.Unmarshal([]byte(text), v)
}
