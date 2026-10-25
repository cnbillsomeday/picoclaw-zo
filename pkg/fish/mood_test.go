package fish

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func TestComputeMoodDecaysAndClamps(t *testing.T) {
	cfg := DefaultMoodConfig()
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.Local)

	// 1 小时前 +10：70 + 10*0.5^(1/3)
	events := []MoodEvent{{TS: now.Add(-time.Hour).Format(time.RFC3339), D: 10}}
	state := ComputeMood(events, cfg, now)
	want := 70 + 10*math.Pow(0.5, 1.0/3.0)
	if math.Abs(state.Value-want) > 1e-9 {
		t.Fatalf("心情衰减不对：得到 %.4f，期望 %.4f", state.Value, want)
	}

	// 6 小时前 +10：70 + 10*0.25
	events = []MoodEvent{{TS: now.Add(-6 * time.Hour).Format(time.RFC3339), D: 10}}
	state = ComputeMood(events, cfg, now)
	if math.Abs(state.Value-72.5) > 1e-9 {
		t.Fatalf("两个半衰期后应该剩 1/4：得到 %.4f", state.Value)
	}

	// 上限 100
	events = []MoodEvent{{TS: now.Format(time.RFC3339), D: 999}}
	state = ComputeMood(events, cfg, now)
	if state.Value != 100 {
		t.Fatalf("心情上限是 100，得到 %.4f", state.Value)
	}

	// 下限 clamp
	cfg.Floor = 60
	events = []MoodEvent{{TS: now.Format(time.RFC3339), D: -999}}
	state = ComputeMood(events, cfg, now)
	if state.Value != 60 {
		t.Fatalf("心情下限是 floor，得到 %.4f", state.Value)
	}
}

func TestComputeMoodEmotionDecayAndVisibility(t *testing.T) {
	cfg := DefaultMoodConfig()
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.Local)

	events := []MoodEvent{{
		TS: now.Add(-90 * time.Minute).Format(time.RFC3339),
		E:  map[string]float64{"开心": 4, "吃醋": 4},
	}}
	state := ComputeMood(events, cfg, now)
	if math.Abs(state.Emo["开心"]-2) > 1e-9 {
		t.Fatalf("90 分钟后应该减半：得到 %v", state.Emo["开心"])
	}

	// 450 分钟 = 5 个半衰期：4*0.5^5 = 0.125 < 0.3，应该被丢掉。
	events = []MoodEvent{{TS: now.Add(-450 * time.Minute).Format(time.RFC3339), E: map[string]float64{"开心": 4}}}
	state = ComputeMood(events, cfg, now)
	if _, ok := state.Emo["开心"]; ok {
		t.Fatalf("小于 0.3 的情绪应该丢掉：%v", state.Emo)
	}
	if len(state.Emo) != 0 {
		t.Fatalf("不该剩下别的情绪：%v", state.Emo)
	}
}

func TestMoodLineFormat(t *testing.T) {
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.Local)
	cfg := DefaultMoodConfig()

	events := []MoodEvent{
		{TS: now.Format(time.RFC3339), D: 9},
		{TS: now.Add(-192 * time.Minute).Format(time.RFC3339), Why: "跟 zlin 把功能跑通了"},
	}
	state := ComputeMood(events, cfg, now)
	// 70 + 9 = 79（最近这条 why 的 d=0，不影响心情）
	line := state.Line()
	want := "心情 79/100（基线 70，+9） ｜ 最近：跟 zlin 把功能跑通了（3.2 小时前）"
	if line != want {
		t.Fatalf("渲染不对：\n得到 %q\n期望 %q", line, want)
	}

	// 有情绪时插在中间，按浓度排序。
	events = []MoodEvent{{
		TS: now.Format(time.RFC3339),
		D:  9,
		E:  map[string]float64{"甜蜜": 4, "开心": 5},
	}}
	state = ComputeMood(events, cfg, now)
	line = state.Line()
	if !strings.Contains(line, " ｜ 开心+5 甜蜜+4") {
		t.Fatalf("情绪段不对：%q", line)
	}

	// 差值为 0 时不写 +/-，没有情绪/why 时不写那两段。
	state = ComputeMood(nil, cfg, now)
	if got, want := state.Line(), "心情 70/100（基线 70）"; got != want {
		t.Fatalf("空状态渲染不对：得到 %q，期望 %q", got, want)
	}
}

