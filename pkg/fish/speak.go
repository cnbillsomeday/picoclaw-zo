package fish

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 掷骰结果。
const (
	SpeakDecisionIdle   = "idle"
	SpeakDecisionMissed = "missed"
	SpeakDecisionDryRun = "dry_run"
	SpeakDecisionSent   = "sent"
	// SpeakDecisionReady 只用于「只看命中线」的预览，不会写账本。
	SpeakDecisionReady = "ready"
)

// SpeakConfig 是 speak/config.json 的结构。
type SpeakConfig struct {
	Enabled       bool     `json:"enabled"`
	DryRun        bool     `json:"dry_run"`
	IntervalMin   int      `json:"interval_min"`
	CooldownMin   int      `json:"cooldown_min"`
	Quiet         [2]int   `json:"quiet"`
	PeakHours     [][2]int `json:"peak_hours"`
	PeakLine      float64  `json:"peak_line"`
	OffLine       float64  `json:"off_line"`
	JustTalkedMin float64  `json:"just_talked_min"`
	ColdMin       float64  `json:"cold_min"`
	MissBonus     float64  `json:"miss_bonus"`
	MissBonusCap  float64  `json:"miss_bonus_cap"`
	// Trigger 只是账本里 would_send 记的内容，真发不发由 dry_run 决定。
	Trigger string `json:"trigger,omitempty"`
}

// DefaultSpeakConfig 返回缺省的主动搭话配置（默认试运行）。
func DefaultSpeakConfig() SpeakConfig {
	return SpeakConfig{
		Enabled:       true,
		DryRun:        true,
		IntervalMin:   30,
		CooldownMin:   20,
		Quiet:         [2]int{1, 8},
		PeakHours:     [][2]int{{9, 12}, {14, 18}},
		PeakLine:      20,
		OffLine:       60,
		JustTalkedMin: 20,
		ColdMin:       360,
		MissBonus:     10,
		MissBonusCap:  40,
	}
}

// SpeakState 是 speak/state.json 的结构，时间都是 RFC3339。
type SpeakState struct {
	Misses      int    `json:"misses"`
	LastSpoke   string `json:"last_spoke"`
	LastRoll    string `json:"last_roll"`
	LastInbound string `json:"last_inbound"`
	Ticks       int    `json:"ticks"`
}

// SpeakPlan 是一次判定的全部中间量。
type SpeakPlan struct {
	Decision  string
	Reason    string
	Peak      bool
	BaseLine  float64
	Factor    float64
	Why       string
	Bonus     float64
	Line      float64
	Roll      float64
	Hit       bool
	WouldSend string
	// Go 为 true 表示该继续跑这次 agent 回合（cron 闸门就看它）。
	Go bool
}

// speakLogEntry 是 speak.log 的一行。
type speakLogEntry struct {
	TS        string  `json:"ts"`
	Decision  string  `json:"decision"`
	Reason    string  `json:"reason"`
	Peak      bool    `json:"peak"`
	BaseLine  float64 `json:"base_line"`
	Factor    float64 `json:"factor"`
	Why       string  `json:"why"`
	Bonus     float64 `json:"bonus"`
	Line      float64 `json:"line"`
	Roll      float64 `json:"roll"`
	Hit       bool    `json:"hit"`
	WouldSend string  `json:"would_send"`
}

// LoadSpeakConfig 读配置；文件不存在时写一份缺省配置并返回。
func LoadSpeakConfig(workspace string) (SpeakConfig, error) {
	cfg := DefaultSpeakConfig()
	path := SpeakConfigPath(workspace)
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return cfg, err
		}
		if writeErr := writeSpeakConfig(path, cfg); writeErr != nil {
			return cfg, writeErr
		}
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// 配置坏了也不能让调用方崩，回落到缺省值。
		return DefaultSpeakConfig(), nil
	}
	return cfg, nil
}

