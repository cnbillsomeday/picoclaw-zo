// PicoClaw - Ultra-lightweight personal AI agent

package a2a

import (
	"fmt"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

// channelTypeA2A is the config channel type this package registers. It is
// declared locally so the fork does not need to patch pkg/config.
const channelTypeA2A = "a2a"

func init() {
	config.RegisterChannelSettings(channelTypeA2A, A2ASettings{})
	channels.RegisterFactory(
		channelTypeA2A,
		func(channelName, channelType string, cfg *config.Config, b *bus.MessageBus) (channels.Channel, error) {
			if cfg == nil {
				return nil, fmt.Errorf("a2a: config is nil")
			}
			bc := cfg.Channels[channelName]
			if bc == nil {
				return nil, fmt.Errorf("a2a: channel %q is not configured", channelName)
			}
			settings, err := decodeA2ASettings(bc)
			if err != nil {
				return nil, err
			}
			ch, err := NewA2AChannel(bc, settings, b)
			if err != nil {
				return nil, err
			}
			if channelName != channelTypeA2A {
				ch.SetName(channelName)
			}
			return ch, nil
		},
	)
}
