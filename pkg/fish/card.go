package fish

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// 卡片类型。
const (
	CardKindEvent = "event"
	CardKindFact  = "fact"
	CardKindPref  = "pref"
)

// 卡片默认值。
const (
	DefaultCardImp    = 0.7
	DefaultCardHalf   = 30.0
	DefaultCardSource = "manual"
)

// Card 是一张记忆卡片，对应 cards.jsonl 的一行。
type Card struct {
	ID           string   `json:"id"`
	T            string   `json:"t"`
	Text         string   `json:"text"`
	Tags         []string `json:"tags"`
	Imp          float64  `json:"imp"`
	Kind         string   `json:"kind"`
	Half         float64  `json:"half"`
	Key          *string  `json:"key"`
	Src          string   `json:"src"`
	SupersededBy *string  `json:"superseded_by"`
}

// Superseded 表示这张卡已经被同键的新卡取代。
func (c Card) Superseded() bool {
	return c.SupersededBy != nil && *c.SupersededBy != ""
}

// NewCard 是加卡时的入参。
type NewCard struct {
	Text string
	T    string
	Tags []string
	Imp  float64
	Kind string
	Half float64
	Key  string
	Src  string
}

var (
	dateInText = regexp.MustCompile(`20\d\d-\d\d-\d\d`)

	// factHints 出现这些词更像「偏好/约定」，而不是会过期的事件。
	factHints = []string{"永远", "绝不", "约定", "称呼", "要求", "偏爱", "禁止", "一律", "必须"}
)

// NormalizeText 把正文压成单行：所有空白折叠成一个空格。
func NormalizeText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// CardID 按 sha1(text+t) 生成卡片 id（前缀 m，取前 10 位十六进制）。
func CardID(text, t string) string {
	sum := sha1.Sum([]byte(text + t))
	return "m" + hex.EncodeToString(sum[:])[:10]
}

// LoadCards 读取所有有效卡片：跳过被取代的行、空行和坏行。
func LoadCards(workspace string) ([]Card, error) {
	rows, err := loadRawCards(workspace)
	if err != nil {
		return nil, err
	}
	cards := make([]Card, 0, len(rows))
	for _, c := range rows {
		if c.Superseded() || strings.TrimSpace(c.Text) == "" {
			continue
		}
		cards = append(cards, c)
	}
	return cards, nil
}

// ListCards 按加入顺序倒序返回前 n 张有效卡片（n<=0 表示全部）。
func ListCards(workspace string, n int) ([]Card, error) {
	cards, err := LoadCards(workspace)
	if err != nil {
		return nil, err
	}
	out := make([]Card, 0, len(cards))
	for i := len(cards) - 1; i >= 0; i-- {
		out = append(out, cards[i])
		if n > 0 && len(out) >= n {
			break
		}
	}
	return out, nil
}

