package a2a

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

type testLLMPayload struct {
	InputTokens  int
	OutputTokens int
}

type testTurnEndPayload struct {
	Status       string
	FinalContent string
	Iterations   int
}

func TestRecordLLMUsageAggregatesPerTurn(t *testing.T) {
	c := &Channel{}
	c.recordLLMUsage("turn-1", testLLMPayload{InputTokens: 5, OutputTokens: 7})
	c.recordLLMUsage("turn-1", testLLMPayload{InputTokens: 3, OutputTokens: 4})
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
	c.recordLLMUsage("turn-1", testLLMPayload{InputTokens: 2, OutputTokens: 3})

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

// TestEmptyScopeTurnEndIgnored: a turn.end whose scope carries no channel
// (cron jobs, fish speak-up turns) must never settle an A2A inbound, even
// when it is the only pending peer.
func TestEmptyScopeTurnEndIgnored(t *testing.T) {
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
		peer:    "bob",
		inbound: pendingInbound{peer: "bob", convID: "conv-1", msgID: "msg-1", attempt: attempt},
	})

	// No channel, no chat id in scope: this is what cron/fish turns emit.
	evt := runtimeevents.Event{
		Kind:  runtimeevents.KindAgentTurnEnd,
		Scope: runtimeevents.Scope{TurnID: "turn-cron"},
		Payload: testTurnEndPayload{
			Status: "completed", FinalContent: "speak line", Iterations: 1,
		},
	}
	if err := c.handleRuntimeEvent(context.Background(), evt); err != nil {
		t.Fatalf("handle: %v", err)
	}

	c.pendMu.Lock()
	n := len(c.pendingByPeer)
	c.pendMu.Unlock()
	if n != 1 {
		t.Fatalf("pending settled by an unrelated empty-scope turn.end: %d left", n)
	}
	if got := st.InboundEntry("conv-1", "msg-1"); got == nil || got.Status != "pending" {
		t.Fatalf("state entry = %+v, want still pending", got)
	}
	// The cron turn's usage must not accumulate either.
	c.recordLLMUsage("", testLLMPayload{InputTokens: 1, OutputTokens: 1})
	c.usageMu.Lock()
	emptyOK := len(c.usageByTurn) == 0
	c.usageMu.Unlock()
	if !emptyOK {
		t.Fatal("empty TurnID usage was stored")
	}
}

// TestStaleUsagePruned: entries whose turn never ends are evicted after the
// grace TTL instead of leaking.
func TestStaleUsagePruned(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	c := &Channel{
		inboundTimeout: time.Minute,
		now:            func() time.Time { return start },
	}
	c.recordLLMUsage("turn-old", testLLMPayload{InputTokens: 1, OutputTokens: 1})
	if got := c.takeTurnUsage("turn-old"); got == (turnUsage{}) {
		t.Fatal("usage not recorded")
	}

	// Re-add, then advance past 2*inboundTimeout: gc on the next record
	// must drop the stale entry.
	c.recordLLMUsage("turn-old", testLLMPayload{InputTokens: 2, OutputTokens: 2})
	c.now = func() time.Time { return start.Add(3 * time.Minute) }
	c.recordLLMUsage("turn-fresh", testLLMPayload{InputTokens: 1, OutputTokens: 1})
	c.usageMu.Lock()
	_, stale := c.usageByTurn["turn-old"]
	fresh, ok := c.usageByTurn["turn-fresh"]
	c.usageMu.Unlock()
	if stale {
		t.Fatal("stale usage entry was not pruned")
	}
	if !ok || fresh.usage.TokensIn != 1 {
		t.Fatalf("fresh entry wrong: %+v", fresh)
	}
}

