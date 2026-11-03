package channels

import (
	"context"
	"testing"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

type runtimeEventChannel struct {
	mockChannel
	got runtimeevents.Bus
}

func (c *runtimeEventChannel) SetRuntimeEvents(bus runtimeevents.Bus) {
	c.got = bus
}

// The A2A channel subscribes to turn events through SetRuntimeEvents. If the
// manager forgets to inject the bus, owner notifications never fire, so guard
// the wiring with a test.
func TestStartAllInjectsRuntimeEvents(t *testing.T) {
	eventBus := runtimeevents.NewBus()
	ch := &runtimeEventChannel{}

	m := newTestManager()
	m.runtimeEvents = eventBus
	m.channels["a2a"] = ch

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := m.StartAll(ctx); err != nil {
		t.Fatalf("StartAll() error = %v", err)
	}
	if ch.got == nil {
		t.Fatal("runtime event bus was not injected into the channel")
	}
	_ = m.StopAll(ctx)
}
