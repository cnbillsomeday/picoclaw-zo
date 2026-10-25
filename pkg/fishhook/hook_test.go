package fishhook

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/fish"
)

type rpcReply struct {
	ID     json.RawMessage            `json:"id"`
	Result map[string]json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type harness struct {
	t      *testing.T
	in     *io.PipeWriter
	out    *bufio.Reader
	done   chan error
	cancel context.CancelFunc
}

func newHarness(t *testing.T, ws string) *harness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, inR, outW, ws)
	}()
	h := &harness{t: t, in: inW, out: bufio.NewReader(outR), done: done, cancel: cancel}
	t.Cleanup(func() {
		cancel()
		_ = inW.Close()
		_ = outW.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})
	return h
}

// call 发一行请求、收一行响应。
func (h *harness) call(payload string) rpcReply {
	h.t.Helper()

	writeErr := make(chan error, 1)
	go func() {
		_, err := h.in.Write([]byte(payload + "\n"))
		writeErr <- err
	}()

	type readResult struct {
		line []byte
		err  error
	}
	read := make(chan readResult, 1)
	go func() {
		line, err := h.out.ReadBytes('\n')
		read <- readResult{line: line, err: err}
	}()

	var got readResult
	select {
	case got = <-read:
	case <-time.After(5 * time.Second):
		h.t.Fatal("等 hook 响应超时")
	}
	if got.err != nil {
		h.t.Fatalf("读响应失败：%v", got.err)
	}
	if err := <-writeErr; err != nil {
		h.t.Fatalf("写请求失败：%v", err)
	}

	var reply rpcReply
	if err := json.Unmarshal(got.line, &reply); err != nil {
		h.t.Fatalf("响应不是合法 JSON：%v（%q）", err, string(got.line))
	}
	if reply.Error != nil {
		h.t.Fatalf("hook 返回错误：%s", reply.Error.Message)
	}
	return reply
}

func seedCards(t *testing.T, ws string) {
	t.Helper()
	for i := 0; i < 10; i++ {
		if _, _, err := fish.AddCard(ws, fish.NewCard{
			Text: "无关的旧事第 " + string(rune('一'+i)) + " 条",
			T:    "2026-10-01",
		}); err != nil {
			t.Fatalf("seed card: %v", err)
		}
	}
	if _, _, err := fish.AddCard(ws, fish.NewCard{
		Text: "zlin 把乌龙茶放在厨房第二个柜子里",
		T:    "2026-10-20",
	}); err != nil {
		t.Fatalf("seed card: %v", err)
	}
}

