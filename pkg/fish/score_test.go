package fish

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

func mustAddCard(t *testing.T, ws string, in NewCard) Card {
	t.Helper()
	card, _, err := AddCard(ws, in)
	if err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	return card
}

func TestLoadCardsSkipsSupersededAndBadLines(t *testing.T) {
	ws := t.TempDir()
	if err := ensureDir(Dir(ws)); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"id":"m1","t":"2026-10-01","text":"有效卡片一","imp":0.7,"kind":"event"}`,
		``,
		`这不是 JSON`,
		`{"id":"m2","t":"2026-10-02","text":"被取代的卡片","imp":0.7,"kind":"event","superseded_by":"m3"}`,
		`{"id":"m3","t":"2026-10-03","text":"有效卡片二","imp":0.7,"kind":"fact"}`,
	}, "\n")
	if err := os.WriteFile(CardsPath(ws), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	cards, err := LoadCards(ws)
	if err != nil {
		t.Fatalf("LoadCards: %v", err)
	}
	if len(cards) != 2 {
		t.Fatalf("想要 2 张有效卡片，得到 %d：%+v", len(cards), cards)
	}
	if cards[0].ID != "m1" || cards[1].ID != "m3" {
		t.Fatalf("顺序或内容不对：%+v", cards)
	}
}

func TestAddCardKeySupersedesPrevious(t *testing.T) {
	ws := t.TempDir()

	first := mustAddCard(t, ws, NewCard{Text: "zlin 的常用模型是 A", T: "2026-10-01", Key: "zlin.模型", Kind: CardKindFact})
	second := mustAddCard(t, ws, NewCard{Text: "zlin 的常用模型是 B", T: "2026-10-02", Key: "zlin.模型", Kind: CardKindFact})

	if first.ID == second.ID {
		t.Fatal("两张卡 id 不该相同")
	}

	rows, err := loadRawCards(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("想要 2 行，得到 %d", len(rows))
	}
	if rows[0].SupersededBy == nil || *rows[0].SupersededBy != second.ID {
		t.Fatalf("旧卡没有被标取代：%+v", rows[0])
	}
	if rows[1].SupersededBy != nil {
		t.Fatalf("新卡不该被取代：%+v", rows[1])
	}

	cards, err := LoadCards(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].ID != second.ID {
		t.Fatalf("加载结果不对：%+v", cards)
	}

	// 第二次加同键卡时返回值要报「顶掉 1 张」。
	_, replaced, err := AddCard(ws, NewCard{Text: "zlin 的常用模型是 C", Key: "zlin.模型", Kind: CardKindFact})
	if err != nil {
		t.Fatal(err)
	}
	if replaced != 1 {
		t.Fatalf("想要顶掉 1 张，得到 %d", replaced)
	}
}

func TestAddCardWithoutKeyNeverSupersedes(t *testing.T) {
	ws := t.TempDir()
	mustAddCard(t, ws, NewCard{Text: "今天天气不错", T: "2026-10-01"})
	mustAddCard(t, ws, NewCard{Text: "今天天气不错", T: "2026-10-02"})

	cards, err := LoadCards(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("没有 key 时不该取代旧卡：%+v", cards)
	}
}

func TestAddCardRejectsEmptyText(t *testing.T) {
	ws := t.TempDir()
	if _, _, err := AddCard(ws, NewCard{Text: "   "}); err == nil {
		t.Fatal("空正文应该报错")
	}
}

func TestImportCardsIsIdempotent(t *testing.T) {
	ws := t.TempDir()
	memory := `# 记忆

## 关于 zlin
- 他偏爱自己拍板，讨厌被安排
- 短

§

2026-10-23 把 hao 迁到 OpenCode V2，七台机器都用这个二进制。

§

