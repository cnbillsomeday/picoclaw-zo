package fish

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 七根情绪，顺序固定。
var MoodEmotions = []string{"开心", "踏实", "甜蜜", "心疼", "担心", "难过", "吃醋"}

// 心情默认配置。
const (
	DefaultMoodBaseline     = 70.0
	DefaultMoodFloor        = 0.0
	DefaultMoodHalfHours    = 3.0
	DefaultEmoHalfMinutes   = 90.0
	MoodEmoVisibleThreshold = 0.3
)

// MoodConfig 是 mood/config.json 的结构。
type MoodConfig struct {
	Baseline      float64 `json:"baseline"`
	Floor         float64 `json:"floor"`
	MoodHalfHours float64 `json:"mood_half_hours"`
	EmoHalfMin    float64 `json:"emo_half_min"`
}

// DefaultMoodConfig 返回缺省心情配置。
func DefaultMoodConfig() MoodConfig {
	return MoodConfig{
		Baseline:      DefaultMoodBaseline,
		Floor:         DefaultMoodFloor,
		MoodHalfHours: DefaultMoodHalfHours,
		EmoHalfMin:    DefaultEmoHalfMinutes,
	}
}

// MoodEvent 是一条心情事件。
type MoodEvent struct {
	TS  string             `json:"ts"`
	D   float64            `json:"d"`
	E   map[string]float64 `json:"e"`
	Why string             `json:"why"`
	Sid string             `json:"sid"`
}

// MoodState 是现算出来的心情。
type MoodState struct {
	Value    float64
	Baseline float64
	Emo      map[string]float64
	Why      string
	AgeHours float64
	HasWhy   bool
}

// Line 渲染成单行，给注入和 CLI 用。
func (m MoodState) Line() string {
	parts := make([]string, 0, 3)

	head := fmt.Sprintf("心情 %d/100（基线 %s", int(math.Round(m.Value)), formatNum(m.Baseline))
	if diff := int(math.Round(m.Value - m.Baseline)); diff != 0 {
		head += fmt.Sprintf("，%+d）", diff)
	} else {
		head += "）"
	}
	parts = append(parts, head)

	if len(m.Emo) > 0 {
		names := make([]string, 0, len(m.Emo))
		for name := range m.Emo {
			names = append(names, name)
		}
		sort.SliceStable(names, func(i, j int) bool {
			if m.Emo[names[i]] == m.Emo[names[j]] {
				return emotionRank(names[i]) < emotionRank(names[j])
			}
			return m.Emo[names[i]] > m.Emo[names[j]]
		})
		chunks := make([]string, 0, len(names))
		for _, name := range names {
			chunks = append(chunks, name+"+"+formatNum(m.Emo[name]))
		}
		parts = append(parts, strings.Join(chunks, " "))
	}

	if m.HasWhy {
		parts = append(parts, fmt.Sprintf("最近：%s（%s）", m.Why, FormatAge(m.AgeHours)))
	}
	return strings.Join(parts, " ｜ ")
}

// FormatAge 把小时数渲染成「N 分钟前 / N.N 小时前 / N.N 天前」。
func FormatAge(hours float64) string {
	if hours < 1 {
		return fmt.Sprintf("%d 分钟前", int(math.Round(hours*60)))
	}
	if hours < 24 {
		return fmt.Sprintf("%.1f 小时前", hours)
	}
	return fmt.Sprintf("%.1f 天前", hours/24)
}

// LoadMoodConfig 读配置，缺文件或缺字段都回落到默认值。
func LoadMoodConfig(workspace string) MoodConfig {
	cfg := DefaultMoodConfig()
	raw, err := os.ReadFile(MoodConfigPath(workspace))
	if err == nil {
		_ = json.Unmarshal(raw, &cfg)
	}
	return cfg
}

