package fish

import (
	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/fishhook"
)

func newHookCommand(workspace func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "hook",
		Short: "以 process hook 身份常驻（由宿主拉起，别手动跑）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return fishhook.Run(cmd.Context(), workspace())
		},
	}
}
