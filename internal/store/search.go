package store

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"unicode"

	"renstra/internal/provider"
)

type scoredMsg struct {
	id    int64
	msg   provider.Message
	score float64
}

// SearchMessagesRanked performs hybrid BM25 + trigram/prefix semantic ranking
// across stored conversation history (including compacted turns).
// When allChats is true, it searches across every chat session in SQLite.
func (s *Store) SearchMessagesRanked(ctx context.Context, chatID int64, query string, limit int, allChats bool) ([]provider.Message, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("query pencarian kosong")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}

	var querySQL string
	var args []any
	if allChats || chatID == 0 {
		querySQL = `SELECT id, role, content FROM messages WHERE role IN ('user','assistant') ORDER BY id DESC LIMIT 2000`
	} else {
		querySQL = `SELECT id, role, content FROM messages WHERE chat_id = ? AND role IN ('user','assistant') ORDER BY id DESC LIMIT 2000`
		args = append(args, chatID)
	}

	rows, err := s.db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type cand struct {
		id     int64
		msg    provider.Message
		tokens []string
		tf     map[string]int
		grams  map[string]struct{}
	}
	var cands []cand
	for rows.Next() {
		var id int64
		var m provider.Message
		if err := rows.Scan(&id, &m.Role, &m.Content); err != nil {
			return nil, err
		}
		toks := tokenizeText(m.Content)
		tf := make(map[string]int, len(toks))
		for _, t := range toks {
			tf[t]++
		}
		cands = append(cands, cand{
			id:     id,
			msg:    m,
			tokens: toks,
			tf:     tf,
			grams:  charTrigrams(m.Content),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, nil
	}

	qLower := strings.ToLower(q)
	qTokens := tokenizeText(q)
	qGrams := charTrigrams(q)
	if len(qTokens) == 0 {
		// Fallback for punctuation-only queries like "%"
		var exact []provider.Message
		for _, c := range cands {
			if strings.Contains(strings.ToLower(c.msg.Content), qLower) {
				exact = append(exact, c.msg)
				if len(exact) >= limit {
					break
				}
			}
		}
		reverseMsgs(exact)
		return exact, nil
	}

	// Compute IDF & average document length for BM25
	N := float64(len(cands))
	var totalLen float64
	df := make(map[string]int, len(qTokens))
	for _, c := range cands {
		totalLen += float64(len(c.tokens))
		for _, qt := range qTokens {
			if c.tf[qt] > 0 {
				df[qt]++
			}
		}
	}
	avgDL := totalLen / N
	if avgDL < 1 {
		avgDL = 1
	}

	const k1 = 1.2
	const b = 0.75

	var scored []scoredMsg
	for _, c := range cands {
		var score float64
		if strings.Contains(strings.ToLower(c.msg.Content), qLower) {
			score += 4.0
		}
		dl := float64(len(c.tokens))
		for _, qt := range qTokens {
			freq := float64(c.tf[qt])
			if freq > 0 {
				idf := math.Log(1.0 + (N-float64(df[qt])+0.5)/(float64(df[qt])+0.5))
				tfNorm := (freq * (k1 + 1.0)) / (freq + k1*(1.0-b+b*(dl/avgDL)))
				score += idf * tfNorm
			} else if len(qt) >= 4 {
				// Prefix/morphological fallback (e.g. "menghapus" <-> "hapus")
				for dt := range c.tf {
					if len(dt) >= 4 && (strings.Contains(dt, qt) || strings.Contains(qt, dt)) {
						score += 0.45
						break
					}
				}
			}
		}
		if len(qGrams) > 0 && len(c.grams) > 0 {
			sim := trigramJaccard(qGrams, c.grams)
			if sim >= 0.08 {
				score += sim * 3.0
			}
		}
		if score > 0.25 {
			scored = append(scored, scoredMsg{id: c.id, msg: c.msg, score: score})
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		if math.Abs(scored[i].score-scored[j].score) > 1e-6 {
			return scored[i].score > scored[j].score
		}
		return scored[i].id < scored[j].id
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	// Present selected top hits chronologically (oldest first) for natural reading.
	sort.Slice(scored, func(i, j int) bool { return scored[i].id < scored[j].id })
	out := make([]provider.Message, 0, len(scored))
	for _, s := range scored {
		out = append(out, s.msg)
	}
	return out, nil
}

func tokenizeText(s string) []string {
	var toks []string
	var cur strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur.WriteRune(r)
		} else if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		toks = append(toks, cur.String())
	}
	return toks
}

func charTrigrams(s string) map[string]struct{} {
	norm := strings.Join(tokenizeText(s), " ")
	runes := []rune(norm)
	if len(runes) < 3 {
		return nil
	}
	out := make(map[string]struct{}, len(runes)-2)
	for i := 0; i <= len(runes)-3; i++ {
		out[string(runes[i:i+3])] = struct{}{}
	}
	return out
}

func trigramJaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	var inter int
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	if inter == 0 {
		return 0
	}
	union := len(a) + len(b) - inter
	return float64(inter) / float64(union)
}

func reverseMsgs(msgs []provider.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}