我们约定：绝不在对话里打印 token。
`
	path := ws + "/MEMORY.md"
	if err := os.WriteFile(path, []byte(memory), 0o644); err != nil {
		t.Fatal(err)
	}

	added, skipped, err := ImportCards(ws, path)
	if err != nil {
		t.Fatalf("ImportCards: %v", err)
	}
	// 两段 + 第一段里那条长 bullet + 一条有效 bullet（"短" 只有 3 个字符不计数）
	if added != 4 {
		t.Fatalf("想要新增 4 张，得到 %d（skipped=%d）", added, skipped)
	}

	added2, skipped2, err := ImportCards(ws, path)
	if err != nil {
		t.Fatalf("第二次 ImportCards: %v", err)
	}
	if added2 != 0 {
		t.Fatalf("重复导入不该新增，得到 %d", added2)
	}
	if skipped2 != 4 {
		t.Fatalf("重复导入应该全部跳过，得到 %d", skipped2)
	}

	cards, err := LoadCards(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 4 {
		t.Fatalf("想要 4 张卡，得到 %d", len(cards))
	}

	byText := make(map[string]Card, len(cards))
	for _, c := range cards {
		byText[c.Text] = c
	}

	bullet, ok := byText["他偏爱自己拍板，讨厌被安排"]
	if !ok {
		t.Fatalf("bullet 没成卡：%+v", cards)
	}
	if bullet.Kind != CardKindFact {
		t.Fatalf("含「要求/偏爱」类词的 bullet 应该是 fact，得到 %s", bullet.Kind)
	}
	if bullet.T != "" {
		t.Fatalf("没有日期时 t 应该是空串，得到 %q", bullet.T)
	}

	dated, ok := byText["2026-10-23 把 hao 迁到 OpenCode V2，七台机器都用这个二进制。"]
	if !ok {
		t.Fatalf("§ 段落没成卡：%+v", cards)
	}
	if dated.T != "2026-10-23" {
		t.Fatalf("日期应该从正文里抽出来，得到 %q", dated.T)
	}
	if dated.Kind != CardKindEvent {
		t.Fatalf("这条应该是 event，得到 %s", dated.Kind)
	}

	rule, ok := byText["我们约定：绝不在对话里打印 token。"]
	if !ok {
		t.Fatalf("约定段没成卡：%+v", cards)
	}
	if rule.Kind != CardKindFact {
		t.Fatalf("含「绝不/约定」应该是 fact，得到 %s", rule.Kind)
	}
}

func TestCardIDStable(t *testing.T) {
	a := CardID("同一段文字", "2026-10-25")
	b := CardID("同一段文字", "2026-10-25")
	if a != b {
		t.Fatalf("同样输入应该同样 id：%s vs %s", a, b)
	}
	if !strings.HasPrefix(a, "m") || len(a) != 11 {
		t.Fatalf("id 应该是 m + 10 位十六进制，得到 %s", a)
	}
	if CardID("同一段文字", "2026-10-26") == a {
		t.Fatal("日期不同 id 应该不同")
	}
}

func TestRecallWritesLedgerAndSkipsWhenDisabled(t *testing.T) {
	ws := t.TempDir()
	for i := 0; i < 8; i++ {
		mustAddCard(t, ws, NewCard{Text: "无关的旧事第 " + string(rune('一'+i)) + " 条记录", T: "2026-10-01"})
	}
	mustAddCard(t, ws, NewCard{Text: "zlin 把乌龙茶放在厨房第二个柜子里", T: "2026-10-20"})

	out, err := RecallWorkspace(ws, "乌龙茶放哪", "ses_test", 6, false)
	if err != nil {
		t.Fatalf("RecallWorkspace: %v", err)
	}
	if out.Block == "" {
		t.Fatal("应该召回到茶叶那张卡")
	}
	if _, err := os.Stat(RecallLogPath(ws)); !os.IsNotExist(err) {
		t.Fatalf("no-log 时不该写账本：%v", err)
	}

	if _, err := RecallWorkspace(ws, "乌龙茶放哪", "ses_test", 6, true); err != nil {
		t.Fatalf("RecallWorkspace: %v", err)
	}
	raw, err := os.ReadFile(RecallLogPath(ws))
	if err != nil {
		t.Fatalf("读账本失败：%v", err)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.Split(strings.TrimSpace(string(raw)), "\n")[0]), &entry); err != nil {
		t.Fatalf("账本不是合法 JSON：%v", err)
	}
	for _, key := range []string{"ts", "session", "query", "cards", "picked", "dropped"} {
		if _, ok := entry[key]; !ok {
			t.Fatalf("账本缺字段 %q：%v", key, entry)
		}
	}
	if entry["session"] != "ses_test" {
		t.Fatalf("session 记错了：%v", entry["session"])
	}
}

func TestRecallQueryTruncatedInLedger(t *testing.T) {
	ws := t.TempDir()
	long := strings.Repeat("茶", 300)
	out, err := RecallWorkspace(ws, long, "", 6, true)
	if err != nil {
		t.Fatal(err)
	}
	if out.Block != "" {
		t.Fatal("没有卡片时不该有召回块")
	}
	raw, err := os.ReadFile(RecallLogPath(ws))
	if err != nil {
		t.Fatal(err)
	}
	var entry recallEntry
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &entry); err != nil {
		t.Fatal(err)
	}
	if len([]rune(entry.Query)) != RecallQueryLogRunes {
		t.Fatalf("query 应该截到 %d 字，得到 %d", RecallQueryLogRunes, len([]rune(entry.Query)))
	}
}

func TestFreshnessRules(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)

	fact := Card{Kind: CardKindFact, T: "2020-01-01", Half: 30}
	if got := freshness(fact, now); got != 1.0 {
		t.Fatalf("fact 不该淡忘，得到 %v", got)
	}
	pref := Card{Kind: CardKindPref, T: "2020-01-01", Half: 30}
	if got := freshness(pref, now); got != 1.0 {
		t.Fatalf("pref 不该淡忘，得到 %v", got)
	}

	fresh := Card{Kind: CardKindEvent, T: "2026-10-25T12:00:00", Half: 30}
	if got := freshness(fresh, now); math.Abs(got-1.0) > 1e-9 {
		t.Fatalf("今天的 event 新鲜度应该是 1，得到 %v", got)
	}

	half := Card{Kind: CardKindEvent, T: "2026-09-25T12:00:00", Half: 30}
	if got := freshness(half, now); math.Abs(got-0.5) > 1e-6 {
		t.Fatalf("一个半衰期后应该是 0.5，得到 %v", got)
	}

	noDate := Card{Kind: CardKindEvent, T: "", Half: 30}
	if got := freshness(noDate, now); got != 0.6 {
		t.Fatalf("日期解析失败应该是 0.6，得到 %v", got)
	}
}

func TestScoreMatchesDocumentedFormula(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cards := make([]Card, 0, 12)
	for i := 0; i < 10; i++ {
		cards = append(cards, Card{
			ID:   "noise" + string(rune('a'+i)),
			Text: "跟查询无关的第 " + string(rune('一'+i)) + " 条噪音",
			Kind: CardKindEvent,
			T:    "2026-10-20",
			Imp:  0.7,
		})
	}
	target := Card{ID: "target", Text: "乌龙茶放在厨房第二个柜子", Kind: CardKindEvent, T: "2026-10-25T12:00:00", Imp: 0.7}
	cards = append(cards, target)

	scored := scoreCards(cards, "乌龙茶放哪", now)
	if len(scored) != 1 {
		t.Fatalf("只该有 1 张卡命中，得到 %d", len(scored))
	}

	// 手工算一遍：N=11，查询三组双字里「乌龙」「龙茶」「茶放」命中 3 个，df 都是 1。
	n := float64(len(cards))
	idf := math.Log(1 + n/(1+1))
	raw := idf * float64(len(scored[0].hit))
	want := raw * (0.6 + 0.4*1.0) * (0.7 + 0.3*0.7)
	if math.Abs(scored[0].score-want) > 1e-9 {
		t.Fatalf("分数不符：得到 %.6f，期望 %.6f（hit=%v）", scored[0].score, want, scored[0].hit)
	}
	if len(scored[0].hit) > 6 {
		t.Fatalf("hit 最多记 6 个词，得到 %d", len(scored[0].hit))
	}
}

func TestScoreTieKeepsBothAndOrderIsStable(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cards := []Card{
		{ID: "a", Text: "周末去爬山看了日出", Kind: CardKindEvent, T: "2026-10-25", Imp: 0.7},
		{ID: "b", Text: "周末去爬山看了日落", Kind: CardKindEvent, T: "2026-10-25", Imp: 0.7},
	}
	for i := 0; i < 10; i++ {
		cards = append(cards, Card{
			ID:   "noise" + string(rune('a'+i)),
			Text: "无关噪音 " + string(rune('一'+i)),
			Kind: CardKindEvent,
			T:    "2026-10-20",
			Imp:  0.7,
		})
	}

	scored := scoreCards(cards, "周末爬山", now)
	if len(scored) != 2 {
		t.Fatalf("同分的两张都该保留，得到 %d", len(scored))
	}
	if scored[0].score != scored[1].score {
		t.Fatalf("两张卡应该同分：%v vs %v", scored[0].score, scored[1].score)
	}
	if scored[0].card.ID != "a" || scored[1].card.ID != "b" {
		t.Fatalf("同分时应该保持原顺序，得到 %s, %s", scored[0].card.ID, scored[1].card.ID)
	}
}

func TestScoreNoHitReturnsNothing(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cards := []Card{{ID: "a", Text: "乌龙茶放在厨房", Kind: CardKindEvent, T: "2026-10-25", Imp: 0.7}}
	if scored := scoreCards(cards, "quantum physics", now); len(scored) != 0 {
		t.Fatalf("没有命中词时不该返回卡片：%+v", scored)
	}
	if block := renderPicked(scoreCards(cards, "", now), RenderMaxRunes); block != "" {
		t.Fatalf("空查询不该渲染出内容：%q", block)
	}
}

func TestScoreThresholdFiltersWeakMatches(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	// 只有一张卡、命中一个词：raw≈0.69，乘完权重远低于 2.0 的绝对门槛。
	cards := []Card{{ID: "a", Text: "乌龙茶", Kind: CardKindEvent, T: "2026-10-25", Imp: 0.7}}
	if scored := scoreCards(cards, "乌龙茶", now); len(scored) != 0 {
		t.Fatalf("低于绝对门槛的应该被丢掉：%+v", scored)
	}
}

func TestRenderTruncatesLongBodyAndTotalLength(t *testing.T) {
	long := strings.Repeat("茶", 300)
	card := Card{ID: "a", Text: long, T: "2026-10-25"}
	block := renderPicked([]scoredCard{{card: card, score: 9}}, RenderMaxRunes)
	if !strings.HasSuffix(block, "…") {
		t.Fatalf("超长正文应该以 … 结尾：%q", block)
	}
	body := strings.TrimPrefix(block, "- [2026-10-25] ")
	if len([]rune(body)) != RenderBodyRunes+1 {
		t.Fatalf("正文应该截到 %d 字再加 …，得到 %d", RenderBodyRunes, len([]rune(body)))
	}

	// 塞满很多卡片，总长不能超过 800。
	picked := make([]scoredCard, 0, 20)
	for i := 0; i < 20; i++ {
		picked = append(picked, scoredCard{card: Card{
			ID:   "x" + string(rune('a'+i)),
			Text: strings.Repeat("茶", 200),
			T:    "2026-10-25",
		}})
	}
	block = renderPicked(picked, RenderMaxRunes)
	lines := strings.Split(block, "\n")
	if len(lines) >= 20 {
		t.Fatalf("总长上限没起作用，渲染了 %d 行", len(lines))
	}
	if len([]rune(block)) > RenderMaxRunes {
		t.Fatalf("渲染块超长：%d", len([]rune(block)))
	}
}

func TestRenderEmptyDateAndLimit(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	cards := []Card{
		{ID: "a", Text: "无日期的一条事", Kind: CardKindEvent, T: "", Imp: 0.7},
	}
	block := renderPicked([]scoredCard{{card: cards[0], score: 5}}, RenderMaxRunes)
	if !strings.HasPrefix(block, "- [无日期] ") {
		t.Fatalf("没有日期时应该写「无日期」：%q", block)
	}
	if got := len(scoreCards(cards, "日期", now)); got != 0 {
		t.Fatalf("「日期」不在正文里，不该命中，得到 %d", got)
	}
}