func TestServeHelloAndBeforeLLMInjection(t *testing.T) {
	ws := t.TempDir()
	seedCards(t, ws)
	h := newHarness(t, ws)

	hello := h.call(`{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"name":"fish","version":1,"modes":["llm"]}}`)
	var ok bool
	if err := json.Unmarshal(hello.Result["ok"], &ok); err != nil || !ok {
		t.Fatalf("hello 应该返回 ok=true：%v / %s", err, string(hello.Result["ok"]))
	}

	request := `{"jsonrpc":"2.0","id":2,"method":"hook.before_llm","params":` +
		`{"meta":{"SessionKey":"telegram:1396228334"},"model":"mimo-v2.6-flash",` +
		`"messages":[{"role":"system","content":"你是郝"},{"role":"user","content":"我想喝点乌龙茶"}],` +
		`"tools":[],"options":{"temperature":0.3}}}`
	reply := h.call(request)

	var action string
	if err := json.Unmarshal(reply.Result["action"], &action); err != nil {
		t.Fatalf("响应缺 action：%v", err)
	}
	if action != "modify" {
		t.Fatalf("有内容可注入时应该返回 modify，得到 %q", action)
	}

	var modifiedParams struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reply.Result["request"], &modifiedParams); err != nil {
		t.Fatalf("request 解析失败：%v", err)
	}
	if len(modifiedParams.Messages) != 2 {
		t.Fatalf("消息条数不该变：%d", len(modifiedParams.Messages))
	}
	if modifiedParams.Messages[0].Content != "你是郝" {
		t.Fatalf("system 消息不许被改：%q", modifiedParams.Messages[0].Content)
	}
	userContent := modifiedParams.Messages[1].Content
	if !strings.Contains(userContent, startMarker) || !strings.Contains(userContent, endMarker) {
		t.Fatalf("user 消息里没有注入段：%q", userContent)
	}
	if !strings.HasPrefix(userContent, "我想喝点乌龙茶") {
		t.Fatalf("原话要保留在注入段前面：%q", userContent)
	}
	if !strings.Contains(userContent, moodHeading) {
		t.Fatalf("应该带上心情段：%q", userContent)
	}
	if !strings.Contains(userContent, memoryHeading) {
		t.Fatalf("应该带上记忆段：%q", userContent)
	}
	if !strings.Contains(userContent, "乌龙茶") {
		t.Fatalf("应该召回到茶叶那张卡：%q", userContent)
	}

	// 幂等：把改过的请求原样再发一次，注入段不能叠加。
	second := `{"jsonrpc":"2.0","id":3,"method":"hook.before_llm","params":` + string(reply.Result["request"]) + `}`
	reply2 := h.call(second)

	var again struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(reply2.Result["request"], &again); err != nil {
		t.Fatalf("第二次 request 解析失败：%v", err)
	}
	content := again.Messages[1].Content
	if got := strings.Count(content, startMarker); got != 1 {
		t.Fatalf("注入段应该只有一段，得到 %d 段：%q", got, content)
	}
	if got := strings.Count(content, endMarker); got != 1 {
		t.Fatalf("结束标记应该只有一个，得到 %d 个：%q", got, content)
	}
	if got := strings.Count(content, moodHeading); got != 1 {
		t.Fatalf("心情段应该只有一段，得到 %d 段：%q", got, content)
	}
	if got := strings.Count(content, memoryHeading); got != 1 {
		t.Fatalf("记忆段应该只有一段，得到 %d 段：%q", got, content)
	}

	// 他刚说过话，last_inbound 应该被记上。
	state, err := fish.LoadSpeakState(ws)
	if err != nil {
		t.Fatalf("读 state: %v", err)
	}
	if state.LastInbound == "" {
		t.Fatal("before_llm 应该记 last_inbound")
	}
}

