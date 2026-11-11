// PicoClaw - Ultra-lightweight personal AI agent

package a2a

import (
	"context"
	"reflect"
	"strings"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// turnUsageEntry accumulates one turn's token usage; `at` bounds how long a
// lost turn.end may leave the entry in memory.
type turnUsageEntry struct {
	usage turnUsage
	at    time.Time
}

// pendingInbound is one delivered message waiting for its turn to finish.
type pendingInbound struct {
	peer      string
	convID    string
	msgID     string
	attempt   int
	preview   string
	inboundAt time.Time
}

// SetRuntimeEvents is an optional channel lifecycle hook: the channel manager
// injects the runtime event bus when the channel implements this method. The
// A2A channel subscribes to turn events to settle inbound messages and render
// the owner wake summary.
func (c *Channel) SetRuntimeEvents(bus runtimeevents.Bus) {
	if c == nil {
		return
	}
	c.events = bus
}

func (c *Channel) startRuntimeEvents() {
	if c == nil || c.events == nil || c.ctx == nil {
		return
	}
	sub, err := c.events.Channel().
		OfKind(runtimeevents.KindAgentTurnEnd, runtimeevents.KindAgentLLMResponse).
		Subscribe(c.ctx, runtimeevents.SubscribeOptions{
			Name:        "a2a-" + c.agentSlug,
			Buffer:      64,
			Concurrency: runtimeevents.Locked,
		}, c.handleRuntimeEvent)
	if err != nil {
		logger.WarnCF("a2a", "failed to subscribe runtime events; wake summaries disabled", map[string]any{
			"agent": c.agentSlug,
			"error": err.Error(),
		})
		return
	}
	c.eventSub = sub
	logger.InfoCF("a2a", "runtime event subscription started", map[string]any{
		"agent": c.agentSlug,
	})
}

func (c *Channel) stopRuntimeEvents() {
	if c == nil || c.eventSub == nil {
		return
	}
	_ = c.eventSub.Close()
	c.eventSub = nil
}

func (c *Channel) handleRuntimeEvent(_ context.Context, evt runtimeevents.Event) error {
	if c == nil {
		return nil
	}
	// Strict scope match: a turn.end with an empty Channel scope (cron jobs,
	// fish speak-up turns, CLI turns) must never settle an A2A inbound.
	if c.BaseChannel != nil && !strings.EqualFold(evt.Scope.Channel, c.Name()) {
		return nil
	}
	switch evt.Kind {
	case runtimeevents.KindAgentLLMResponse:
		c.recordLLMUsage(evt.Scope.TurnID, evt.Payload)
	case runtimeevents.KindAgentTurnEnd:
		c.handleTurnEndEvent(evt)
	}
	return nil
}

// recordLLMUsage accumulates token usage per turn. The payload is read via
// reflection so the channel package does not import pkg/agent (which imports
// pkg/channels and would form an import cycle).
func (c *Channel) recordLLMUsage(turnID string, payload any) {
	if c == nil {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		// Untracked turn (e.g. a proactive cron turn): aggregating under ""
		// would leak memory because no turn.end can ever consume it.
		return
	}
	in := payloadIntField(payload, "InputTokens")
	out := payloadIntField(payload, "OutputTokens")
	now := c.currentTime()
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if c.usageByTurn == nil {
		c.usageByTurn = map[string]turnUsageEntry{}
	}
	entry := c.usageByTurn[turnID]
	entry.usage.TokensIn += in
	entry.usage.TokensOut += out
	entry.usage.LLMCalls++
	if entry.at.IsZero() {
		entry.at = now
	}
	c.usageByTurn[turnID] = entry
	c.gcTurnUsageLocked(now)
}

// gcTurnUsageLocked drops usage entries whose turn ended (or was lost) long
// ago; without this a missed turn.end leaks the entry for the channel's
// lifetime.
func (c *Channel) gcTurnUsageLocked(now time.Time) {
	ttl := c.inboundTimeout
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	// Keep generous headroom above the turn timeout so a slow turn that
	// eventually ends still finds its usage.
	ttl *= 2
	for turnID, entry := range c.usageByTurn {
		if now.Sub(entry.at) > ttl {
			delete(c.usageByTurn, turnID)
		}
	}
}

func (c *Channel) takeTurnUsage(turnID string) turnUsage {
	if c == nil {
		return turnUsage{}
	}
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	entry, ok := c.usageByTurn[turnID]
	if !ok {
		return turnUsage{}
	}
	delete(c.usageByTurn, turnID)
	return entry.usage
}

func (c *Channel) handleTurnEndEvent(evt runtimeevents.Event) {
	peer := strings.TrimSpace(evt.Scope.ChatID)
	if peer == "" {
		// Fall back to the sole pending peer when the event scope omits chat.
		peer = c.solePendingPeer()
	}
	if peer == "" {
		return
	}
	c.pendMu.Lock()
	pending, ok := c.pendingByPeer[peer]
	if ok {
		delete(c.pendingByPeer, peer)
	}
	c.pendMu.Unlock()
	if !ok {
		return
	}

	status := strings.TrimSpace(payloadStringField(evt.Payload, "Status"))
	if status == "" {
		status = "error"
	}
	reply := payloadStringField(evt.Payload, "FinalContent")
	iterations := payloadIntField(evt.Payload, "Iterations")
	usage := c.takeTurnUsage(evt.Scope.TurnID)
	usage.Iterations = iterations

	result := inboundResult{Status: status, Reply: reply, Usage: usage}
	if !strings.EqualFold(status, "completed") {
		result.Error = "turn ended with status " + status
	}
	c.handleInboundResult(pending.convID, peer, pending.msgID, pending.attempt, result)
}

// prunePendingByPeer drops in-memory pending entries whose state entry is no
// longer "pending" (settled, retried or expired). The poller expires stale
// state entries via ExpirePendingInbound but that never touched this map, so
// a lost turn.end used to leave the entry here until the peer's next message
// overwrote it.
func (c *Channel) prunePendingByPeer() {
	if c == nil || c.state == nil {
		return
	}
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	for peer, p := range c.pendingByPeer {
		entry := c.state.InboundEntry(p.convID, p.msgID)
		if entry == nil || entry.Status != "pending" {
			delete(c.pendingByPeer, peer)
		}
	}
}

func (c *Channel) solePendingPeer() string {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	if len(c.pendingByPeer) != 1 {
		return ""
	}
	for peer := range c.pendingByPeer {
		return peer
	}
	return ""
}

func (c *Channel) registerPendingInbound(p peerPending) {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	if c.pendingByPeer == nil {
		c.pendingByPeer = map[string]pendingInbound{}
	}
	c.pendingByPeer[p.peer] = p.inbound
}

type peerPending struct {
	peer    string
	inbound pendingInbound
}

func payloadStringField(payload any, name string) string {
	v := reflect.ValueOf(payload)
	if !v.IsValid() {
		return ""
	}
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName(name)
	if !f.IsValid() || f.Kind() != reflect.String {
		return ""
	}
	return f.String()
}

func payloadIntField(payload any, name string) int {
	v := reflect.ValueOf(payload)
	if !v.IsValid() {
		return 0
	}
	if v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0
	}
	f := v.FieldByName(name)
	if !f.IsValid() {
		return 0
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(f.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int(f.Uint())
	}
	return 0
}
