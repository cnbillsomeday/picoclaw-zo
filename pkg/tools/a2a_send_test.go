package tools

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func a2aTestConfig(settings string, allow []string) *config.Config {
	return &config.Config{Channels: config.ChannelsConfig{
		"a2a": &config.Channel{
			Enabled:   true,
			Type:      a2aChannelType,
			AllowFrom: allow,
			Settings:  config.RawNode(settings),
		},
	}}
}

func TestHasEnabledA2AChannel(t *testing.T) {
	if HasEnabledA2AChannel(nil) {
		t.Fatal("nil config must not report an enabled A2A channel")
	}
	cfg := a2aTestConfig(`{"base_url":"https://a2a.example.com","agent_slug":"alice","token":"t"}`, []string{"bob"})
	if !HasEnabledA2AChannel(cfg) {
		t.Fatal("expected enabled A2A channel")
	}
	disabled := a2aTestConfig(`{}`, nil)
	disabled.Channels["a2a"].Enabled = false
	if HasEnabledA2AChannel(disabled) {
		t.Fatal("disabled channel must not count")
	}
}

func TestFindEnabledA2AChannelDecodesSettings(t *testing.T) {
	cfg := a2aTestConfig(`{
		"base_url":"https://a2a.example.com",
		"agent_slug":"alice",
		"token":"tok",
		"key_file":"~/.a2a/ed25519.seed",
		"dry_run":true
	}`, []string{"bob", "carol"})
	name, bc, settings, err := findEnabledA2AChannel(cfg)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if name != "a2a" || bc == nil {
		t.Fatalf("name=%q bc=%v", name, bc)
	}
	if settings.AgentSlug != "alice" || settings.Token != "tok" || !settings.DryRun {
		t.Fatalf("settings = %+v", settings)
	}
	if !a2aAllowed(bc, "Bob") {
		t.Fatal("allow_from match must be case-insensitive")
	}
	if a2aAllowed(bc, "dave") {
		t.Fatal("dave must not be allowed")
	}
}

func TestFindEnabledA2AChannelEnvOverrides(t *testing.T) {
	t.Setenv("PICOCLAW_CHANNELS_A2A_BASE_URL", "http://127.0.0.1:21220")
	t.Setenv("PICOCLAW_CHANNELS_A2A_TOKEN", "env-token")
	cfg := a2aTestConfig(`{"agent_slug":"alice"}`, []string{"bob"})
	_, _, settings, err := findEnabledA2AChannel(cfg)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if settings.BaseURL != "http://127.0.0.1:21220" || settings.Token != "env-token" {
		t.Fatalf("env overrides not applied: %+v", settings)
	}
}
