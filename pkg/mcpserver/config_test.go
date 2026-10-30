package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Host != "127.0.0.1" || cfg.Port != 18901 || len(cfg.Agents) != 0 {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfigValidation(t *testing.T) {
	for name, body := range map[string]string{
		"missing token":  `{"agents":[{"name":"claude"}]}`,
		"bad name":       `{"agents":[{"name":"a/b","token":"x"}]}`,
		"duplicate name": `{"agents":[{"name":"a","token":"x"},{"name":"a","token":"y"}]}`,
	} {
		ws := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(ConfigPath(ws)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ConfigPath(ws), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(ws); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestAgentPermissionsAndLookup(t *testing.T) {
	cfg := Config{Agents: []Agent{{Name: "claude", Token: "secret"}}}
	a, ok := cfg.AgentByToken(" secret ")
	if !ok || a.Name != "claude" {
		t.Fatal("token lookup failed")
	}
	if _, ok := cfg.AgentByToken("wrong"); ok {
		t.Fatal("wrong token must not resolve")
	}
	if !a.CanReadMainSession() || !a.CanWriteMemory() {
		t.Fatal("read main session and write memory default to true")
	}
	if a.CanWriteMood() {
		t.Fatal("write mood defaults to false")
	}
}
