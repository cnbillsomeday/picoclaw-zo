package fish

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/pkg/fish"
)

func newSpeakCommand(workspace func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "speak",
		Short: "主动搭话（带骰子的定时任务）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newSpeakWhyCommand(workspace),
		newSpeakRollCommand(workspace),
		newSpeakStateCommand(workspace),
	)
	return cmd
}

func newSpeakWhyCommand(workspace func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "why",
		Short: "只看当前命中线和影响因素，不掷",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ws := workspace()
			now := time.Now()
			plan := fish.PreviewSpeak(ws, now)
			cfg, err := fish.LoadSpeakConfig(ws)
			if err != nil {
				return err
			}
			state, err := fish.LoadSpeakState(ws)
			if err != nil {
				return err
			}

			phase := "谷时"
			if plan.Peak {
				phase = "峰时"
			}
			fmt.Fprintf(out, "现在 %s 周%d，%s（开关 %s，%s）\n",
				now.Format("2006-01-02 15:04"), int(now.Weekday()), phase,
				onOff(cfg.Enabled), dryRunLabel(cfg))

			fmt.Fprintf(out, "① 基础线 %s（峰时 %s / 谷时 %s）\n",
				formatFloat(plan.BaseLine), formatFloat(cfg.PeakLine), formatFloat(cfg.OffLine))
			fmt.Fprintf(out, "② 时机因子 %s（%s）\n", formatFloat(plan.Factor), plan.Why)
			fmt.Fprintf(out, "③ 连挂 %d 次，每次 +%s\n", state.Misses, formatFloat(cfg.MissBonus))
			fmt.Fprintf(out, "④ 没中加成 +%s（上限 +%s）\n", formatFloat(plan.Bonus), formatFloat(cfg.MissBonusCap))
			fmt.Fprintf(out, "⑤ 命中线 %s（= %s × %s + %s，封顶 100）\n",
				formatFloat(plan.Line), formatFloat(plan.BaseLine), formatFloat(plan.Factor), formatFloat(plan.Bonus))
			fmt.Fprintf(out, "⑥ 安静时段 %d-%d 点，冷却 %d 分钟\n", cfg.Quiet[0], cfg.Quiet[1], cfg.CooldownMin)

			if plan.Decision == fish.SpeakDecisionIdle {
				fmt.Fprintf(out, "现在不掷：%s\n", plan.Reason)
			} else {
				fmt.Fprintln(out, "现在可以掷。")
			}
			return nil
		},
	}
}

func newSpeakRollCommand(workspace func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "roll",
		Short: "手动掷一次（遵守 dry_run）",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			plan := fish.EvaluateSpeak(workspace())

			fmt.Fprintf(out, "掷 %s / 命中线 %s（基础线 %s × 因子 %s + 加成 %s）\n",
				formatFloat(plan.Roll), formatFloat(plan.Line),
				formatFloat(plan.BaseLine), formatFloat(plan.Factor), formatFloat(plan.Bonus))

			switch plan.Decision {
			case fish.SpeakDecisionIdle:
				fmt.Fprintf(out, "没掷：%s\n", plan.Reason)
			case fish.SpeakDecisionMissed:
				fmt.Fprintf(out, "没中：%s\n", plan.Reason)
			case fish.SpeakDecisionDryRun:
				fmt.Fprintf(out, "掷中，但还在试运行（dry_run），没真发。\n")
				if strings.TrimSpace(plan.WouldSend) != "" {
					fmt.Fprintf(out, "本来要说：%s\n", plan.WouldSend)
				}
			case fish.SpeakDecisionSent:
				fmt.Fprintf(out, "掷中：%s\n", plan.Reason)
			default:
				fmt.Fprintf(out, "%s：%s\n", plan.Decision, plan.Reason)
			}
			return nil
		},
	}
}

func newSpeakStateCommand(workspace func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "state",
		Short: "看 state.json",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			ws := workspace()
			state, err := fish.LoadSpeakState(ws)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "没中次数 %d\n", state.Misses)
			fmt.Fprintf(out, "判定次数 %d\n", state.Ticks)
			fmt.Fprintf(out, "上次开口 %s\n", orDash(state.LastSpoke))
			fmt.Fprintf(out, "上次掷骰 %s\n", orDash(state.LastRoll))
			fmt.Fprintf(out, "上次他说话 %s\n", orDash(state.LastInbound))
			return nil
		},
	}
}

func onOff(v bool) string {
	if v {
		return "开"
	}
	return "关"
}

func dryRunLabel(cfg fish.SpeakConfig) string {
	if cfg.DryRun {
		return "试运行"
	}
	return "真发"
}

func orDash(v string) string {
	if strings.TrimSpace(v) == "" {
		return "-"
	}
	return v
}