// TestPrunePendingByPeer: when the state entry is no longer pending
// (expired/settled), the in-memory map entry must go too.
func TestPrunePendingByPeer(t *testing.T) {
	dir := t.TempDir()
	st, err := loadChannelState(filepath.Join(dir, "state.json"), "alice")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	now := time.Unix(1_700_000_000, 0)
	attempt := st.MarkInboundPending("conv-1", "msg-1", now, "hello", now)
	c := &Channel{
		agentSlug:    "alice",
		state:        st,
		inboundRetry: time.Minute,
		now:          func() time.Time { return now },
	}
	c.registerPendingInbound(peerPending{
		peer:    "bob",
		inbound: pendingInbound{peer: "bob", convID: "conv-1", msgID: "msg-1", attempt: attempt},
	})

	// Still pending: prune keeps it.
	c.prunePendingByPeer()
	c.pendMu.Lock()
	if len(c.pendingByPeer) != 1 {
		t.Fatal("prune dropped a live pending entry")
	}
	c.pendMu.Unlock()

	// State transitions to failed (what ExpirePendingInbound does): prune
	// must now drop the map entry.
	st.MarkInboundFailed("conv-1", "msg-1", now, time.Minute, "boom")
	c.prunePendingByPeer()
	c.pendMu.Lock()
	if len(c.pendingByPeer) != 0 {
		t.Fatal("stale pending entry survived prune")
	}
	c.pendMu.Unlock()
}

// TestQueuedNoticeTTL: a reply-mode notice that sits in the queue longer
// than queuedNoticeTTL is dropped on flush instead of popping out later on
// an unrelated send; a fresh notice still publishes.
func TestQueuedNoticeTTL(t *testing.T) {
	st, err := loadChannelState(filepath.Join(t.TempDir(), "state.json"), "alice")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	base := time.Unix(1_700_000_000, 0)
	now := base
	c := &Channel{
		BaseChannel:   channels.NewBaseChannel("a2a", nil, nil, nil),
		agentSlug:     "alice",
		state:         st,
		msgBus:        bus.NewMessageBus(),
		notifyMode:    "reply",
		notifyChannel: "telegram",
		notifyChatID:  "owner-1",
		now:           func() time.Time { return now },
	}
	defer c.msgBus.Close()

	notice := ownerNotice{convID: "c1", msgID: "m1", peer: "bob", status: "completed", reply: "hello", inbound: "hi"}

	// Queue at t=base, then let 20 minutes pass before the flush: the notice
	// is stale and must be dropped (queue ends empty).
	c.queueOwnerNotification(notice)
	now = base.Add(20 * time.Minute)
	c.notifyReplySent("bob")
	c.noticeMu.Lock()
	if n := len(c.queuedNotice); n != 0 {
		t.Fatalf("stale notice kept in queue: %d", n)
	}
	c.noticeMu.Unlock()

	// A fresh notice at t=+20min flushes and reaches the outbound bus.
	fresh := notice
	fresh.msgID = "m2"
	c.queueOwnerNotification(fresh)
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case out := <-c.msgBus.OutboundChan():
			if out.Channel != "telegram" || out.ChatID != "owner-1" {
				t.Errorf("flush routed to %s/%s", out.Channel, out.ChatID)
			}
		case <-time.After(5 * time.Second):
			t.Error("fresh notice never published")
		}
	}()
	c.notifyReplySent("bob")
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("timed out waiting for publish goroutine")
	}
}

// TestSaveIfDirtySkipsUnchanged: a bare SetTick marks dirty (tick is a real
// change), but Save clears the flag so an unchanged second save skips the
// disk write.
func TestSaveIfDirtySkipsUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	st, err := loadChannelState(path, "alice")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	st.SetTick(now)
	if err := st.SaveIfDirty(); err != nil {
		t.Fatalf("first save: %v", err)
	}
	first, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Another tick-less save: nothing changed, must not rewrite the file.
	time.Sleep(10 * time.Millisecond)
	if err := st.SaveIfDirty(); err != nil {
		t.Fatal(err)
	}
	second, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !second.ModTime().Equal(first.ModTime()) {
		t.Fatal("unchanged state was rewritten to disk")
	}
	// A real change (MarkSeen) makes the next SaveIfDirty write again.
	st.MarkSeen("c1", "m1")
	if err := st.SaveIfDirty(); err != nil {
		t.Fatal(err)
	}
	third, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if third.ModTime().Equal(first.ModTime()) {
		t.Fatal("dirty state was not written")
	}
}
