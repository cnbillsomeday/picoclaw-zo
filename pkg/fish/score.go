package fish

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// QueryRuneLimit 是查询进入分词前截断的字符数。
	QueryRuneLimit = 400
	// DocRuneLimit 是卡片正文进入分词前截断的字符数。
	DocRuneLimit = 2000
	// QueryTermLimit 是参与打分的查询词上限（按 idf 取最高）。
	QueryTermLimit = 40
	// ScoreAbsMin 是绝对门槛；相对门槛是最高分的 55%。
	ScoreAbsMin = 2.0
	ScoreRelMin = 0.55
	// DefaultRecallLimit 是默认保留条数。
	DefaultRecallLimit = 6
	// RenderMaxRunes 是召回块的总长度上限。
	RenderMaxRunes = 800
	// RenderBodyRunes 是单张卡片正文的显示上限。
	RenderBodyRunes = 220
	// RecallQueryLogRunes 是账本里 query 保留的字符数。
	RecallQueryLogRunes = 200
	// RecallDroppedLogs 是账本里落选卡的记录条数。
	RecallDroppedLogs = 5
)

var wordPattern = regexp.MustCompile(`[a-zA-Z0-9_.-]{2,}`)

// Picked 是一张被召回（或落选）的卡片及其证据。
type Picked struct {
	ID        string   `json:"id"`
	T         string   `json:"t"`
	Score     float64  `json:"score"`
	Evidence  float64  `json:"evidence"`
	Freshness float64  `json:"freshness"`
	Imp       float64  `json:"imp"`
	Hit       []string `json:"hit"`
}

// Dropped 是落选的卡片，多带一个原因。
type Dropped struct {
	ID        string   `json:"id"`
	T         string   `json:"t"`
	Score     float64  `json:"score"`
	Evidence  float64  `json:"evidence"`
	Freshness float64  `json:"freshness"`
	Imp       float64  `json:"imp"`
	Hit       []string `json:"hit"`
	Reason    string   `json:"reason"`
}

// RecallOutput 是一次召回的结果。
type RecallOutput struct {
	Block   string
	Total   int
	Picked  []Picked
	Dropped []Dropped
}

// recallEntry 是 recall.log 的一行。
type recallEntry struct {
	TS      string    `json:"ts"`
	Session string    `json:"session"`
	Query   string    `json:"query"`
	Cards   int       `json:"cards"`
	Picked  []Picked  `json:"picked"`
	Dropped []Dropped `json:"dropped"`
}

type scoredCard struct {
	card      Card
	score     float64
	evidence  float64
	freshness float64
	imp       float64
	hit       []string
}

// RecallWorkspace 读取工作区的卡片并召回，需要时写一行账本。
func RecallWorkspace(workspace, query, session string, limit int, logIt bool) (RecallOutput, error) {
	cards, err := LoadCards(workspace)
	if err != nil {
		return RecallOutput{}, err
	}
	return RecallCards(workspace, cards, query, session, limit, logIt, time.Now())
}

// RecallCards 在已加载的卡片上做召回（便于单测注入时间）。
func RecallCards(
	workspace string,
	cards []Card,
	query, session string,
	limit int,
	logIt bool,
	now time.Time,
) (RecallOutput, error) {
	if limit <= 0 {
		limit = DefaultRecallLimit
	}
	if now.IsZero() {
		now = time.Now()
	}

	scored := scoreCards(cards, query, now)
	picked := scored
	if len(picked) > limit {
		picked = picked[:limit]
	}

	out := RecallOutput{Total: len(cards), Block: renderPicked(picked, RenderMaxRunes)}
	out.Picked = make([]Picked, 0, len(picked))
	for _, s := range picked {
		out.Picked = append(out.Picked, Picked{
			ID:        s.card.ID,
			T:         s.card.T,
			Score:     round3(s.score),
			Evidence:  round3(s.evidence),
			Freshness: round3(s.freshness),
			Imp:       s.imp,
			Hit:       s.hit,
		})
	}
	out.Dropped = make([]Dropped, 0, RecallDroppedLogs)
	for i := len(picked); i < len(scored) && len(out.Dropped) < RecallDroppedLogs; i++ {
		s := scored[i]
		out.Dropped = append(out.Dropped, Dropped{
			ID:        s.card.ID,
			T:         s.card.T,
			Score:     round3(s.score),
			Evidence:  round3(s.evidence),
			Freshness: round3(s.freshness),
			Imp:       s.imp,
			Hit:       s.hit,
			Reason:    "低于门槛或超额",
		})
	}

	if logIt {
		entry := recallEntry{
			TS:      now.Format(time.RFC3339),
			Session: session,
			Query:   truncateRunes(query, RecallQueryLogRunes),
			Cards:   len(cards),
			Picked:  out.Picked,
			Dropped: out.Dropped,
		}
		// 账本是诊断用的，写失败不该影响召回本身。
		_ = appendJSONLine(RecallLogPath(workspace), entry)
	}

	return out, nil
}

