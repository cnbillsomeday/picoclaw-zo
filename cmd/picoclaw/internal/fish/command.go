package fish

import (
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/fish"
)

// NewFishCommand 注册 `picoclaw fish` 子命令树。
func NewFishCommand() *cobra.Command {
	var workdir string

	cmd := &cobra.Command{
		Use:   "fish",
		Short: "记忆卡片 / 心情 / 主动搭话",
		Long:  "鱼的记忆三件事：记忆卡片（召回）、现算心情、带骰子的主动搭话。",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.PersistentFlags().StringVar(&workdir, "dir", "",
		"工作区目录（默认 $PICOCLAW_HOME/workspace）")

	// 解析顺序：--dir > $PICOCLAW_HOME/workspace > ~/.picoclaw/workspace
	resolveWorkspace := func() string {
		if dir := strings.TrimSpace(workdir); dir != "" {
			return filepath.Clean(dir)
		}
		return fish.WorkspacePath()
	}

	cmd.AddCommand(
		newHookCommand(resolveWorkspace),
		newMemCommand(resolveWorkspace),
		newMoodCommand(resolveWorkspace),
		newSpeakCommand(resolveWorkspace),
	)

	return cmd
}
