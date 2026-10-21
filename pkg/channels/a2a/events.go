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
	if evt.Scope.Channel != "" && c.BaseChannel != nil && !strings.EqualFold(evt.Scope.Channel, c.Name()) {
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
	in := payloadIntField(payload, "TokensIn")
	out := payloadIntField(payload, "TokensOut")
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	if c.usageByTurn == nil {
		c.usageByTurn = map[string]*turnUsage{}
	}
	u := c.usageByTurn[turnID]
	if u == nil {
		u = &turnUsage{}
		c.usageByTurn[turnID] = u
	}
	u.TokensIn += in
	u.TokensOut += out
	u.LLMCalls++
}

func (c *Channel) takeTurnUsage(turnID string) turnUsage {
	if c == nil {
		return turnUsage{}
	}
	c.usageMu.Lock()
	defer c.usageMu.Unlock()
	u := c.usageByTurn[turnID]
	if u == nil {
		return turnUsage{}
	}
	out := *u
	delete(c.usageByTurn, turnID)
	return out
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
