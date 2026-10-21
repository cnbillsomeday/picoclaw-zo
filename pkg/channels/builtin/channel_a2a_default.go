//go:build !custom_channels

package builtin

// Registers the A2A client channel in default builds. The package lives
// outside this fork's patch surface: everything it needs is additive.
import _ "github.com/sipeed/picoclaw/pkg/channels/a2a"
