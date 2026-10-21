package a2a

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/channels"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

type testLLMPayload struct {
	TokensIn  int
	TokensOut int
}

type testTurnEndPayload struct {
	Status       string
	FinalContent string
	Iterations   int
}

func TestRecordLLMUsageAggregatesPerTurn(t *testing.T) {
	c := &Channel{}
	c.recordLLMUsage("turn-1", testLLMPayload{TokensIn: 5, TokensOut: 7})
	c.recordLLMUsage("turn-1", testLLMPayload{TokensIn: 3, TokensOut: 4})
	usage := c.takeTurnUsage("turn-1")
	if usage.TokensIn != 8 || usage.TokensOut != 11 || usage.LLMCalls != 2 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
	if usage.TotalTokens() != 19 {
		t.Fatalf("total = %d", usage.TotalTokens())
	}
	if other := c.takeTurnUsage("turn-1"); other != (turnUsage{}) {
		t.Fatalf("usage must be consumed, got %+v", other)
	}
}

func TestTurnEndEventSettlesPendingInbound(t *testing.T) {
	dir := t.TempDir()
	st, err := loadChannelState(filepath.Join(dir, "state.json"), "alice")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	attempt := st.MarkInboundPending("conv-1", "msg-1", now, "hello", now)

	c := &Channel{
		BaseChannel:        channels.NewBaseChannel("a2a", nil, nil, nil),
		agentSlug:          "alice",
		state:              st,
		notifyMode:         "off",
		inboundRetry:       time.Minute,
		inboundMaxAttempts: 3,
		now:                func() time.Time { return now },
	}
	c.registerPendingInbound(peerPending{
		peer: "bob",
		inbound: pendingInbound{
			peer: "bob", convID: "conv-1", msgID: "msg-1", attempt: attempt,
		},
	})
	c.recordLLMUsage("turn-1", testLLMPayload{TokensIn: 2, TokensOut: 3})

	evt := runtimeevents.Event{
		Kind: runtimeevents.KindAgentTurnEnd,
		Scope: runtimeevents.Scope{
			Channel: "a2a",
			ChatID:  "bob",
			TurnID:  "turn-1",
		},
		Payload: testTurnEndPayload{Status: "failed", Iterations: 2},
	}
	if err := c.handleRuntimeEvent(context.Background(), evt); err != nil {
		t.Fatalf("handle: %v", err)
	}

	c.pendMu.Lock()
	pending := len(c.pendingByPeer)
	c.pendMu.Unlock()
	if pending != 0 {
		t.Fatalf("pending entry not settled: %d", pending)
	}
	entry := st.InboundEntry("conv-1", "msg-1")
	if entry == nil || entry.Status != "failed" {
		t.Fatalf("state entry = %+v", entry)
	}
	if usage := c.takeTurnUsage("turn-1"); usage != (turnUsage{}) {
		t.Fatalf("usage not consumed: %+v", usage)
	}
}

func TestPayloadReflection(t *testing.T) {
	payload := testTurnEndPayload{Status: "completed", FinalContent: "hi", Iterations: 4}
	if got := payloadStringField(payload, "Status"); got != "completed" {
		t.Fatalf("status = %q", got)
	}
	if got := payloadStringField(payload, "FinalContent"); got != "hi" {
		t.Fatalf("content = %q", got)
	}
	if got := payloadIntField(payload, "Iterations"); got != 4 {
		t.Fatalf("iterations = %d", got)
	}
	if got := payloadStringField(&payload, "FinalContent"); got != "hi" {
		t.Fatalf("pointer payload = %q", got)
	}
}
