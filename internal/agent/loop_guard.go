package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type toolOutcome struct {
	name string
	hash string
}

// loopGuard detects exact tool repeats, alternating A/B cycles, stagnant
// identical tool outputs, and repetitive assistant narration across turns.
type loopGuard struct {
	recentCalls   []string
	recentResults []toolOutcome
	recentTexts   []string
	warnings      int
}

func normalizeSig(name, args string) string {
	return name + ":" + strings.Join(strings.Fields(strings.TrimSpace(args)), " ")
}

func normalizeReplyText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

// checkCall inspects an upcoming tool call for exact loops, 2-step alternating
// cycles (A->B->A->B->A->B), or excessive repetition in the recent window.
func (g *loopGuard) checkCall(name, args string) (warning string, hardStop bool) {
	sig := normalizeSig(name, args)
	g.recentCalls = append(g.recentCalls, sig)
	if len(g.recentCalls) > 12 {
		g.recentCalls = g.recentCalls[len(g.recentCalls)-12:]
	}

	n := len(g.recentCalls)
	reason := ""

	// 1. Exact consecutive 3x repeat (A -> A -> A).
	if n >= 3 && g.recentCalls[n-1] == g.recentCalls[n-2] && g.recentCalls[n-2] == g.recentCalls[n-3] {
		reason = fmt.Sprintf("tool %s dengan argumen identik sudah dipanggil 3 kali berturut-turut", name)
	}

	// 2. Alternating 2-step cycle (A -> B -> A -> B -> A -> B).
	if reason == "" && n >= 6 &&
		g.recentCalls[n-1] == g.recentCalls[n-3] && g.recentCalls[n-3] == g.recentCalls[n-5] &&
		g.recentCalls[n-2] == g.recentCalls[n-4] && g.recentCalls[n-4] == g.recentCalls[n-6] &&
		g.recentCalls[n-1] != g.recentCalls[n-2] {
		reason = fmt.Sprintf("siklus pengulangan berpola (A <-> B) terdeteksi pada tool %s", name)
	}

	// 3. Same call >= 4 times inside the last 8 calls.
	if reason == "" && n >= 4 {
		start := n - 8
		if start < 0 {
			start = 0
		}
		hits := 0
		for _, prev := range g.recentCalls[start:] {
			if prev == sig {
				hits++
			}
		}
		if hits >= 4 {
			reason = fmt.Sprintf("tool %s dengan argumen sama sudah diulang %d kali dalam 8 langkah terakhir", name, hits)
		}
	}

	if reason == "" {
		return "", false
	}

	g.warnings++
	if g.warnings >= 3 {
		return fmt.Sprintf("Eksekusi dihentikan otomatis karena loop berulang (%s). Ubah pendekatan atau periksa log/status terakhir.", reason), true
	}
	return fmt.Sprintf("[loop terdeteksi (%d/3): %s. HENTIKAN pengulangan ini sekarang — gunakan pendekatan lain atau segera berikan jawaban akhir ke user.]", g.warnings, reason), false
}

// recordResult checks whether the same tool keeps returning identical output
// across recent calls (typical of stuck polling or failing retries).
func (g *loopGuard) recordResult(name, result string) (warning string, hardStop bool) {
	norm := normalizeReplyText(result)
	if len(norm) < 12 {
		return "", false
	}
	sum := sha256.Sum256([]byte(norm))
	h := hex.EncodeToString(sum[:8])
	g.recentResults = append(g.recentResults, toolOutcome{name: name, hash: h})
	if len(g.recentResults) > 8 {
		g.recentResults = g.recentResults[len(g.recentResults)-8:]
	}

	same := 0
	for _, r := range g.recentResults {
		if r.name == name && r.hash == h {
			same++
		}
	}
	if same >= 4 {
		g.warnings++
		if g.warnings >= 3 {
			return fmt.Sprintf("Eksekusi dihentikan otomatis karena tool %s terus menghasilkan output identik tanpa progres.", name), true
		}
		return fmt.Sprintf("\n[PERINGATAN STAGNASI (%d/3): tool %s menghasilkan output yang sama %d kali terakhir tanpa perubahan. Jangan ulangi perintah yang sama; ubah strategi atau laporkan hasilnya ke user.]", g.warnings, name, same), false
	}
	return "", false
}

// checkAssistantText detects when the model keeps outputting nearly identical
// narration across consecutive iterations.
func (g *loopGuard) checkAssistantText(text string) (repeated bool, hardStop bool) {
	norm := normalizeReplyText(text)
	if len([]rune(norm)) < 24 {
		return false, false
	}
	g.recentTexts = append(g.recentTexts, norm)
	if len(g.recentTexts) > 5 {
		g.recentTexts = g.recentTexts[len(g.recentTexts)-5:]
	}
	n := len(g.recentTexts)
	if n >= 3 && textsNearEqual(g.recentTexts[n-1], g.recentTexts[n-2]) && textsNearEqual(g.recentTexts[n-2], g.recentTexts[n-3]) {
		g.warnings++
		return true, g.warnings >= 3
	}
	return false, false
}

func textsNearEqual(a, b string) bool {
	if a == b {
		return true
	}
	wa := strings.Fields(a)
	wb := strings.Fields(b)
	if len(wa) == 0 || len(wb) == 0 {
		return false
	}
	setA := make(map[string]struct{}, len(wa))
	for _, w := range wa {
		setA[w] = struct{}{}
	}
	setB := make(map[string]struct{}, len(wb))
	for _, w := range wb {
		setB[w] = struct{}{}
	}
	inter := 0
	for w := range setA {
		if _, ok := setB[w]; ok {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return false
	}
	return float64(inter)/float64(union) >= 0.82
}
