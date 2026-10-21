// PicoClaw - Ultra-lightweight personal AI agent

package a2a

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

// A2ASettings is the channel-local view of channel.settings.a2a. Defining it
// here (instead of in pkg/config) keeps the channel additive: the fork does not
// need to patch the upstream config schema.
type A2ASettings struct {
	BaseURL         string `json:"base_url"`
	AgentSlug       string `json:"agent_slug"`
	Token           string `json:"token"`
	KeyFile         string `json:"key_file"`
	PollIntervalSec int    `json:"poll_interval_sec"`
	HTTPTimeoutSec  int    `json:"http_timeout_sec"`
	// Sign defaults to true when omitted.
	Sign            *bool  `json:"sign"`
	MaxMessageChars int    `json:"max_message_chars"`
	PeersRefreshSec int    `json:"peers_refresh_sec"`
	StateFile       string `json:"state_file"`
	CooldownSec     int    `json:"cooldown_sec"`
	DryRun          bool   `json:"dry_run"`

	ReplyPolicy         string   `json:"reply_policy"`
	ReplyPolicyFile     string   `json:"reply_policy_file"`
	SuppressPatterns    []string `json:"suppress_patterns"`
	ReplyWindowSec      *int     `json:"reply_window_sec"`
	MaxRepliesPerWindow *int     `json:"max_replies_per_window"`

	RequireSign        *bool `json:"require_sign"`
	InboundRetrySec    int   `json:"inbound_retry_sec"`
	InboundMaxAttempts int   `json:"inbound_max_attempts"`
	InboundTimeoutSec  int   `json:"inbound_timeout_sec"`

	NotifyChannel  string `json:"notify_channel"`
	NotifyChatID   string `json:"notify_chat_id"`
	NotifyMode     string `json:"notify_mode"`
	NotifyMaxChars int    `json:"notify_max_chars"`
}

// decodeA2ASettings reads the channel's settings JSON without registering a
// decoder in pkg/config.
func decodeA2ASettings(bc *config.Channel) (*A2ASettings, error) {
	if bc == nil {
		return nil, fmt.Errorf("a2a: channel config is nil")
	}
	settings := &A2ASettings{}
	if raw := strings.TrimSpace(string(bc.Settings)); raw != "" && raw != "null" {
		if err := json.Unmarshal(bc.Settings, settings); err != nil {
			return nil, fmt.Errorf("a2a: decode settings: %w", err)
		}
	}
	// v1.4.2.1 exposed these four fields through PICOCLAW_CHANNELS_A2A_* env
	// vars. The channel decodes its settings locally, so apply them here to
	// keep existing deployments working.
	applyA2AEnvOverrides(settings)
	return settings, nil
}

// applyA2AEnvOverrides keeps the environment-variable contract of the old
// in-tree A2ASettings (env wins over the config file, as before).
func applyA2AEnvOverrides(settings *A2ASettings) {
	if settings == nil {
		return
	}
	if v := strings.TrimSpace(os.Getenv("PICOCLAW_CHANNELS_A2A_BASE_URL")); v != "" {
		settings.BaseURL = v
	}
	if v := strings.TrimSpace(os.Getenv("PICOCLAW_CHANNELS_A2A_AGENT_SLUG")); v != "" {
		settings.AgentSlug = v
	}
	if v := strings.TrimSpace(os.Getenv("PICOCLAW_CHANNELS_A2A_TOKEN")); v != "" {
		settings.Token = v
	}
	if v := strings.TrimSpace(os.Getenv("PICOCLAW_CHANNELS_A2A_KEY_FILE")); v != "" {
		settings.KeyFile = v
	}
}

func (c *A2ASettings) RequireSignEnabled() bool {
	if c == nil || c.RequireSign == nil {
		return true
	}
	return *c.RequireSign
}

func (c *A2ASettings) GetInboundRetry() time.Duration {
	if c == nil || c.InboundRetrySec <= 0 {
		return time.Minute
	}
	return time.Duration(c.InboundRetrySec) * time.Second
}

func (c *A2ASettings) GetInboundMaxAttempts() int {
	if c == nil || c.InboundMaxAttempts <= 0 {
		return 3
	}
	return c.InboundMaxAttempts
}

func (c *A2ASettings) GetInboundTimeout() time.Duration {
	if c == nil || c.InboundTimeoutSec <= 0 {
		return 5 * time.Minute
	}
	return time.Duration(c.InboundTimeoutSec) * time.Second
}

func (c *A2ASettings) GetNotifyChannel() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.NotifyChannel)
}

func (c *A2ASettings) GetNotifyChatID() string {
	if c == nil {
		return ""
	}
	return strings.TrimSpace(c.NotifyChatID)
}

func (c *A2ASettings) GetNotifyMode() string {
	if c == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(c.NotifyMode))
}

func (c *A2ASettings) GetNotifyMaxChars() int {
	if c == nil || c.NotifyMaxChars <= 0 {
		return 400
	}
	return c.NotifyMaxChars
}

func (c *A2ASettings) GetReplyWindow() time.Duration {
	if c == nil || c.ReplyWindowSec == nil || *c.ReplyWindowSec <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(*c.ReplyWindowSec) * time.Second
}

func (c *A2ASettings) GetMaxRepliesPerWindow() int {
	if c == nil || c.MaxRepliesPerWindow == nil {
		return 3
	}
	return *c.MaxRepliesPerWindow
}

func (c *A2ASettings) SignEnabled() bool {
	if c == nil || c.Sign == nil {
		return true
	}
	return *c.Sign
}
