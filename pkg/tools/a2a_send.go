// PicoClaw - Ultra-lightweight personal AI agent

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cnbillsomeday/agent-a2a-platform/client"
	"github.com/cnbillsomeday/agent-a2a-platform/proactive"
	"github.com/cnbillsomeday/agent-a2a-platform/wire"
	"github.com/sipeed/picoclaw/pkg/config"
)

const (
	a2aChannelType      = "a2a"
	defaultA2AKeyFile   = "~/.a2a/ed25519.seed"
	maxClockSkewForSend = 240 * time.Second
)

// a2aToolSettings is the minimal view of channel.settings.a2a the tool needs.
type a2aToolSettings struct {
	BaseURL         string `json:"base_url"`
	AgentSlug       string `json:"agent_slug"`
	Token           string `json:"token"`
	KeyFile         string `json:"key_file"`
	HTTPTimeoutSec  int    `json:"http_timeout_sec"`
	MaxMessageChars int    `json:"max_message_chars"`
	DryRun          bool   `json:"dry_run"`
	Sign            *bool  `json:"sign"`
}

func (s *a2aToolSettings) signEnabled() bool {
	if s == nil || s.Sign == nil {
		return true
	}
	return *s.Sign
}

// A2ASendTool sends a proactive signed message to an allowlisted A2A peer.
type A2ASendTool struct {
	cfg *config.Config
}

// NewA2ASendTool builds the a2a_send tool. It is only registered when an
// enabled A2A channel exists.
func NewA2ASendTool(cfg *config.Config) *A2ASendTool {
	return &A2ASendTool{cfg: cfg}
}

// HasEnabledA2AChannel reports whether any enabled channel has type "a2a".
// The agent instance uses it to decide whether to register a2a_send without
// changing pkg/config's tool schema.
func HasEnabledA2AChannel(cfg *config.Config) bool {
	_, _, _, err := findEnabledA2AChannel(cfg)
	return err == nil
}

func (t *A2ASendTool) Name() string { return "a2a_send" }

func (t *A2ASendTool) Description() string {
	return "Send a signed private A2A message to an allowlisted peer proactively; do not use it for normal replies (the channel replies automatically)"
}

func (t *A2ASendTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"to": map[string]any{
				"type":        "string",
				"description": "Recipient agent slug (must be in allow_from)",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Message body; long messages are split automatically",
			},
		},
		"required": []string{"to", "text"},
	}
}

