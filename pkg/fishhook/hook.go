// Package fishhook 把 pkg/fish 包装成一个 JSON-RPC over stdio 的 process hook。
//
// 宿主（PicoClaw）在每次调 LLM 前调用 hook.before_llm，这里负责：
//   - 记下「他最近说过话」的时间（给主动搭话算时机因子）
//   - 按最后一条 user 消息做记忆召回 + 现算心情，注入到那条消息末尾
//
// 任何异常都必须降级成 continue：这个进程可以让注入失效，但绝不能让会话失败。
package fishhook

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/fish"
)

const (
	startMarker = "<!--fish:start-->"
	endMarker   = "<!--fish:end-->"

	hookName = "fish"

	maxLineSize = 8 * 1024 * 1024
	scanBufSize = 64 * 1024

	// cronSessionPrefix 是 cron 触发回合的 session_key 前缀。
	cronSessionPrefix = "agent:cron-"

	// speakMarker 是「主动搭话」回合的标记：带这个标记的回合要抽掉工具、
	// 限死输出长度，逼它一句话说清楚，别把它当任务去查资料。
	speakMarker = "<!--speak-up-->"

	// speakMaxTokens 是主动搭话回合的输出上限（一句话够用）。
	speakMaxTokens = 200

	memoryHeading = "## 记忆（自动召回，按相关的旧事；不确定就说不记得，别顺着编）"
	moodHeading   = "## 心情（现算，会自然回落）"
)

var fishBlockPattern = regexp.MustCompile(`(?s)` + regexp.QuoteMeta(startMarker) + `.*?` + regexp.QuoteMeta(endMarker))

// Run 常驻运行 hook 进程，直到 stdin 关闭或 ctx 结束。
func Run(ctx context.Context, workspace string) error {
	return Serve(ctx, os.Stdin, os.Stdout, workspace)
}

// Serve 是 Run 的可注入 IO 版本（单测用 io.Pipe 模拟宿主）。
func Serve(ctx context.Context, in io.Reader, out io.Writer, workspace string) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, scanBufSize), maxLineSize)

	writer := bufio.NewWriter(out)

	for scanner.Scan() {
		if ctx != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
		}

		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		resp := handleLine(workspace, append([]byte(nil), line...))
		if resp == nil {
			continue
		}
		if _, err := writer.Write(resp); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}

	return scanner.Err()
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// handleLine 处理一行请求，返回要写回的行（nil 表示这条不需要回）。
func handleLine(workspace string, line []byte) (resp []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		logf("解码请求失败：%v", err)
		return nil
	}
	// 没有 id 或 id 为空 = notification，宿主不等待响应。
	if len(req.ID) == 0 || string(bytes.TrimSpace(req.ID)) == "null" {
		return nil
	}

	result, err := dispatch(workspace, req.Method, req.Params)
	out := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if err != nil {
		out.Error = &rpcError{Code: -32000, Message: err.Error()}
	} else {
		if result == nil {
			result = map[string]any{}
		}
		out.Result = result
	}

	body, err := json.Marshal(out)
	if err != nil {
		logf("编码响应失败：%v", err)
		return nil
	}
	return body
}

// dispatch 按方法名分发；panic 在这里被吃掉，降级为放行。
func dispatch(workspace, method string, params json.RawMessage) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			logf("处理 %s 时 panic：%v", method, r)
			result = continueFor(method)
			err = nil
		}
	}()

	switch method {
	case "hook.hello":
		return map[string]any{"ok": true, "name": hookName}, nil
	case "hook.before_llm":
		return handleBeforeLLM(workspace, params), nil
	case "hook.after_llm", "hook.before_tool", "hook.after_tool":
		return map[string]any{"action": "continue"}, nil
	case "hook.approve_tool":
		return map[string]any{"approved": true}, nil
	default:
		return nil, fmt.Errorf("method not found: %s", method)
	}
}

// continueFor 给出该方法的安全默认响应。
func continueFor(method string) map[string]any {
	if method == "hook.approve_tool" {
		return map[string]any{"approved": true}
	}
	return map[string]any{"action": "continue"}
}