func TestMoodLineNegativeDiffAndUnknownEmotion(t *testing.T) {
	now := time.Date(2026, 10, 25, 20, 0, 0, 0, time.Local)
	cfg := DefaultMoodConfig()

	events := []MoodEvent{{TS: now.Format(time.RFC3339), D: -8, E: map[string]float64{"难过": 3, "不存在": 9}}}
	state := ComputeMood(events, cfg, now)
	line := state.Line()
	if !strings.HasPrefix(line, "心情 62/100（基线 70，-8） ｜ 难过+3") {
		t.Fatalf("负增长渲染不对：%q", line)
	}
	if strings.Contains(line, "不存在") {
		t.Fatalf("不认识的情绪不该出现在渲染里：%q", line)
	}
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		hours float64
		want  string
	}{
		{0.25, "15 分钟前"},
		{0.5, "30 分钟前"},
		{1.5, "1.5 小时前"},
		{3.2, "3.2 小时前"},
		{25, "1.0 天前"},
		{48, "2.0 天前"},
	}
	for _, c := range cases {
		if got := FormatAge(c.hours); got != c.want {
			t.Fatalf("FormatAge(%v) = %q，期望 %q", c.hours, got, c.want)
		}
	}
}

func TestAddMoodEventPersistsAndReloads(t *testing.T) {
	ws := t.TempDir()
	now := time.Now()

	emo, err := ParseEmoSpec("开心=4,甜蜜=2")
	if err != nil {
		t.Fatalf("ParseEmoSpec: %v", err)
	}
	state, err := AddMoodEvent(ws, 3, emo, "被夸了", "ses_1", now)
	if err != nil {
		t.Fatalf("AddMoodEvent: %v", err)
	}
	if state.Value <= 70 {
		t.Fatalf("加了 +3 心情应该上涨：%.2f", state.Value)
	}
	if state.Emo["开心"] != 4 {
		t.Fatalf("情绪没记上：%v", state.Emo)
	}
	if !state.HasWhy || state.Why != "被夸了" {
		t.Fatalf("why 没记上：%+v", state)
	}

	events, err := LoadMoodEvents(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("想要 1 条事件，得到 %d", len(events))
	}
	if events[0].Sid != "ses_1" {
		t.Fatalf("sid 没写进去：%+v", events[0])
	}

	logLine := FormatMoodLog(events[0])
	if !strings.Contains(logLine, "+3") || !strings.Contains(logLine, "开心+4") || !strings.Contains(logLine, "被夸了") {
		t.Fatalf("日志渲染不对：%q", logLine)
	}

	backup, err := ResetMood(ws, now)
	if err != nil {
		t.Fatalf("ResetMood: %v", err)
	}
	if backup == "" {
		t.Fatal("reset 应该留个备份路径")
	}
	if _, err := os.Stat(MoodEventsPath(ws)); !os.IsNotExist(err) {
		t.Fatalf("reset 之后事件文件应该没了：%v", err)
	}
	state, err = LoadMood(ws)
	if err != nil {
		t.Fatal(err)
	}
	if state.Value != 70 {
		t.Fatalf("reset 之后应该回到基线：%.2f", state.Value)
	}
}

func TestParseEmoSpecRejectsBadInput(t *testing.T) {
	if _, err := ParseEmoSpec("开心"); err == nil {
		t.Fatal("缺少 = 应该报错")
	}
	if _, err := ParseEmoSpec("生气=3"); err == nil {
		t.Fatal("不认识的情绪应该报错")
	}
	if _, err := ParseEmoSpec("开心=abc"); err == nil {
		t.Fatal("非数字应该报错")
	}
}

func TestMoodConfigFallsBackToDefaults(t *testing.T) {
	ws := t.TempDir()
	cfg := LoadMoodConfig(ws)
	if cfg.Baseline != 70 || cfg.MoodHalfHours != 3 || cfg.EmoHalfMin != 90 || cfg.Floor != 0 {
		t.Fatalf("缺省配置不对：%+v", cfg)
	}

	if err := ensureDir(MoodDir(ws)); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"baseline": 55, "mood_half_hours": 2})
	if err := os.WriteFile(MoodConfigPath(ws), body, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg = LoadMoodConfig(ws)
	if cfg.Baseline != 55 || cfg.MoodHalfHours != 2 {
		t.Fatalf("配置文件没生效：%+v", cfg)
	}
	if cfg.EmoHalfMin != 90 {
		t.Fatalf("没写的字段应该保持默认：%+v", cfg)
	}
}
