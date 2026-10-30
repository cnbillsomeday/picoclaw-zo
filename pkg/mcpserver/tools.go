package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/fish"
	"github.com/sipeed/picoclaw/pkg/memory"
)

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(body)}},
	}, nil, nil
}

func errResult(format string, args ...any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}, nil, nil
}

func argString(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func argInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func argFloat(args map[string]any, key string) float64 {
	v, _ := args[key].(float64)
	return v
}

func argBool(args map[string]any, key string, def bool) bool {
	v, ok := args[key].(bool)
	if !ok {
		return def
	}
	return v
}

func argStrings(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func readWorkspaceFile(workspace, name string) string {
	raw, err := os.ReadFile(filepath.Join(workspace, name))
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s *Server) registerTools() {
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "profile_get",
		Description: "Read picoclaw's persona (AGENT.md, MEMORY.md) and current mood line.",
	}, s.toolProfile)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "memory_context",
		Description: "Recall memory cards for a topic and return a ready-to-inject context block.",
	}, s.toolMemoryContext)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "memory_search",
		Description: "Search memory cards and return matching cards with text and source.",
	}, s.toolMemorySearch)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "mood_show",
		Description: "Show the current mood computed from mood events.",
	}, s.toolMoodShow)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "session_read",
		Description: "Read a session transcript. Use a picoclaw session key for owner sessions, or ext/<id> for your own archived sessions.",
	}, s.toolSessionRead)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "session_begin",
		Description: "Start an external session and get persona + recalled memory as initial context.",
	}, s.toolSessionBegin)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "session_append",
		Description: "Append verbatim messages to an external session transcript.",
	}, s.toolSessionAppend)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "session_end",
		Description: "Close an external session and store its summary.",
	}, s.toolSessionEnd)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "remember",
		Description: "Store a distilled memory card. The caller is recorded as the card source.",
	}, s.toolRemember)

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:        "mood_add",
		Description: "Record a mood event (requires write_mood permission).",
	}, s.toolMoodAdd)
}

func (s *Server) toolProfile(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	mood, _ := fish.LoadMood(s.workspace)
	return jsonResult(map[string]any{
		"agent_md":  readWorkspaceFile(s.workspace, "AGENT.md"),
		"memory_md": readWorkspaceFile(s.workspace, "MEMORY.md"),
		"mood_line": mood.Line(),
	})
}

func (s *Server) toolMemoryContext(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	out, err := fish.RecallWorkspace(s.workspace, argString(args, "query"), argString(args, "session"), argInt(args, "limit"), false)
	if err != nil {
		return errResult("recall failed: %v", err)
	}
	return jsonResult(out)
}

