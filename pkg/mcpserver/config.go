// Package mcpserver exposes picoclaw's memory, persona and session archive to
// external agents over MCP. It never runs a model: callers bring their own
// tokens, picoclaw only stores and recalls.
package mcpserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Agent is one registered caller. Token is the Bearer secret it presents.
type Agent struct {
	Name            string `json:"name"`
	Token           string `json:"token"`
	ReadMainSession *bool  `json:"read_main_session,omitempty"`
	WriteMemory     *bool  `json:"write_memory,omitempty"`
	WriteMood       *bool  `json:"write_mood,omitempty"`
}

// CanReadMainSession reports whether the agent may read the owner's own
// session transcripts. Defaults to true (see the design discussion).
func (a Agent) CanReadMainSession() bool {
	return a.ReadMainSession == nil || *a.ReadMainSession
}

// CanWriteMemory reports whether the agent may append memory cards.
func (a Agent) CanWriteMemory() bool {
	return a.WriteMemory == nil || *a.WriteMemory
}

// CanWriteMood reports whether the agent may record mood events. Off by
// default: mood is the owner's own state, not a shared whiteboard.
func (a Agent) CanWriteMood() bool {
	return a.WriteMood != nil && *a.WriteMood
}

// Config is read from <workspace>/mcp/config.json.
type Config struct {
	Host   string  `json:"host"`
	Port   int     `json:"port"`
	Agents []Agent `json:"agents"`
}

// DefaultConfig listens on loopback only; no agent is registered.
func DefaultConfig() Config {
	return Config{Host: "127.0.0.1", Port: 18901}
}

// ConfigPath returns the config file location for a workspace.
func ConfigPath(workspace string) string {
	return filepath.Join(workspace, "mcp", "config.json")
}

// LoadConfig reads the config file; a missing file yields the defaults.
func LoadConfig(workspace string) (Config, error) {
	cfg := DefaultConfig()
	raw, err := os.ReadFile(ConfigPath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, fmt.Errorf("mcp: parse %s: %w", ConfigPath(workspace), err)
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 18901
	}
	seen := map[string]bool{}
	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" {
			return cfg, fmt.Errorf("mcp: agents[%d] has no name", i)
		}
		if a.Name != sanitizeName(a.Name) {
			return cfg, fmt.Errorf("mcp: agent name %q may only contain letters, digits, '.', '_' and '-'", a.Name)
		}
		if seen[a.Name] {
			return cfg, fmt.Errorf("mcp: duplicate agent name %q", a.Name)
		}
		seen[a.Name] = true
		if strings.TrimSpace(a.Token) == "" {
			return cfg, fmt.Errorf("mcp: agent %q has no token", a.Name)
		}
	}
	return cfg, nil
}

// AgentByToken resolves a Bearer token to its agent.
func (c Config) AgentByToken(token string) (Agent, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return Agent{}, false
	}
	for _, a := range c.Agents {
		if a.Token == token {
			return a, true
		}
	}
	return Agent{}, false
}
