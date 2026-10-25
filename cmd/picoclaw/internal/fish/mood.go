package fish

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/fish"
)

func newMoodCommand(workspace func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mood",
		Short: "心情（现算，会自然回落）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newMoodAddCommand(workspace),
		newMoodShowCommand(workspace),
		newMoodLogCommand(workspace),
		newMoodResetCommand(workspace),
	)
	return cmd
}

func newMoodAddCommand(workspace func() string) *cobra.Command {
	var (
		d   float64
		emo string
		why string
		sid string
	)

	cmd := &cobra.Command{
		Use:   "add",
		Short: "记一笔心情",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			emotions := map[string]float64{}
			if strings.TrimSpace(emo) != "" {
				parsed, err := fish.ParseEmoSpec(emo)
				if err != nil {
					return err
				}
				emotions = parsed
			}
			state, err := fish.AddMoodEvent(workspace(), d, emotions, why, sid, time.Now())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "已记：%s %s → 现在心情 %d\n", signed(d), why, int(math.Round(state.Value)))
			return nil
		},
	}

	cmd.Flags().Float64Var(&d, "d", 0, "心情涨跌，比如 3 或 -2")
	cmd.Flags().StringVar(&emo, "emo", "", "情绪，比如 开心=4,甜蜜=2")
	cmd.Flags().StringVar(&why, "why", "", "为什么")
	cmd.Flags().StringVar(&sid, "sid", "", "会话 id")
	return cmd
}

func newMoodShowCommand(workspace func() string) *cobra.Command {
	var (
		line   bool
		asJSON bool
	)

	cmd := &cobra.Command{
		Use:   "show",
		Short: "看现在心情",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			state, err := fish.LoadMood(workspace())
			if err != nil {
				return err
			}
			cfg := fish.LoadMoodConfig(workspace())

			switch {
			case asJSON:
				payload := map[string]any{
					"mood":     math.Round(state.Value*10) / 10,
					"baseline": cfg.Baseline,
					"emo":      state.Emo,
					"why":      state.Why,
					"age_h":    state.AgeHours,
				}
				body, err := json.Marshal(payload)
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(body))
			case line:
				fmt.Fprintln(out, state.Line())
			default:
				fmt.Fprintf(out, "心情 %d/100（基线 %s）\n", int(math.Round(state.Value)), formatFloat(cfg.Baseline))
				if len(state.Emo) == 0 {
					fmt.Fprintln(out, "情绪：平静")
				} else {
					chunks := make([]string, 0, len(state.Emo))
					for name, v := range state.Emo {
						chunks = append(chunks, fmt.Sprintf("%s %s", name, formatFloat(v)))
					}
					fmt.Fprintln(out, "情绪："+strings.Join(chunks, "  "))
				}
				if state.HasWhy {
					fmt.Fprintf(out, "最近一次波动：%s（%s）\n", state.Why, fish.FormatAge(state.AgeHours))
				}
			}
			return nil
		},
	}

	cmd.Flags().BoolVar(&line, "line", false, "一行渲染（注入用）")
	cmd.Flags().BoolVar(&asJSON, "json", false, "机器读")
	return cmd
}

func newMoodLogCommand(workspace func() string) *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "log",
		Short: "最近的心情事件",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			events, err := fish.LoadMoodEvents(workspace())
			if err != nil {
				return err
			}
			if len(events) == 0 {
				fmt.Fprintln(out, "还没有心情事件。")
				return nil
			}
			start := 0
			if limit > 0 && len(events) > limit {
				start = len(events) - limit
			}
			for _, e := range events[start:] {
				fmt.Fprintln(out, fish.FormatMoodLog(e))
			}
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 10, "最近几条")
	return cmd
}

func newMoodResetCommand(workspace func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "reset",
		Short: "清空心情事件（先备份），回到基线",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			backup, err := fish.ResetMood(workspace(), time.Now())
			if err != nil {
				return err
			}
			if backup == "" {
				fmt.Fprintln(out, "本来就没有事件，还是基线。")
				return nil
			}
			cfg := fish.LoadMoodConfig(workspace())
			fmt.Fprintf(out, "已清空（备份 %s），回到基线 %s\n", backup, formatFloat(cfg.Baseline))
			return nil
		},
	}
}

func signed(v float64) string {
	if v > 0 {
		return "+" + formatFloat(v)
	}
	return formatFloat(v)
}

func formatFloat(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%d", int64(v))
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}
