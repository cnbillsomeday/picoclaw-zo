package a2a

import (
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestDecodeA2ASettingsDefaults(t *testing.T) {
	bc := &config.Channel{
		Enabled: true,
		Type:    channelTypeA2A,
		Settings: config.RawNode(`{
			"base_url": "https://a2a.example.com",
			"agent_slug": "alice",
			"token": "secret-token",
			"notify_mode": "Reply"
		}`),
	}
	settings, err := decodeA2ASettings(bc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings.BaseURL != "https://a2a.example.com" || settings.AgentSlug != "alice" {
		t.Fatalf("unexpected identity: %+v", settings)
	}
	if !settings.SignEnabled() {
		t.Fatal("sign must default to true")
	}
	if !settings.RequireSignEnabled() {
		t.Fatal("require_sign must default to true")
	}
	if got := settings.GetNotifyMode(); got != "reply" {
		t.Fatalf("notify mode = %q", got)
	}
	if got := settings.GetInboundRetry(); got != time.Minute {
		t.Fatalf("retry = %s", got)
	}
	if got := settings.GetInboundMaxAttempts(); got != 3 {
		t.Fatalf("max attempts = %d", got)
	}
}

func TestDecodeA2ASettingsOverrides(t *testing.T) {
	bc := &config.Channel{
		Enabled: true,
		Type:    channelTypeA2A,
		Settings: config.RawNode(`{
			"sign": false,
			"require_sign": false,
			"inbound_retry_sec": 5,
			"inbound_max_attempts": 1,
			"notify_max_chars": 99
		}`),
	}
	settings, err := decodeA2ASettings(bc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if settings.SignEnabled() || settings.RequireSignEnabled() {
		t.Fatal("explicit false overrides were ignored")
	}
	if settings.GetInboundRetry() != 5*time.Second || settings.GetInboundMaxAttempts() != 1 {
		t.Fatalf("overrides not applied: %+v", settings)
	}
	if settings.GetNotifyMaxChars() != 99 {
		t.Fatalf("notify chars = %d", settings.GetNotifyMaxChars())
	}
}