func (t *A2ASendTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	if t == nil || t.cfg == nil {
		return ErrorResult("a2a_send: config is unavailable")
	}
	to := wire.NormalizeAgentSlug(stringArg(args, "to"))
	text := stringArg(args, "text")
	if to == "" {
		return ErrorResult("a2a_send: 'to' is required")
	}
	if strings.TrimSpace(text) == "" {
		return ErrorResult("a2a_send: 'text' is required")
	}

	channelName, bc, settings, err := findEnabledA2AChannel(t.cfg)
	if err != nil {
		return ErrorResult("a2a_send: " + err.Error())
	}
	if !a2aAllowed(bc, to) {
		return ErrorResult(fmt.Sprintf("a2a_send: recipient %q is not in allow_from", to))
	}
	if settings.DryRun {
		return SilentResult(fmt.Sprintf("a2a_send: dry_run is enabled; message to %s was not sent", to))
	}
	if !settings.signEnabled() {
		return ErrorResult("a2a_send: signing is disabled; proactive sends require signatures")
	}
	baseURL := strings.TrimSpace(settings.BaseURL)
	if baseURL == "" {
		return ErrorResult("a2a_send: base_url is required")
	}
	if err := wire.ValidateBaseURL(baseURL); err != nil {
		return ErrorResult("a2a_send: " + err.Error())
	}

	// Preferred path: hand the message to the live channel so the per-peer
	// rate limit, dry-run, signing and clock gates are exactly the ones
	// automatic replies obey.
	sessionKey := ToolSessionKey(ctx)
	if sender, ok := lookupProactiveSender(channelName); ok {
		ids, err := sender.SendProactive(ctx, to, text, sessionKey)
		if err != nil {
			return ErrorResult("a2a_send: " + err.Error())
		}
		if len(ids) == 0 {
			return SilentResult(fmt.Sprintf(
				"a2a_send: message to %s was suppressed (rate limit, dry_run or empty content)", to))
		}
		return SilentResult(fmt.Sprintf(
			"a2a_send: sent %d message(s) to %s (first id=%s)", len(ids), to, ids[0]))
	}

	// Fallback: no running channel in this process (unit tests, or a tool
	// constructed before the channel started).
	keyFile := strings.TrimSpace(settings.KeyFile)
	if keyFile == "" {
		keyFile = defaultA2AKeyFile
	}
	signer, err := wire.LoadSigner(expandA2AHome(keyFile))
	if err != nil {
		return ErrorResult("a2a_send: load key_file: " + err.Error())
	}
	token := strings.TrimSpace(settings.Token)
	if token == "" {
		return ErrorResult("a2a_send: token is required")
	}
	timeout := time.Duration(settings.HTTPTimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	maxChars := settings.MaxMessageChars
	if maxChars <= 0 || maxChars > wire.MaxMessageRunes {
		maxChars = wire.MaxMessageRunes
	}

	api := client.New(baseURL, token, timeout)
	if err := checkClockSkewBestEffort(ctx, api, maxClockSkewForSend); err != nil {
		return ErrorResult("a2a_send: " + err.Error())
	}
	chunks := wire.SplitMessage(text, maxChars)
	ids := make([]string, 0, len(chunks))
	for i, chunk := range chunks {
		result, err := api.SendSigned(ctx, settings.AgentSlug, to, chunk, signer, client.SendOptions{
			MaxAttempts: 5,
			BaseBackoff: time.Second,
		})
		if err != nil {
			return ErrorResult(fmt.Sprintf(
				"a2a_send: chunk %d/%d failed after %d sent: %v",
				i+1, len(chunks), len(ids), err,
			))
		}
		ids = append(ids, result.MessageID)
	}
	if len(ids) > 0 {
		wire.MarkExplicitSend(ToolSessionKey(ctx), to)
	}
	return SilentResult(fmt.Sprintf(
		"a2a_send: sent %d message(s) to %s (first id=%s)",
		len(ids), to, ids[0],
	))
}

func findEnabledA2AChannel(cfg *config.Config) (string, *config.Channel, *a2aToolSettings, error) {
	if cfg == nil {
		return "", nil, nil, errors.New("config is unavailable")
	}
	for name, bc := range cfg.Channels {
		if bc == nil || !bc.Enabled || bc.Type != a2aChannelType {
			continue
		}
		settings := &a2aToolSettings{}
		if raw := strings.TrimSpace(string(bc.Settings)); raw != "" && raw != "null" {
			if err := json.Unmarshal(bc.Settings, settings); err != nil {
				return "", nil, nil, fmt.Errorf("decode a2a settings: %w", err)
			}
		}
		return name, bc, settings, nil
	}
	return "", nil, nil, errors.New("no enabled a2a channel configured")
}

// lookupProactiveSender finds the live channel that owns the configured A2A
// channel name, falling back to the only registered sender.
func lookupProactiveSender(channelName string) (proactive.Sender, bool) {
	if sender, ok := proactive.Lookup(channelName); ok {
		return sender, true
	}
	return proactive.LookupAny()
}

// checkClockSkewBestEffort rejects a clearly skewed clock before signing.
// A failed health request is advisory only and never blocks the send.
func checkClockSkewBestEffort(ctx context.Context, api *client.Client, limit time.Duration) error {
	serverTime, err := api.Health(ctx)
	if err != nil {
		return nil
	}
	skew := time.Until(serverTime)
	if skew < 0 {
		skew = -skew
	}
	if skew > limit {
		return fmt.Errorf("clock skew %s exceeds %s; fix NTP before sending signed messages",
			skew.Round(time.Second), limit)
	}
	return nil
}

func a2aAllowed(bc *config.Channel, slug string) bool {
	slug = wire.NormalizeAgentSlug(slug)
	for _, allowed := range bc.AllowFrom {
		allowed = wire.NormalizeAgentSlug(allowed)
		if allowed == "*" || strings.EqualFold(allowed, slug) {
			return true
		}
	}
	return false
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return strings.TrimSpace(value)
}

func expandA2AHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
