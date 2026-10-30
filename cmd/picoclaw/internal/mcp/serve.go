package mcp

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/fish"
	"github.com/sipeed/picoclaw/pkg/mcpserver"
)

// newServeCommand exposes the workspace over MCP for external agents. It runs
// no model and never joins the gateway process.
func newServeCommand() *cobra.Command {
	var dir, host string
	var port int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve memory/persona/sessions over MCP (no model calls)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			workspace := strings.TrimSpace(dir)
			if workspace == "" {
				workspace = fish.WorkspacePath()
			}
			cfg, err := mcpserver.LoadConfig(workspace)
			if err != nil {
				return err
			}
			if host != "" {
				cfg.Host = host
			}
			if port != 0 {
				cfg.Port = port
			}
			srv := mcpserver.New(workspace, cfg)
			fmt.Fprintf(cmd.ErrOrStderr(), "picoclaw mcp serve on http://%s/mcp (%d agents)\n", srv.Addr(), len(cfg.Agents))

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return srv.ListenAndServe(ctx)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "workspace directory (default $PICOCLAW_HOME/workspace)")
	cmd.Flags().StringVar(&host, "host", "", "listen host (overrides config)")
	cmd.Flags().IntVar(&port, "port", 0, "listen port (overrides config)")
	return cmd
}