// LoadMoodEvents 读全部心情事件（坏行跳过）。
func LoadMoodEvents(workspace string) ([]MoodEvent, error) {
	events := make([]MoodEvent, 0, 64)
	err := scanJSONLines(MoodEventsPath(workspace), func(line []byte) error {
		var e MoodEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return err
		}
		events = append(events, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return events, nil
}

// ComputeMood 读取时现算：心情按 3 小时半衰，情绪按 90 分钟半衰。
func ComputeMood(events []MoodEvent, cfg MoodConfig, now time.Time) MoodState {
	if now.IsZero() {
		now = time.Now()
	}
	moodHalf := cfg.MoodHalfHours
	if moodHalf <= 0 {
		moodHalf = DefaultMoodHalfHours
	}
	emoHalf := cfg.EmoHalfMin
	if emoHalf <= 0 {
		emoHalf = DefaultEmoHalfMinutes
	}

	value := cfg.Baseline
	emo := make(map[string]float64, len(MoodEmotions))
	for _, name := range MoodEmotions {
		emo[name] = 0
	}

	state := MoodState{Baseline: cfg.Baseline}
	for _, e := range events {
		ageHours := now.Sub(parseMoodTS(e.TS, now)).Hours()
		if ageHours < 0 {
			ageHours = 0
		}
		if e.D != 0 {
			value += e.D * math.Pow(0.5, ageHours/moodHalf)
		}
		for name, v := range e.E {
			if _, ok := emo[name]; !ok {
				continue
			}
			emo[name] += v * math.Pow(0.5, (ageHours*60)/emoHalf)
		}
		if strings.TrimSpace(e.Why) != "" {
			state.Why = e.Why
			state.AgeHours = ageHours
			state.HasWhy = true
		}
	}

	value = math.Max(cfg.Floor, math.Min(100, value))
	state.Value = value

	visible := make(map[string]float64, len(emo))
	for name, v := range emo {
		v = math.Round(v*10) / 10
		if math.Abs(v) < MoodEmoVisibleThreshold {
			continue
		}
		visible[name] = v
	}
	state.Emo = visible
	return state
}

// LoadMood 现算当前心情。
func LoadMood(workspace string) (MoodState, error) {
	events, err := LoadMoodEvents(workspace)
	if err != nil {
		return MoodState{}, err
	}
	return ComputeMood(events, LoadMoodConfig(workspace), time.Now()), nil
}

// AddMoodEvent 记一笔心情，返回记完之后的现算结果。
func AddMoodEvent(workspace string, d float64, emo map[string]float64, why, sid string, now time.Time) (MoodState, error) {
	if now.IsZero() {
		now = time.Now()
	}
	clean := make(map[string]float64, len(emo))
	for name, v := range emo {
		if !validEmotion(name) {
			continue
		}
		clean[name] = v
	}
	event := MoodEvent{
		TS:  now.Format(time.RFC3339),
		D:   d,
		E:   clean,
		Why: why,
		Sid: sid,
	}
	if err := appendJSONLine(MoodEventsPath(workspace), event); err != nil {
		return MoodState{}, err
	}
	return LoadMoodAt(workspace, now)
}

// LoadMoodAt 用指定时间现算（便于单测）。
func LoadMoodAt(workspace string, now time.Time) (MoodState, error) {
	events, err := LoadMoodEvents(workspace)
	if err != nil {
		return MoodState{}, err
	}
	return ComputeMood(events, LoadMoodConfig(workspace), now), nil
}

// ResetMood 把事件文件改名备份，相当于回到基线。
func ResetMood(workspace string, now time.Time) (string, error) {
	path := MoodEventsPath(workspace)
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if now.IsZero() {
		now = time.Now()
	}
	backup := path + ".bak-" + now.Format("200601021504")
	if err := os.Rename(path, backup); err != nil {
		return "", err
	}
	return backup, nil
}

// ParseEmoSpec 解析 `开心=4,甜蜜=2` 这样的情绪参数。
func ParseEmoSpec(spec string) (map[string]float64, error) {
	out := make(map[string]float64, 2)
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, raw, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("情绪要写成 开心=4 的形式：%q", part)
		}
		name = strings.TrimSpace(name)
		if !validEmotion(name) {
			return nil, fmt.Errorf("不认识的情绪：%q（可用：%s）", name, strings.Join(MoodEmotions, " "))
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			return nil, fmt.Errorf("情绪值不是数字：%q", raw)
		}
		out[name] = v
	}
	return out, nil
}

// FormatMoodLog 把事件渲染成一行日志。
func FormatMoodLog(e MoodEvent) string {
	chunks := make([]string, 0, len(e.E))
	names := make([]string, 0, len(e.E))
	for name := range e.E {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		chunks = append(chunks, name+formatSigned(e.E[name]))
	}
	line := fmt.Sprintf("%s  %s", e.TS, formatSigned(e.D))
	if len(chunks) > 0 {
		line += "  " + strings.Join(chunks, " ")
	}
	if strings.TrimSpace(e.Why) != "" {
		line += "  " + e.Why
	}
	return line
}

func validEmotion(name string) bool {
	for _, known := range MoodEmotions {
		if known == name {
			return true
		}
	}
	return false
}

func emotionRank(name string) int {
	for i, known := range MoodEmotions {
		if known == name {
			return i
		}
	}
	return len(MoodEmotions)
}

func parseMoodTS(ts string, fallback time.Time) time.Time {
	ts = strings.TrimSpace(ts)
	if ts == "" {
		return fallback
	}
	if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
		return parsed
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, ts, time.Local); err == nil {
			return parsed
		}
	}
	return fallback
}

// formatNum 输出不带多余小数的数字。
func formatNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// formatSigned 输出带正负号的数字。
func formatSigned(v float64) string {
	if v > 0 {
		return "+" + formatNum(v)
	}
	return formatNum(v)
}