func (s *Server) toolMemorySearch(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	cards, err := fish.LoadCards(s.workspace)
	if err != nil {
		return errResult("load cards failed: %v", err)
	}
	byID := make(map[string]fish.Card, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	out, err := fish.RecallCards(s.workspace, cards, argString(args, "query"), "", argInt(args, "limit"), false, time.Now())
	if err != nil {
		return errResult("recall failed: %v", err)
	}
	type hit struct {
		ID    string   `json:"id"`
		T     string   `json:"t"`
		Text  string   `json:"text"`
		Kind  string   `json:"kind"`
		Src   string   `json:"src"`
		Key   string   `json:"key,omitempty"`
		Score float64  `json:"score"`
		Tags  []string `json:"tags,omitempty"`
	}
	hits := make([]hit, 0, len(out.Picked))
	for _, p := range out.Picked {
		c, ok := byID[p.ID]
		if !ok {
			continue
		}
		h := hit{ID: c.ID, T: c.T, Text: c.Text, Kind: c.Kind, Src: c.Src, Score: p.Score, Tags: c.Tags}
		if c.Key != nil {
			h.Key = *c.Key
		}
		hits = append(hits, h)
	}
	return jsonResult(map[string]any{"total": out.Total, "cards": hits})
}

func (s *Server) toolMoodShow(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	mood, err := fish.LoadMood(s.workspace)
	if err != nil {
		return errResult("load mood failed: %v", err)
	}
	return jsonResult(map[string]any{
		"line":      mood.Line(),
		"value":     mood.Value,
		"baseline":  mood.Baseline,
		"emotions":  mood.Emo,
		"why":       mood.Why,
		"age_hours": mood.AgeHours,
	})
}

func (s *Server) toolSessionRead(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	agent, _ := agentFrom(ctx)
	key := argString(args, "session_key")
	limit := argInt(args, "limit")
	if key == "" {
		return errResult("session_key is required")
	}

	if id, ok := strings.CutPrefix(key, "ext/"); ok {
		meta, msgs, err := ReadSession(s.workspace, agent.Name, id, limit)
		if err != nil {
			return errResult("%v", err)
		}
		return jsonResult(map[string]any{"session": meta, "messages": msgs})
	}

	if !agent.CanReadMainSession() {
		return errResult("agent %q is not allowed to read owner sessions", agent.Name)
	}
	store, err := memory.NewJSONLStore(filepath.Join(s.workspace, "sessions"))
	if err != nil {
		return errResult("open session store failed: %v", err)
	}
	defer func() { _ = store.Close() }()

	history, err := store.GetHistory(ctx, key)
	if err != nil {
		return errResult("read session failed: %v", err)
	}
	if limit > 0 && len(history) > limit {
		history = history[len(history)-limit:]
	}
	msgs := make([]TranscriptMessage, 0, len(history))
	for _, m := range history {
		msg := TranscriptMessage{Role: m.Role, Content: m.Content}
		if m.CreatedAt != nil {
			msg.TS = m.CreatedAt.UTC().Format(time.RFC3339)
		}
		msgs = append(msgs, msg)
	}
	summary, _ := store.GetSummary(ctx, key)
	return jsonResult(map[string]any{
		"session_key": key,
		"summary":     summary,
		"messages":    msgs,
	})
}

func (s *Server) toolSessionBegin(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	agent, _ := agentFrom(ctx)
	topic := argString(args, "topic")
	meta, err := BeginSession(s.workspace, agent.Name, topic)
	if err != nil {
		return errResult("begin session failed: %v", err)
	}

	out := map[string]any{"session_id": meta.ID, "started_at": meta.StartedAt}
	if argBool(args, "with_context", true) {
		mood, _ := fish.LoadMood(s.workspace)
		recall, _ := fish.RecallWorkspace(s.workspace, topic, "", argInt(args, "limit"), false)
		out["context"] = map[string]any{
			"agent_md":      readWorkspaceFile(s.workspace, "AGENT.md"),
			"memory_block":  recall.Block,
			"memory_picked": recall.Picked,
			"mood_line":     mood.Line(),
		}
	}
	return jsonResult(out)
}

func (s *Server) toolSessionAppend(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	agent, _ := agentFrom(ctx)
	raw, ok := args["messages"].([]any)
	if !ok || len(raw) == 0 {
		return errResult("messages must be a non-empty array")
	}
	msgs := make([]TranscriptMessage, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return errResult("messages[%d] must be an object", i)
		}
		msgs = append(msgs, TranscriptMessage{
			Role:    argString(m, "role"),
			Content: argString(m, "content"),
			Name:    argString(m, "name"),
			TS:      argString(m, "ts"),
		})
	}
	n, err := AppendMessages(s.workspace, agent.Name, argString(args, "session_id"), msgs)
	if err != nil {
		return errResult("%v", err)
	}
	return jsonResult(map[string]any{"appended": n})
}

func (s *Server) toolSessionEnd(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	agent, _ := agentFrom(ctx)
	meta, err := EndSession(s.workspace, agent.Name, argString(args, "session_id"), argString(args, "summary"))
	if err != nil {
		return errResult("%v", err)
	}
	return jsonResult(meta)
}

func (s *Server) toolRemember(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	agent, _ := agentFrom(ctx)
	if !agent.CanWriteMemory() {
		return errResult("agent %q is not allowed to write memory", agent.Name)
	}
	card, replaced, err := fish.AddCard(s.workspace, fish.NewCard{
		Text: argString(args, "text"),
		Key:  argString(args, "key"),
		Kind: argString(args, "kind"),
		Tags: argStrings(args, "tags"),
		Imp:  argFloat(args, "imp"),
		Src:  agent.Name,
	})
	if err != nil {
		return errResult("remember failed: %v", err)
	}
	return jsonResult(map[string]any{"id": card.ID, "replaced": replaced, "src": card.Src})
}

func (s *Server) toolMoodAdd(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
	agent, _ := agentFrom(ctx)
	if !agent.CanWriteMood() {
		return errResult("agent %q is not allowed to write mood", agent.Name)
	}
	emo := map[string]float64{}
	if raw, ok := args["emotions"].(map[string]any); ok {
		for k, v := range raw {
			if f, ok := v.(float64); ok {
				emo[k] = f
			}
		}
	}
	d := argFloat(args, "d")
	if d == 0 {
		d = 3
	}
	state, err := fish.AddMoodEvent(s.workspace, d, emo, argString(args, "why"), "mcp:"+agent.Name, time.Now())
	if err != nil {
		return errResult("mood_add failed: %v", err)
	}
	return jsonResult(map[string]any{"line": state.Line(), "value": state.Value})
}