func TestServeSkipsInboundForCronSession(t *testing.T) {
	ws := t.TempDir()
	seedCards(t, ws)

	old := time.Date(2026, 10, 20, 9, 0, 0, 0, time.Local)
	if err := fish.SaveSpeakState(ws, fish.SpeakState{LastInbound: old.Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}

	h := newHarness(t, ws)
	request := `{"jsonrpc":"2.0","id":5,"method":"hook.before_llm","params":` +
		`{"meta":{"SessionKey":"agent:cron-job123-abc"},"model":"m",` +
		`"messages":[{"role":"user","content":"该说点什么了"}]}}`
	reply := h.call(request)

	var action string
	if err := json.Unmarshal(reply.Result["action"], &action); err != nil || action != "modify" {
		t.Fatalf("cron 会话也应该正常注入（action=%q，err=%v）", action, err)
	}

	state, err := fish.LoadSpeakState(ws)
	if err != nil {
		t.Fatal(err)
	}
	if state.LastInbound != old.Format(time.RFC3339) {
		t.Fatalf("cron 触发的回合不该改 last_inbound：%q", state.LastInbound)
	}
}

func TestServeDegradesToContinue(t *testing.T) {
	ws := t.TempDir()
	h := newHarness(t, ws)

	// 没有 messages
	reply := h.call(`{"jsonrpc":"2.0","id":7,"method":"hook.before_llm","params":{"meta":{"SessionKey":"s"},"model":"m"}}`)
	var action string
	if err := json.Unmarshal(reply.Result["action"], &action); err != nil || action != "continue" {
		t.Fatalf("没有 messages 时应该 continue：%q / %v", action, err)
	}

	// 没有 user 消息
	reply = h.call(`{"jsonrpc":"2.0","id":8,"method":"hook.before_llm","params":{"messages":[{"role":"system","content":"sys"}]}}`)
	if err := json.Unmarshal(reply.Result["action"], &action); err != nil || action != "continue" {
		t.Fatalf("没有 user 消息时应该 continue：%q / %v", action, err)
	}

	// 空正文的 user 消息
	reply = h.call(`{"jsonrpc":"2.0","id":9,"method":"hook.before_llm","params":{"messages":[{"role":"user","content":"   "}]}}`)
	if err := json.Unmarshal(reply.Result["action"], &action); err != nil || action != "continue" {
		t.Fatalf("空正文时应该 continue：%q / %v", action, err)
	}

	// 别的阶段按放行/批准处理
	reply = h.call(`{"jsonrpc":"2.0","id":10,"method":"hook.approve_tool","params":{"tool":"bash"}}`)
	var approved bool
	if err := json.Unmarshal(reply.Result["approved"], &approved); err != nil || !approved {
		t.Fatalf("approve_tool 应该放行：%v / %v", approved, err)
	}

	reply = h.call(`{"jsonrpc":"2.0","id":11,"method":"hook.after_llm","params":{}}`)
	if err := json.Unmarshal(reply.Result["action"], &action); err != nil || action != "continue" {
		t.Fatalf("after_llm 应该 continue：%q / %v", action, err)
	}

	// notification 不回应，但进程要活着：下一行请求还能正常回。
	if _, err := h.in.Write([]byte(`{"jsonrpc":"2.0","method":"hook.runtime_event","params":{"kind":"agent.turn.start"}}` + "\n")); err != nil {
		t.Fatalf("写 notification 失败：%v", err)
	}
	reply = h.call(`{"jsonrpc":"2.0","id":12,"method":"hook.hello","params":{}}`)
	if len(reply.Result) == 0 {
		t.Fatal("notification 之后进程应该还能回请求")
	}
}

func TestStripFishBlock(t *testing.T) {
	raw := "问他早上好\n\n" + startMarker + "\n旧的心情段\n" + endMarker
	if got := stripFishBlock(raw); got != "问他早上好" {
		t.Fatalf("删除旧注入段失败：%q", got)
	}

	two := startMarker + "\nA\n" + endMarker + "\n中间的话\n" + startMarker + "\nB\n" + endMarker
	got := stripFishBlock(two)
	if strings.Contains(got, startMarker) || !strings.Contains(got, "中间的话") {
		t.Fatalf("多段都要删掉且保留正文：%q", got)
	}

	if got := stripFishBlock("没有标记的话"); got != "没有标记的话" {
		t.Fatalf("没有标记时不该动：%q", got)
	}
}

func TestMetaSessionKeyVariants(t *testing.T) {
	cases := map[string]string{
		`{"SessionKey":"a"}`:  "a",
		`{"session_key":"b"}`: "b",
		`{"sessionkey":"c"}`:  "c",
		`{"AgentID":"x"}`:     "",
		`null`:                "",
	}
	for raw, want := range cases {
		if got := metaSessionKey(json.RawMessage(raw)); got != want {
			t.Fatalf("metaSessionKey(%s) = %q，期望 %q", raw, got, want)
		}
	}
}
func TestServeSpeakTurnStripsToolsAndMarker(t *testing.T) {
	ws := t.TempDir()
	seedCards(t, ws)
	h := newHarness(t, ws)

	request := `{"jsonrpc":"2.0","id":9,"method":"hook.before_llm","params":` +
		`{"meta":{"SessionKey":"agent:cron-abc-123"},"model":"mimo-v2.6-flash",` +
		`"messages":[{"role":"user","content":"` + speakMarker + `该主动开口了"}],` +
		`"tools":[{"type":"function","function":{"name":"read_file"}}],` +
		`"options":{"temperature":0.3,"max_tokens":8192}}}`
	reply := h.call(request)

	var params struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
		Tools   []any          `json:"tools"`
		Options map[string]any `json:"options"`
	}
	if err := json.Unmarshal(reply.Result["request"], &params); err != nil {
		t.Fatalf("request 解析失败：%v", err)
	}
	if len(params.Tools) != 0 {
		t.Fatalf("主动搭话回合不该留工具：%v", params.Tools)
	}
	if got, ok := params.Options["max_tokens"].(float64); !ok || int(got) != speakMaxTokens {
		t.Fatalf("max_tokens 应该被压到 %d，得到 %v", speakMaxTokens, params.Options["max_tokens"])
	}
	if _, ok := params.Options["temperature"]; !ok {
		t.Fatalf("其它 options 不许丢：%v", params.Options)
	}
	if strings.Contains(params.Messages[0].Content, speakMarker) {
		t.Fatalf("标记不该留给模型：%q", params.Messages[0].Content)
	}
}
