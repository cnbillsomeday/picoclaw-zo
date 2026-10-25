package fish

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/fish"
)

func newMemCommand(workspace func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mem",
		Short: "记忆卡片",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newMemImportCommand(workspace),
		newMemAddCommand(workspace),
		newMemRecallCommand(workspace),
		newMemListCommand(workspace),
	)
	return cmd
}

func newMemImportCommand(workspace func() string) *cobra.Command {
	var file string

	cmd := &cobra.Command{
		Use:   "import",
		Short: "从 MEMORY.md 切卡片（可重复跑，幂等）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ws := workspace()
			path := strings.TrimSpace(file)
			if path == "" {
				path = filepath.Join(ws, "MEMORY.md")
			}
			added, skipped, err := fish.ImportCards(ws, path)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "已导入 %d 张卡片，跳过 %d 张（已存在或无内容）\n", added, skipped)
			return nil
		},
	}

	cmd.Flags().StringVar(&file, "file", "", "MEMORY.md 路径（默认 <workspace>/MEMORY.md）")
	return cmd
}

func newMemAddCommand(workspace func() string) *cobra.Command {
	var (
		text string
		kind string
		key  string
		imp  float64
		tags string
		t    string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "加一张卡片",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("--text 不能为空")
			}
			out := cmd.OutOrStdout()
			card, replaced, err := fish.AddCard(workspace(), fish.NewCard{
				Text: text,
				T:    strings.TrimSpace(t),
				Tags: splitTags(tags),
				Imp:  imp,
				Kind: strings.TrimSpace(kind),
				Key:  strings.TrimSpace(key),
				Src:  "manual",
			})
			if err != nil {
				return err
			}
			if replaced > 0 {
				fmt.Fprintf(out, "已加卡 %s（顶掉同键旧卡 %d 张）\n", card.ID, replaced)
			} else {
				fmt.Fprintf(out, "已加卡 %s\n", card.ID)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&text, "text", "", "卡片正文")
	cmd.Flags().StringVar(&kind, "kind", fish.CardKindEvent, "event / fact / pref")
	cmd.Flags().StringVar(&key, "key", "", "事实键，写成「对象.方面」（同键只留最新）")
	cmd.Flags().Float64Var(&imp, "imp", fish.DefaultCardImp, "重要度 0~1")
	cmd.Flags().StringVar(&tags, "tags", "", "标签，逗号分隔")
	cmd.Flags().StringVar(&t, "t", time.Now().Format("2006-01-02"), "日期 YYYY-MM-DD")
	_ = cmd.MarkFlagRequired("text")
	return cmd
}

func newMemRecallCommand(workspace func() string) *cobra.Command {
	var (
		query string
		limit int
		noLog bool
	)

	cmd := &cobra.Command{
		Use:   "recall",
		Short: "按关键词召回记忆",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if strings.TrimSpace(query) == "" {
				return fmt.Errorf("--query 不能为空")
			}
			out := cmd.OutOrStdout()
			result, err := fish.RecallWorkspace(workspace(), query, "cli", limit, !noLog)
			if err != nil {
				return err
			}
			if result.Block == "" {
				fmt.Fprintln(out, "没有召回到相关记忆。")
				return nil
			}
			fmt.Fprintln(out, result.Block)
			return nil
		},
	}

	cmd.Flags().StringVar(&query, "query", "", "查询文本")
	cmd.Flags().IntVarP(&limit, "limit", "n", fish.DefaultRecallLimit, "最多几条")
	cmd.Flags().BoolVar(&noLog, "no-log", false, "不写 recall.log")
	_ = cmd.MarkFlagRequired("query")
	return cmd
}

func newMemListCommand(workspace func() string) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "list",
		Short: "列卡片（新的在前）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			cards, err := fish.ListCards(workspace(), limit)
			if err != nil {
				return err
			}
			if len(cards) == 0 {
				fmt.Fprintln(out, "还没有卡片。")
				return nil
			}
			for _, c := range cards {
				stamp := c.T
				if strings.TrimSpace(stamp) == "" {
					stamp = "无日期"
				}
				fmt.Fprintf(out, "%s  %-10s %-5s %s\n", c.ID, stamp, c.Kind, summary(c.Text, 60))
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "最多几条")
	return cmd
}

func splitTags(raw string) []string {
	out := make([]string, 0, 4)
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
}

func summary(text string, limit int) string {
	flat := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(flat) <= limit {
		return flat
	}
	return string([]rune(flat)[:limit]) + "…"
}
