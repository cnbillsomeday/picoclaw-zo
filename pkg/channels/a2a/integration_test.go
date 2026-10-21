//go:build integration

package a2a

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cnbillsomeday/agent-a2a-platform/client"
	"github.com/cnbillsomeday/agent-a2a-platform/server"
	"github.com/cnbillsomeday/agent-a2a-platform/wire"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

// TestIntegrationChannelServerTurnEnd runs the real HTTP path end to end:
// standalone server -> signed inbound -> channel poller -> bus -> synthetic
// agent.turn.end -> unread cleared in the server store.
func TestIntegrationChannelServerTurnEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	dataDir := t.TempDir()
	initRes, err := server.InitData(server.InitOptions{
		DataDir: dataDir,
		Domain:  "localhost",
		Agents:  []string{"alice", "bob"},
	})
	if err != nil {
		t.Fatalf("init platform data: %v", err)
	}
	inst, err := server.New(server.Options{
		Listen:    "127.0.0.1:0",
		DataDir:   dataDir,
		Domain:    "localhost",
		Retention: time.Hour,
		SignMode:  "strict",
		Logger:    log.New(io.Discard, "", 0),
	})
	if err != nil {
		t.Fatalf("start platform server: %v", err)
	}
	if err := inst.Start(); err != nil {
		t.Fatalf("bind platform server: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = inst.Stop(stopCtx)
	})
	baseURL := "http://" + inst.Addr()

	aliceSeed, aliceSigner := writeSeed(t, dataDir, "alice")
	bobSeed, bobSigner := writeSeed(t, dataDir, "bob")
	_ = aliceSeed
	_ = bobSeed
	registerSelfKey(t, ctx, baseURL, initRes.Tokens["alice"], aliceSigner)
	registerSelfKey(t, ctx, baseURL, initRes.Tokens["bob"], bobSigner)

	statePath := filepath.Join(dataDir, "alice-state.json")
	settingsJSON := fmt.Sprintf(`{
		"base_url": %q,
		"agent_slug": "alice",
		"token": %q,
		"key_file": %q,
		"poll_interval_sec": 5,
		"state_file": %q,
		"sign": true,
		"require_sign": true,
		"notify_channel": "telegram",
		"notify_chat_id": "owner-1",
		"notify_mode": "all"
	}`, baseURL, initRes.Tokens["alice"], filepath.Join(dataDir, "alice.seed"), statePath)

	bc := &config.Channel{
		Enabled:   true,
		Type:      channelTypeA2A,
		AllowFrom: []string{"bob"},
		Settings:  config.RawNode(settingsJSON),
	}
	settings, err := decodeA2ASettings(bc)
	if err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	eventBus := runtimeevents.NewBus()
	t.Cleanup(func() { _ = eventBus.Close() })

	ch, err := NewA2AChannel(bc, settings, msgBus)
	if err != nil {
		t.Fatalf("new channel: %v", err)
	}
	ch.SetRuntimeEvents(eventBus)
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("start channel: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		_ = ch.Stop(stopCtx)
	})

	// bob sends a signed message to alice; the channel should poll and deliver.
	bobClient := client.New(baseURL, initRes.Tokens["bob"], 10*time.Second)
	content := "hello from bob " + time.Now().Format("15:04:05.000")
	if _, err := bobClient.SendSigned(ctx, "bob", "alice", content, bobSigner, client.SendOptions{
		MaxAttempts: 3,
		BaseBackoff: 200 * time.Millisecond,
	}); err != nil {
		t.Fatalf("bob send: %v", err)
	}

	var inbound bus.InboundMessage
	select {
	case inbound = <-msgBus.InboundChan():
	case <-time.After(40 * time.Second):
		t.Fatal("timed out waiting for the channel to deliver bob's message")
	}
	if inbound.Channel != "a2a" {
		t.Fatalf("inbound channel = %q", inbound.Channel)
	}
	if inbound.SenderID != "bob" {
		t.Fatalf("inbound sender = %q", inbound.SenderID)
	}
	if !strings.Contains(inbound.Content, content) {
		t.Fatalf("inbound content %q does not contain %q", inbound.Content, content)
	}
	t.Logf("delivered inbound message=%s conv=%s", inbound.MessageID, inbound.Context.Raw["conversation_id"])

	// Simulate per-iteration token usage, then the end of the turn.
	if res := eventBus.Publish(ctx, runtimeevents.Event{
		Kind: runtimeevents.KindAgentLLMResponse,
		Scope: runtimeevents.Scope{
			Channel: "a2a",
			ChatID:  "bob",
			TurnID:  "turn-integration",
		},
		Payload: testLLMPayload{TokensIn: 12, TokensOut: 34},
	}); res.Closed {
		t.Fatal("event bus is closed")
	}
	if res := eventBus.Publish(ctx, runtimeevents.Event{
		Kind: runtimeevents.KindAgentTurnEnd,
		Scope: runtimeevents.Scope{
			Channel: "a2a",
			ChatID:  "bob",
			TurnID:  "turn-integration",
		},
		Payload: testTurnEndPayload{Status: "completed", FinalContent: "ok", Iterations: 1},
	}); res.Closed {
		t.Fatal("event bus is closed")
	}

	// The channel settles the pending inbound and marks the message read.
	aliceClient := client.New(baseURL, initRes.Tokens["alice"], 10*time.Second)
	deadline := time.Now().Add(30 * time.Second)
	for {
		home, _, _, err := aliceClient.Home(ctx, "")
		if err != nil {
			t.Fatalf("alice home: %v", err)
		}
		if home.UnreadTotal == 0 && !ch.state.HasPendingInbound() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("unread not cleared: total=%d pending=%v", home.UnreadTotal, ch.state.HasPendingInbound())
		}
		time.Sleep(250 * time.Millisecond)
	}

	if entry := ch.state.InboundEntry(inbound.Context.Raw["conversation_id"], inbound.MessageID); entry != nil {
		t.Logf("settled inbound status=%s attempts=%d", entry.Status, entry.Attempts)
	}

	// notify_mode=all must publish one owner wake summary containing the
	// aggregated token usage.
	select {
	case notice := <-msgBus.OutboundChan():
		if notice.Channel != "telegram" || notice.ChatID != "owner-1" {
			t.Fatalf("wake summary routed to %s/%s", notice.Channel, notice.ChatID)
		}
		if !strings.Contains(notice.Content, "12") || !strings.Contains(notice.Content, "34") {
			t.Fatalf("wake summary lacks token usage: %q", notice.Content)
		}
		t.Logf("wake summary: %s", strings.ReplaceAll(notice.Content, "\n", " | "))
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for the owner wake summary")
	}
}

func writeSeed(t *testing.T, dir, name string) ([]byte, *wire.Signer) {
	t.Helper()
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := filepath.Join(dir, name+".seed")
	if err := os.WriteFile(path, seed, 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	signer, err := wire.NewSigner(seed)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	return seed, signer
}

func registerSelfKey(t *testing.T, ctx context.Context, baseURL, token string, signer *wire.Signer) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"public_key_b64url": base64.RawURLEncoding.EncodeToString(signer.PublicKey()),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/a2a/keys/register", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("register request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("register key: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register key: status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
}
