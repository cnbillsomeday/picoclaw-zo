package fish

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func mondayAt(hour, minute int) time.Time {
	return time.Date(2026, 10, 26, hour, minute, 0, 0, time.Local) // 周一
}

func saturdayAt(hour, minute int) time.Time {
	return time.Date(2026, 10, 31, hour, minute, 0, 0, time.Local)
}

func TestSpeakLineFormula(t *testing.T) {
	cfg := DefaultSpeakConfig()

	// 峰时、没有回信记录、连挂两次：20*1 + 20 = 40
	plan := previewSpeak(cfg, SpeakState{Misses: 2}, mondayAt(10, 0))
	if !plan.Peak {
		t.Fatal("周一 10 点应该在峰时")
	}
	if plan.BaseLine != 20 {
		t.Fatalf("峰时基础线应该是 20，得到 %v", plan.BaseLine)
	}
	if plan.Factor != 1.0 {
		t.Fatalf("没有回信记录时因子应该是 1，得到 %v", plan.Factor)
	}
	if plan.Bonus != 20 {
		t.Fatalf("连挂两次加成应该是 20，得到 %v", plan.Bonus)
	}
	if plan.Line != 40 {
		t.Fatalf("命中线应该是 40，得到 %v", plan.Line)
	}

	// 刚聊过（10 分钟前）：20*0.15 + 20 = 23
	state := SpeakState{Misses: 2, LastInbound: mondayAt(9, 50).Format(time.RFC3339)}
	plan = previewSpeak(cfg, state, mondayAt(10, 0))
	if math.Abs(plan.Factor-0.15) > 1e-9 {
		t.Fatalf("20 分钟内聊过因子应该是 0.15，得到 %v", plan.Factor)
	}
	if math.Abs(plan.Line-23) > 1e-9 {
		t.Fatalf("命中线应该是 23，得到 %v", plan.Line)
	}

	// 冷太久（7 小时前）：20*1.5 + 20 = 50
	state = SpeakState{Misses: 2, LastInbound: mondayAt(3, 0).Format(time.RFC3339)}
	plan = previewSpeak(cfg, state, mondayAt(10, 0))
	if plan.Factor != 1.5 {
		t.Fatalf("超过 6 小时没说话因子应该是 1.5，得到 %v", plan.Factor)
	}
	if math.Abs(plan.Line-50) > 1e-9 {
		t.Fatalf("命中线应该是 50，得到 %v", plan.Line)
	}

	// 中间地带（2 小时前）：20*1 + 20 = 40
	state = SpeakState{Misses: 2, LastInbound: mondayAt(8, 0).Format(time.RFC3339)}
	plan = previewSpeak(cfg, state, mondayAt(10, 0))
	if plan.Factor != 1.0 {
		t.Fatalf("中间地带因子应该是 1，得到 %v", plan.Factor)
	}

	// 谷时（周六）：基础线 60
	plan = previewSpeak(cfg, SpeakState{}, saturdayAt(20, 0))
	if plan.Peak {
		t.Fatal("周末不该算峰时")
	}
	if plan.BaseLine != 60 {
		t.Fatalf("谷时基础线应该是 60，得到 %v", plan.BaseLine)
	}

	// 加成封顶 40
	plan = previewSpeak(cfg, SpeakState{Misses: 99}, saturdayAt(20, 0))
	if plan.Bonus != 40 {
		t.Fatalf("加成应该封顶 40，得到 %v", plan.Bonus)
	}
	if plan.Line != 100 {
		t.Fatalf("命中线上限应该是 100，得到 %v", plan.Line)
	}
}

func TestInQuietCrossMidnight(t *testing.T) {
	cases := []struct {
		hour  int
		quiet [2]int
		want  bool
	}{
		{23, [2]int{22, 6}, true},  // 跨午夜，起点
		{2, [2]int{22, 6}, true},   // 跨午夜，中间
		{5, [2]int{22, 6}, true},   // 跨午夜，末尾前一小时
		{6, [2]int{22, 6}, false},  // 含起不含止
		{12, [2]int{22, 6}, false}, // 白天
		{1, [2]int{1, 8}, true},    // 普通区间
		{7, [2]int{1, 8}, true},
		{8, [2]int{1, 8}, false},
		{3, [2]int{3, 3}, false},   // 起===止 表示不启用
		{0, [2]int{0, 0}, false},   // 同上
		{5, [2]int{5, 0}, true},    // 5 点到 0 点：跨午夜
	}
	for _, c := range cases {
		if got := InQuiet(c.hour, c.quiet); got != c.want {
			t.Fatalf("InQuiet(%d, %v) = %v，期望 %v", c.hour, c.quiet, got, c.want)
		}
	}
}

