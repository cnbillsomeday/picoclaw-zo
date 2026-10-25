// Package fish 实现「鱼的记忆」三件事：记忆卡片、心情、主动搭话。
//
// 这一层是纯逻辑 + 文件存取，不依赖 agent，可单独单测。
// 文件布局（相对工作区）：
//
//	fish/cards.jsonl            记忆卡片
//	fish/recall.log             召回账本
//	fish/mood/events.jsonl      心情事件
//	fish/mood/config.json       心情配置
//	fish/speak/config.json      主动搭话配置
//	fish/speak/state.json       主动搭话状态
//	fish/speak/speak.log        掷骰账本
package fish

import (
	"os"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/config"
)

// WorkspacePath 按 PICOCLAW_HOME 推导工作区路径。
//
// 顺序和 CLI 一致：$PICOCLAW_HOME/workspace，没有该环境变量时
// 回落到 ~/.picoclaw/workspace（config.GetHome 已经封装了这两步）。
func WorkspacePath() string {
	return filepath.Join(config.GetHome(), "workspace")
}

// Dir 返回工作区下的 fish 目录。
func Dir(workspace string) string {
	return filepath.Join(workspace, "fish")
}

// CardsPath 返回记忆卡片文件路径。
func CardsPath(workspace string) string {
	return filepath.Join(Dir(workspace), "cards.jsonl")
}

// RecallLogPath 返回召回账本路径。
func RecallLogPath(workspace string) string {
	return filepath.Join(Dir(workspace), "recall.log")
}

// MoodDir 返回心情目录。
func MoodDir(workspace string) string {
	return filepath.Join(Dir(workspace), "mood")
}

// MoodConfigPath 返回心情配置路径。
func MoodConfigPath(workspace string) string {
	return filepath.Join(MoodDir(workspace), "config.json")
}

// MoodEventsPath 返回心情事件路径。
func MoodEventsPath(workspace string) string {
	return filepath.Join(MoodDir(workspace), "events.jsonl")
}

// SpeakDir 返回主动搭话目录。
func SpeakDir(workspace string) string {
	return filepath.Join(Dir(workspace), "speak")
}

// SpeakConfigPath 返回主动搭话配置路径。
func SpeakConfigPath(workspace string) string {
	return filepath.Join(SpeakDir(workspace), "config.json")
}

// SpeakStatePath 返回主动搭话状态路径。
func SpeakStatePath(workspace string) string {
	return filepath.Join(SpeakDir(workspace), "state.json")
}

// SpeakLogPath 返回掷骰账本路径。
func SpeakLogPath(workspace string) string {
	return filepath.Join(SpeakDir(workspace), "speak.log")
}

// ensureDir 建目录（0755），已存在不算错。
func ensureDir(dir string) error {
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