// scoreCards 按「idf 加权 × 新鲜度 × 重要度」打分并过滤。
func scoreCards(cards []Card, query string, now time.Time) []scoredCard {
	qTokens := tokens(query, QueryRuneLimit)
	if len(qTokens) == 0 || len(cards) == 0 {
		return nil
	}

	docTokens := make([]map[string]struct{}, len(cards))
	for i, c := range cards {
		set := make(map[string]struct{}, 64)
		for _, tk := range tokens(c.Text, DocRuneLimit) {
			set[tk] = struct{}{}
		}
		docTokens[i] = set
	}

	uniqueQ := make(map[string]struct{}, len(qTokens))
	for _, tk := range qTokens {
		uniqueQ[tk] = struct{}{}
	}

	df := make(map[string]int, len(uniqueQ))
	for tk := range uniqueQ {
		for _, set := range docTokens {
			if _, ok := set[tk]; ok {
				df[tk]++
			}
		}
	}

	n := float64(len(cards))
	idf := func(tk string) float64 {
		return math.Log(1 + n/(1+float64(df[tk])))
	}

	terms := make([]string, 0, len(uniqueQ))
	for tk := range uniqueQ {
		terms = append(terms, tk)
	}
	// 只留最有辨识度的一批查询词；同分按词排序，保证结果稳定。
	sort.Slice(terms, func(i, j int) bool {
		li, lj := idf(terms[i]), idf(terms[j])
		if li == lj {
			return terms[i] < terms[j]
		}
		return li > lj
	})
	if len(terms) > QueryTermLimit {
		terms = terms[:QueryTermLimit]
	}

	scored := make([]scoredCard, 0, len(cards))
	for i, c := range cards {
		hit := make([]string, 0, 6)
		total := 0.0
		hits := 0
		for _, tk := range terms {
			if _, ok := docTokens[i][tk]; !ok {
				continue
			}
			total += idf(tk)
			hits++
			if len(hit) < 6 {
				hit = append(hit, tk)
			}
		}
		if hits == 0 {
			continue
		}
		fresh := freshness(c, now)
		imp := c.Imp
		s := total * (0.6 + 0.4*fresh) * (0.7 + 0.3*imp)
		scored = append(scored, scoredCard{
			card:      c,
			score:     s,
			evidence:  total,
			freshness: fresh,
			imp:       imp,
			hit:       hit,
		})
	}
	if len(scored) == 0 {
		return nil
	}

	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	floor := math.Max(ScoreAbsMin, ScoreRelMin*scored[0].score)
	kept := make([]scoredCard, 0, len(scored))
	for _, s := range scored {
		if s.score >= floor {
			kept = append(kept, s)
		}
	}
	return kept
}

// freshness 长期事实不淡忘，事件按半衰期淡忘。
func freshness(c Card, now time.Time) float64 {
	if c.Kind == CardKindFact || c.Kind == CardKindPref {
		return 1.0
	}
	ts, ok := parseCardDate(c.T)
	if !ok {
		return 0.6
	}
	ageDays := now.Sub(ts).Hours() / 24
	if ageDays < 0 {
		ageDays = 0
	}
	half := c.Half
	if half <= 0 {
		half = DefaultCardHalf
	}
	return math.Pow(0.5, ageDays/half)
}

func parseCardDate(t string) (time.Time, bool) {
	t = strings.TrimSpace(t)
	if t == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, t); err == nil {
			return ts, true
		}
	}
	return time.Time{}, false
}

// renderPicked 渲染召回块。
func renderPicked(picked []scoredCard, maxRunes int) string {
	var b strings.Builder
	used := 0
	for _, s := range picked {
		stamp := strings.TrimSpace(s.card.T)
		if stamp == "" {
			stamp = "无日期"
		}
		body := truncateRunes(strings.Join(strings.Fields(s.card.Text), " "), RenderBodyRunes)
		if utf8.RuneCountInString(strings.TrimSpace(s.card.Text)) > RenderBodyRunes {
			body += "…"
		}
		line := fmt.Sprintf("- [%s] %s", stamp, body)
		width := utf8.RuneCountInString(line)
		if used+width > maxRunes {
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
			used++
		}
		b.WriteString(line)
		used += width
	}
	return b.String()
}

// tokens 分词：转小写、标点空白换成空格；中文取相邻双字，英文数字按词。
func tokens(text string, cap int) []string {
	normalized := normalizeForTokens(text)
	normalized = truncateRunes(normalized, cap)

	han := make([]rune, 0, len(normalized))
	for _, r := range normalized {
		if isHan(r) {
			han = append(han, r)
		}
	}

	out := make([]string, 0, len(han)+16)
	for i := 0; i+1 < len(han); i++ {
		out = append(out, string(han[i:i+2]))
	}
	out = append(out, wordPattern.FindAllString(normalized, -1)...)
	return out
}

func normalizeForTokens(text string) string {
	lowered := strings.ToLower(text)
	var b strings.Builder
	b.Grow(len(lowered))
	for _, r := range lowered {
		if isHan(r) || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	return b.String()
}

func isHan(r rune) bool {
	return r >= 0x4e00 && r <= 0x9fff
}

// truncateRunes 按字符截断。
func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit])
}

func round3(v float64) float64 {
	return math.Round(v*1000) / 1000
}