func TestCooldownBlocksRoll(t *testing.T) {
	ws := t.TempDir()
	now := saturdayAt(20, 0)
	if err := SaveSpeakState(ws, SpeakState{LastSpoke: now.Add(-5 * time.Minute).Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	plan := evaluateSpeak(ws, now, 0)
	if plan.Decision != SpeakDecisionIdle {
		t.Fatalf("冷却期内应该是 idle，得到 %s", plan.Decision)
	}
	if !strings.Contains(plan.Reason, "冷却") {
		t.Fatalf("原因应该提到冷却：%q", plan.Reason)
	}
	if plan.Go {
		t.Fatal("冷却期内不该开口")
	}
}

func TestDisabledAndQuietStayIdle(t *testing.T) {
	ws := t.TempDir()
	cfg := DefaultSpeakConfig()
	cfg.Enabled = false
	if err := writeSpeakConfig(SpeakConfigPath(ws), cfg); err != nil {
		t.Fatal(err)
	}
	plan := evaluateSpeak(ws, saturdayAt(20, 0), 0)
	if plan.Decision != SpeakDecisionIdle || !strings.Contains(plan.Reason, "总开关") {
		t.Fatalf("关掉开关应该 idle，得到 %+v", plan)
	}

	ws = t.TempDir()
	// 凌晨 3 点在默认安静时段 1-8 内
	plan = evaluateSpeak(ws, saturdayAt(3, 0), 0)
	if plan.Decision != SpeakDecisionIdle {
		t.Fatalf("安静时段应该 idle，得到 %s", plan.Decision)
	}
	if !strings.Contains(plan.Reason, "安静时段") {
		t.Fatalf("原因应该提到安静时段：%q", plan.Reason)
	}
}

func TestDryRunNeverGoesButSettlesState(t *testing.T) {
	ws := t.TempDir()
	now := saturdayAt(20, 0) // 谷时，基础线 60

	if err := SaveSpeakState(ws, SpeakState{Misses: 3, Ticks: 7}); err != nil {
		t.Fatal(err)
	}

	plan := evaluateSpeak(ws, now, 10) // 10 < 60+30，命中
	if !plan.Hit {
		t.Fatalf("10 应该掷中（线 %.2f）", plan.Line)
	}
	if plan.Decision != SpeakDecisionDryRun {
		t.Fatalf("dry_run 下应该是 dry_run，得到 %s", plan.Decision)
	}
	if plan.Go {
		t.Fatal("dry_run 下不该真的开口")
	}

	state, err := LoadSpeakState(ws)
	if err != nil {
		t.Fatal(err)
	}
	if state.Misses != 0 {
		t.Fatalf("掷中要清零连挂计数，得到 %d", state.Misses)
	}
	if state.LastSpoke == "" {
		t.Fatal("掷中要记 last_spoke")
	}
	if state.Ticks != 8 {
		t.Fatalf("ticks 应该 +1，得到 %d", state.Ticks)
	}

	entries := readSpeakLog(t, ws)
	if len(entries) != 1 {
		t.Fatalf("应该记一行账，得到 %d", len(entries))
	}
	if entries[0].Decision != SpeakDecisionDryRun {
		t.Fatalf("账本 decision 不对：%+v", entries[0])
	}
	if !strings.Contains(string(mustReadFile(t, SpeakLogPath(ws))), `"would_send"`) {
		t.Fatal("dry_run 账本应该带 would_send 字段")
	}
}

func TestMissedIncrementsAndBonusGrows(t *testing.T) {
	ws := t.TempDir()
	now := saturdayAt(20, 0)

	plan := evaluateSpeak(ws, now, 99) // 线 60，99 >= 60 → 没中
	if plan.Decision != SpeakDecisionMissed {
		t.Fatalf("应该没掷中，得到 %s", plan.Decision)
	}
	if plan.Go {
		t.Fatal("没掷中不该开口")
	}
	state, err := LoadSpeakState(ws)
	if err != nil {
		t.Fatal(err)
	}
	if state.Misses != 1 {
		t.Fatalf("没中应该 +1，得到 %d", state.Misses)
	}
	if state.LastSpoke != "" {
		t.Fatal("没中不该记 last_spoke")
	}

	// 第二次：连挂一次加成 +10，命中线涨到 70
	plan = evaluateSpeak(ws, now.Add(time.Minute), 65)
	if plan.Line != 70 {
		t.Fatalf("连挂一次后命中线应该是 70，得到 %.2f", plan.Line)
	}
	if !plan.Hit {
		t.Fatalf("65 < 70 应该掷中，得到 roll=%.2f", plan.Roll)
	}
	state, err = LoadSpeakState(ws)
	if err != nil {
		t.Fatal(err)
	}
	if state.Misses != 0 {
		t.Fatalf("掷中后连挂计数要清零，得到 %d", state.Misses)
	}
}

func TestEvaluateSpeakGoOnlyWhenLive(t *testing.T) {
	ws := t.TempDir()
	cfg := DefaultSpeakConfig()
	cfg.DryRun = false
	cfg.Quiet = [2]int{0, 0}
	if err := writeSpeakConfig(SpeakConfigPath(ws), cfg); err != nil {
		t.Fatal(err)
	}

	plan := evaluateSpeak(ws, saturdayAt(20, 0), 1)
	if !plan.Go {
		t.Fatalf("关掉 dry_run 且掷中时应该 Go，得到 %+v", plan)
	}
	if plan.Decision != SpeakDecisionSent {
		t.Fatalf("应该记 sent，得到 %s", plan.Decision)
	}

	entries := readSpeakLog(t, ws)
	if len(entries) != 1 || entries[0].Decision != SpeakDecisionSent {
		t.Fatalf("账本不对：%+v", entries)
	}
}

func TestPreviewSpeakDoesNotMutate(t *testing.T) {
	ws := t.TempDir()
	plan := PreviewSpeak(ws, saturdayAt(20, 0))
	if plan.Decision != SpeakDecisionReady {
		t.Fatalf("预览应该是 ready，得到 %s（%s）", plan.Decision, plan.Reason)
	}
	if plan.Line != 60 {
		t.Fatalf("谷时命中线应该是 60，得到 %.2f", plan.Line)
	}
	if _, err := os.Stat(SpeakStatePath(ws)); !os.IsNotExist(err) {
		t.Fatalf("预览不该写 state：%v", err)
	}
	if _, err := os.Stat(SpeakLogPath(ws)); !os.IsNotExist(err) {
		t.Fatalf("预览不该写账本：%v", err)
	}
}

func TestLoadSpeakConfigCreatesDefaultsInNewWorkspace(t *testing.T) {
	ws := t.TempDir() + "/不存在的工作区"
	cfg, err := LoadSpeakConfig(ws)
	if err != nil {
		t.Fatalf("LoadSpeakConfig: %v", err)
	}
	if !cfg.Enabled || !cfg.DryRun {
		t.Fatalf("缺省应该开启且试运行：%+v", cfg)
	}
	if cfg.IntervalMin != 30 || cfg.CooldownMin != 20 || cfg.Quiet != [2]int{1, 8} {
		t.Fatalf("缺省值不对：%+v", cfg)
	}
	if len(cfg.PeakHours) != 2 || cfg.PeakHours[0] != [2]int{9, 12} {
		t.Fatalf("峰时缺省值不对：%+v", cfg.PeakHours)
	}
	if _, err := os.Stat(SpeakConfigPath(ws)); err != nil {
		t.Fatalf("应该把缺省配置写到文件：%v", err)
	}
}

func TestTouchInboundOnlyBumpsTimestamp(t *testing.T) {
	ws := t.TempDir()
	now := saturdayAt(20, 0)
	if err := SaveSpeakState(ws, SpeakState{Misses: 2, Ticks: 4}); err != nil {
		t.Fatal(err)
	}
	if err := TouchInbound(ws, now); err != nil {
		t.Fatalf("TouchInbound: %v", err)
	}
	state, err := LoadSpeakState(ws)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastInbound != now.Format(time.RFC3339) {
		t.Fatalf("last_inbound 没记上：%q", state.LastInbound)
	}
	if state.Misses != 2 || state.Ticks != 4 {
		t.Fatalf("别的字段不该动：%+v", state)
	}
}

func readSpeakLog(t *testing.T, ws string) []speakLogEntry {
	t.Helper()
	raw, err := os.ReadFile(SpeakLogPath(ws))
	if err != nil {
		t.Fatalf("读账本失败：%v", err)
	}
	var out []speakLogEntry
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var entry speakLogEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("账本不是合法 JSON：%v（%q）", err, line)
		}
		out = append(out, entry)
	}
	return out
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读文件失败 %s：%v", path, err)
	}
	return raw
}