// handleBeforeLLM 注入心情 + 记忆，幂等（先删旧段再追加）。
func handleBeforeLLM(workspace string, params json.RawMessage) map[string]any {
	fallback := map[string]any{"action": "continue"}

	var req map[string]json.RawMessage
	if err := json.Unmarshal(params, &req); err != nil || req == nil {
		logf("before_llm 参数解析失败：%v", err)
		return fallback
	}

	sessionKey := metaSessionKey(req["meta"])
	if !isCronSession(sessionKey) {
		if err := fish.TouchInbound(workspace, time.Now()); err != nil {
			logf("记 last_inbound 失败：%v", err)
		}
	}

	rawMessages, ok := req["messages"]
	if !ok {
		return fallback
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(rawMessages, &messages); err != nil {
		logf("messages 解析失败：%v", err)
		return fallback
	}

	idx := lastUserMessageIndex(messages)
	if idx < 0 {
		return fallback
	}
	var last struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(messages[idx], &last); err != nil {
		logf("最后一条 user 消息解析失败：%v", err)
		return fallback
	}

	cleaned := stripFishBlock(last.Content)

	// 主动搭话回合：去掉标记、抽掉工具、限死输出，避免模型把它当任务去做。
	speakTurn := strings.Contains(cleaned, speakMarker)
	if speakTurn {
		cleaned = strings.TrimSpace(strings.ReplaceAll(cleaned, speakMarker, ""))
	}

	if strings.TrimSpace(cleaned) == "" {
		// 空正文没什么可召回的，也别把一条空消息撑出内容。
		return fallback
	}
	block := buildBlock(workspace, cleaned, sessionKey)
	if block == "" {
		return fallback
	}

	newContent := strings.TrimRight(cleaned, " \t\r\n")
	if newContent != "" {
		newContent += "\n\n"
	}
	newContent += block

	var patched map[string]json.RawMessage
	if err := json.Unmarshal(messages[idx], &patched); err != nil || patched == nil {
		logf("最后一条 user 消息改写失败：%v", err)
		return fallback
	}
	encodedContent, err := json.Marshal(newContent)
	if err != nil {
		return fallback
	}
	patched["content"] = encodedContent
	encodedMessage, err := json.Marshal(patched)
	if err != nil {
		return fallback
	}
	messages[idx] = encodedMessage

	encodedMessages, err := json.Marshal(messages)
	if err != nil {
		return fallback
	}
	req["messages"] = encodedMessages

	if speakTurn {
		// tools 置空 = 这一轮没有可调用的工具（模型只能直接说话）
		req["tools"] = json.RawMessage("[]")
		if opts, err := patchMaxTokens(req["options"], speakMaxTokens); err == nil && opts != nil {
			req["options"] = opts
		}
	}

	return map[string]any{"action": "modify", "request": req}
}

// buildBlock 拼出注入段；心情和记忆都为空时返回空串。
func buildBlock(workspace, query, sessionKey string) (block string) {
	defer func() {
		if r := recover(); r != nil {
			logf("拼注入段 panic：%v", r)
			block = ""
		}
	}()

	moodLine := ""
	if mood, err := fish.LoadMood(workspace); err != nil {
		logf("读心情失败：%v", err)
	} else {
		moodLine = mood.Line()
	}

	memBlock := ""
	if strings.TrimSpace(query) != "" {
		if out, err := fish.RecallWorkspace(workspace, query, sessionKey, fish.DefaultRecallLimit, true); err != nil {
			logf("记忆召回失败：%v", err)
		} else {
			memBlock = out.Block
		}
	}

	if moodLine == "" && memBlock == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString(startMarker)
	if moodLine != "" {
		b.WriteString("\n")
		b.WriteString(moodHeading)
		b.WriteString("\n")
		b.WriteString(moodLine)
	}
	if memBlock != "" {
		b.WriteString("\n")
		b.WriteString(memoryHeading)
		b.WriteString("\n")
		b.WriteString(memBlock)
	}
	b.WriteString("\n")
	b.WriteString(endMarker)
	return b.String()
}

// stripFishBlock 删掉文本里已有的注入段（可能多段）。
func stripFishBlock(content string) string {
	if !strings.Contains(content, startMarker) {
		return content
	}
	cleaned := fishBlockPattern.ReplaceAllString(content, "")
	return strings.TrimRight(cleaned, " \t\r\n")
}

// lastUserMessageIndex 找最后一条 role=="user" 的消息下标。
func lastUserMessageIndex(messages []json.RawMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(messages[i], &probe); err != nil {
			continue
		}
		if probe.Role == "user" {
			return i
		}
	}
	return -1
}

// metaSessionKey 兼容 SessionKey / session_key 两种写法。
func metaSessionKey(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var meta map[string]any
	if err := json.Unmarshal(raw, &meta); err != nil {
		return ""
	}
	for key, value := range meta {
		if !strings.EqualFold(key, "sessionkey") && key != "session_key" {
			continue
		}
		if s, ok := value.(string); ok {
			return s
		}
	}
	return ""
}

func isCronSession(sessionKey string) bool {
	return strings.HasPrefix(sessionKey, cronSessionPrefix)
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[fishhook] "+format+"\n", args...)
}

// patchMaxTokens 在原有 options 上覆盖 max_tokens，保留其它键（选项整体替换，
// 少写一个键就会丢模型参数）。
func patchMaxTokens(raw json.RawMessage, maxTokens int) (json.RawMessage, error) {
	opts := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &opts); err != nil {
			return nil, err
		}
	}
	opts["max_tokens"] = maxTokens
	return json.Marshal(opts)
}