func writeSpeakConfig(path string, cfg SpeakConfig) error {
	if err := ensureDir(dirOf(path)); err != nil {
		return err
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

// LoadSpeakState 读状态；没有文件就当作全零。
func LoadSpeakState(workspace string) (SpeakState, error) {
	state := SpeakState{}
	raw, err := os.ReadFile(SpeakStatePath(workspace))
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return SpeakState{}, nil
	}
	return state, nil
}

// SaveSpeakState 写状态。
func SaveSpeakState(workspace string, state SpeakState) error {
	path := SpeakStatePath(workspace)
	if err := ensureDir(dirOf(path)); err != nil {
		return err
	}
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

// TouchInbound 记下「他最近说过话」的时间。
func TouchInbound(workspace string, at time.Time) error {
	state, err := LoadSpeakState(workspace)
	if err != nil {
		return err
	}
	state.LastInbound = at.Format(time.RFC3339)
	return SaveSpeakState(workspace, state)
}

// InQuiet 判断小时是否落在安静时段（含起不含止，起===止表示不启用，支持跨午夜）。
func InQuiet(hour int, quiet [2]int) bool {
	start, end := quiet[0], quiet[1]
	if start == end {
		return false
	}
	if start < end {
		return hour >= start && hour < end
	}
	return hour >= start || hour < end
}

// IsPeak 判断是否落在工作日峰时区间。
func IsPeak(now time.Time, peakHours [][2]int) bool {
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}
	for _, window := range peakHours {
		if now.Hour() >= window[0] && now.Hour() < window[1] {
			return true
		}
	}
	return false
}

// PreviewSpeak 只算命中线和影响因素，不掷骰、不改状态、不写账本。
func PreviewSpeak(workspace string, now time.Time) SpeakPlan {
	cfg, err := LoadSpeakConfig(workspace)
	if err != nil {
		return SpeakPlan{Decision: SpeakDecisionIdle, Reason: err.Error()}
	}
	state, err := LoadSpeakState(workspace)
	if err != nil {
		return SpeakPlan{Decision: SpeakDecisionIdle, Reason: err.Error()}
	}
	if now.IsZero() {
		now = time.Now()
	}
	return previewSpeak(cfg, state, now)
}

// EvaluateSpeak 掷一次骰子：更新状态并记一行账。返回的 Go 表示该不该真的开口。
func EvaluateSpeak(workspace string) SpeakPlan {
	return evaluateSpeak(workspace, time.Now(), rand.Float64()*100)
}

func evaluateSpeak(workspace string, now time.Time, roll float64) SpeakPlan {
	cfg, err := LoadSpeakConfig(workspace)
	if err != nil {
		return SpeakPlan{Decision: SpeakDecisionIdle, Reason: err.Error()}
	}
	state, err := LoadSpeakState(workspace)
	if err != nil {
		return SpeakPlan{Decision: SpeakDecisionIdle, Reason: err.Error()}
	}

	plan := planSpeak(cfg, state, now, roll)

	state.Ticks++
	switch plan.Decision {
	case SpeakDecisionIdle:
	case SpeakDecisionMissed:
		state.Misses++
		state.LastRoll = now.Format(time.RFC3339)
	default: // dry_run / sent 都算「掷中」
		state.Misses = 0
		state.LastRoll = now.Format(time.RFC3339)
		state.LastSpoke = now.Format(time.RFC3339)
	}
	// 状态写失败不该影响判定结果，但账本仍然照记。
	_ = SaveSpeakState(workspace, state)
	_ = appendJSONLine(SpeakLogPath(workspace), speakLogEntry{
		TS:        now.Format(time.RFC3339),
		Decision:  plan.Decision,
		Reason:    plan.Reason,
		Peak:      plan.Peak,
		BaseLine:  plan.BaseLine,
		Factor:    plan.Factor,
		Why:       plan.Why,
		Bonus:     plan.Bonus,
		Line:      round2(plan.Line),
		Roll:      round2(plan.Roll),
		Hit:       plan.Hit,
		WouldSend: plan.WouldSend,
	})
	return plan
}

// previewSpeak 算出命中线，并按「关/静/冷」的顺序给出闸门结论。
func previewSpeak(cfg SpeakConfig, state SpeakState, now time.Time) SpeakPlan {
	plan := SpeakPlan{}
	plan.Peak, plan.BaseLine, plan.Factor, plan.Bonus, plan.Line, plan.Why = speakLine(cfg, state, now)

	if !cfg.Enabled {
		plan.Decision = SpeakDecisionIdle
		plan.Reason = "总开关关闭"
		return plan
	}
	if InQuiet(now.Hour(), cfg.Quiet) {
		plan.Decision = SpeakDecisionIdle
		plan.Reason = fmt.Sprintf("安静时段 %d-%d 点，不掷", cfg.Quiet[0], cfg.Quiet[1])
		return plan
	}
	if state.LastSpoke != "" {
		if last, ok := parseSpeakTime(state.LastSpoke); ok {
			gapMin := now.Sub(last).Minutes()
			if gapMin < float64(cfg.CooldownMin) {
				plan.Decision = SpeakDecisionIdle
				plan.Reason = fmt.Sprintf("冷却中（%d < %d 分钟）", int(math.Round(gapMin)), cfg.CooldownMin)
				return plan
			}
		}
	}
	plan.Decision = SpeakDecisionReady
	return plan
}

// planSpeak 在预览的基础上掷骰。
func planSpeak(cfg SpeakConfig, state SpeakState, now time.Time, roll float64) SpeakPlan {
	plan := previewSpeak(cfg, state, now)
	if plan.Decision != SpeakDecisionReady {
		return plan
	}

	plan.Roll = roll
	plan.Hit = roll < plan.Line
	switch {
	case !plan.Hit:
		plan.Decision = SpeakDecisionMissed
		plan.Reason = fmt.Sprintf("掷 %.2f ≥ 命中线 %.2f，下次 +%s",
			round2(plan.Roll), round2(plan.Line), formatNum(cfg.MissBonus))
	case cfg.DryRun:
		plan.Decision = SpeakDecisionDryRun
		plan.Reason = "掷中（试运行，未真发）"
		plan.WouldSend = cfg.Trigger
	default:
		plan.Decision = SpeakDecisionSent
		plan.Reason = "掷中，该开口了"
		plan.Go = true
	}
	return plan
}

// speakLine 算基础线、时机因子和最终命中线。
func speakLine(cfg SpeakConfig, state SpeakState, now time.Time) (peak bool, base, factor, bonus, line float64, why string) {
	peak = IsPeak(now, cfg.PeakHours)
	base = cfg.OffLine
	if peak {
		base = cfg.PeakLine
	}

	factor = 1.0
	why = "还没有他的消息"
	if state.LastInbound != "" {
		if last, ok := parseSpeakTime(state.LastInbound); ok {
			gapMin := now.Sub(last).Minutes()
			if gapMin < 0 {
				gapMin = 0
			}
			switch {
			case gapMin < cfg.JustTalkedMin:
				factor = 0.15
				why = fmt.Sprintf("刚聊过（%d 分钟前）", int(math.Round(gapMin)))
			case gapMin > cfg.ColdMin:
				factor = 1.5
				why = fmt.Sprintf("冷太久（%.1f 小时）", gapMin/60)
			default:
				why = fmt.Sprintf("距上次 %d 分钟", int(math.Round(gapMin)))
			}
		}
	}

	bonus = math.Min(cfg.MissBonusCap, float64(state.Misses)*cfg.MissBonus)
	line = math.Min(100, base*factor+bonus)
	return peak, base, factor, bonus, line, why
}

func parseSpeakTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	if ts, err := time.Parse(time.RFC3339, raw); err == nil {
		return ts, true
	}
	if ts, err := time.ParseInLocation("2006-01-02T15:04:05", raw, time.Local); err == nil {
		return ts, true
	}
	return time.Time{}, false
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func dirOf(path string) string {
	return filepath.Dir(path)
}