// loadRawCards 读全部行（含已被取代的），供改写与去重用。
func loadRawCards(workspace string) ([]Card, error) {
	rows := make([]Card, 0, 64)
	err := scanJSONLines(CardsPath(workspace), func(line []byte) error {
		var c Card
		if err := json.Unmarshal(line, &c); err != nil {
			return err
		}
		rows = append(rows, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// AddCard 追加一张卡片；带 key 时把同 key 且未取代的旧卡标成 superseded_by。
// 返回新卡和被顶掉的旧卡数量。
func AddCard(workspace string, in NewCard) (Card, int, error) {
	text := NormalizeText(in.Text)
	if text == "" {
		return Card{}, 0, fmt.Errorf("卡片正文不能为空")
	}

	t := strings.TrimSpace(in.T)
	if t == "" {
		t = time.Now().Format("2006-01-02")
	}
	kind := strings.TrimSpace(in.Kind)
	if kind == "" {
		kind = CardKindEvent
	}
	imp := in.Imp
	if imp <= 0 {
		imp = DefaultCardImp
	}
	half := in.Half
	if half <= 0 {
		half = DefaultCardHalf
	}
	src := strings.TrimSpace(in.Src)
	if src == "" {
		src = DefaultCardSource
	}

	tags := make([]string, 0, len(in.Tags))
	for _, tag := range in.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		tags = append(tags, tag)
	}

	id := CardID(text, t)
	key := strings.TrimSpace(in.Key)

	rows, err := loadRawCards(workspace)
	if err != nil {
		return Card{}, 0, err
	}

	replaced := 0
	if key != "" {
		for i := range rows {
			if rows[i].Key == nil || *rows[i].Key != key || rows[i].Superseded() {
				continue
			}
			sid := id
			rows[i].SupersededBy = &sid
			replaced++
		}
	}

	card := Card{
		ID:   id,
		T:    t,
		Text: text,
		Tags: tags,
		Imp:  imp,
		Kind: kind,
		Half: half,
		Src:  src,
	}
	if key != "" {
		card.Key = &key
	}

	rows = append(rows, card)

	values := make([]any, 0, len(rows))
	for _, row := range rows {
		values = append(values, row)
	}
	if err := writeJSONLines(CardsPath(workspace), values); err != nil {
		return Card{}, 0, err
	}
	return card, replaced, nil
}

// ImportCards 从 MEMORY.md 切卡：先按 `§` 单独一行的位置分段，
// 再把 `- ` 开头、长度大于 10 的 bullet 各当一张卡。已存在的 id 跳过（幂等）。
func ImportCards(workspace, memoryFile string) (added, skipped int, err error) {
	raw, err := os.ReadFile(memoryFile)
	if err != nil {
		return 0, 0, err
	}

	candidates := splitMemoryCards(string(raw))

	seen := make(map[string]struct{}, 128)
	rows, err := loadRawCards(workspace)
	if err != nil {
		return 0, 0, err
	}
	for _, row := range rows {
		seen[row.ID] = struct{}{}
	}

	existing := make([]any, 0, len(rows)+len(candidates))
	for _, row := range rows {
		existing = append(existing, row)
	}

	for _, text := range candidates {
		text = NormalizeText(text)
		if text == "" {
			skipped++
			continue
		}
		t := ""
		if m := dateInText.FindString(text); m != "" {
			t = m
		}
		id := CardID(text, t)
		if _, ok := seen[id]; ok {
			skipped++
			continue
		}
		seen[id] = struct{}{}

		existing = append(existing, Card{
			ID:   id,
			T:    t,
			Text: text,
			Tags: []string{},
			Imp:  DefaultCardImp,
			Kind: classifyCard(text),
			Half: DefaultCardHalf,
			Src:  "MEMORY.md",
		})
		added++
	}

	if added == 0 {
		return 0, skipped, nil
	}
	if err := writeJSONLines(CardsPath(workspace), existing); err != nil {
		return 0, 0, err
	}
	return added, skipped, nil
}

// splitMemoryCards 按 § 分段并收集 bullet 卡片。
func splitMemoryCards(raw string) []string {
	var out []string
	var cur []string

	flush := func() {
		if len(cur) == 0 {
			return
		}
		block := strings.Join(cur, "\n")

		// 一段里 bullet 够多就拆成一条条，别再把整段当一张大卡（否则一份
		// markdown 记忆会被反复记成同一坨，还跟 bullet 卡重复）。
		var bullets []string
		for _, line := range cur {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "- ") && utf8.RuneCountInString(trimmed) > 10 {
				bullets = append(bullets, trimmed[2:])
			}
		}
		if len(bullets) >= 2 {
			out = append(out, bullets...)
		} else {
			out = append(out, block)
		}
		cur = cur[:0]
	}

	for _, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "§" {
			flush()
			continue
		}
		cur = append(cur, line)
	}
	flush()

	return out
}

// classifyCard 判断一段正文更像事实还是事件。
func classifyCard(text string) string {
	for _, hint := range factHints {
		if strings.Contains(text, hint) {
			return CardKindFact
		}
	}
	return CardKindEvent
}
