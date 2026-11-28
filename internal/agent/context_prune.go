package agent

import (
	"fmt"
	"strings"

	"renstra/internal/provider"
)

const (
	keepRecentToolResults = 4
	maskToolMinRunes      = 420
	maskToolPreviewRunes  = 240
)

// CanCompact reports whether CompactHistory / Store.Compact has a valid
// RoleUser cut point (> 0) so we avoid wasting an auxiliary LLM summarize call
// on single-turn loops where no cut is possible.
func CanCompact(msgs []provider.Message, keep int) bool {
	if len(msgs) <= keep {
		return false
	}
	cut := len(msgs) - keep
	for cut > 0 && msgs[cut].Role != provider.RoleUser {
		cut--
	}
	return cut > 0
}

// PrepareRequestHistory returns a provider-ready view of history that keeps
// token usage bounded during long single-turn tool loops without mutating the
// persisted SQLite history or splitting assistant(tool_calls)/tool pairs.
func PrepareRequestHistory(sysPrompt string, tools []provider.ToolDef, history []provider.Message, window int, threshold float64) ([]provider.Message, int, int) {
	if len(history) == 0 {
		return history, 0, 0
	}

	out := make([]provider.Message, len(history))
	copy(out, history)

	// Pass 1: Observation masking on older verbose tool outputs.
	var toolIdxs []int
	for i, m := range out {
		if m.Role == provider.RoleTool {
			toolIdxs = append(toolIdxs, i)
		}
	}

	maskedCount := 0
	if len(toolIdxs) > keepRecentToolResults {
		cutoff := len(toolIdxs) - keepRecentToolResults
		for _, idx := range toolIdxs[:cutoff] {
			content := out[idx].Content
			runes := []rune(content)
			if len(runes) <= maskToolMinRunes {
				continue
			}
			preview := strings.TrimSpace(string(runes[:maskToolPreviewRunes]))
			suffix := fmt.Sprintf("\n...[hasil tool diringkas dari %d char untuk hemat konteks; jalankan ulang atau pakai session_search jika butuh detail]", len(runes))
			if strings.HasPrefix(strings.TrimSpace(content), "[untrusted]") {
				suffix += "\n[/untrusted]"
			}
			out[idx].Content = preview + suffix
			maskedCount++
		}
	}

	// Pass 2: If a single long turn STILL exceeds the compaction threshold and
	// standard RoleUser compaction cannot cut, fold older complete
	// (assistant + tool*) blocks in memory while keeping leading RoleSystem,
	// the initiating RoleUser, and the trailing assistant/tool tail intact.
	foldedCount := 0
	if window > 0 && threshold > 0 && ShouldCompactRequest(sysPrompt, tools, out, window, threshold) && !CanCompact(out, keepTurns) {
		var folded []provider.Message
		folded, foldedCount = foldInTurnHistory(out, keepTurns)
		if foldedCount > 0 {
			out = folded
		}
	}

	return out, maskedCount, foldedCount
}

// foldInTurnHistory collapses older complete assistant+tool groups within a
// single long turn while preserving the first RoleUser (required by Anthropic)
// and keeping every assistant(tool_calls) paired with all of its RoleTool replies.
func foldInTurnHistory(msgs []provider.Message, keep int) ([]provider.Message, int) {
	firstUser := -1
	for i, m := range msgs {
		if m.Role == provider.RoleUser {
			firstUser = i
			break
		}
	}
	if firstUser < 0 || len(msgs)-firstUser <= keep+2 {
		return msgs, 0
	}

	// Find a safe tail cut index that starts on a RoleAssistant message so we
	// never orphan a RoleTool message from its assistant(tool_calls).
	tailStart := len(msgs) - keep
	if tailStart <= firstUser+1 {
		return msgs, 0
	}
	for tailStart > firstUser+1 && msgs[tailStart].Role != provider.RoleAssistant {
		tailStart--
	}
	if tailStart <= firstUser+1 || msgs[tailStart].Role != provider.RoleAssistant {
		return msgs, 0
	}

	dropped := msgs[firstUser+1 : tailStart]
	if len(dropped) < 4 {
		return msgs, 0
	}

	summary := buildInTurnFoldSummary(dropped)
	res := make([]provider.Message, 0, firstUser+2+(len(msgs)-tailStart))
	res = append(res, msgs[:firstUser+1]...)
	res = append(res, provider.Message{
		Role:    provider.RoleSystem,
		Content: summary,
	})
	res = append(res, msgs[tailStart:]...)
	return res, len(dropped)
}

func buildInTurnFoldSummary(dropped []provider.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[Ringkasan %d langkah sebelumnya dalam turn ini (konteks diringkas otomatis):]\n", len(dropped))
	var toolSeq []string
	var lastNotes []string
	for _, m := range dropped {
		if m.Role != provider.RoleAssistant {
			continue
		}
		for _, tc := range m.ToolCalls {
			toolSeq = append(toolSeq, tc.Name)
		}
		if note := strings.TrimSpace(m.Content); note != "" {
			lastNotes = append(lastNotes, clip(note, 140))
		}
	}
	if len(toolSeq) > 0 {
		if len(toolSeq) > 12 {
			toolSeq = append([]string{"..."}, toolSeq[len(toolSeq)-12:]...)
		}
		fmt.Fprintf(&b, "- Tool yang sudah dijalankan: %s\n", strings.Join(toolSeq, " -> "))
	}
	if len(lastNotes) > 0 {
		if len(lastNotes) > 4 {
			lastNotes = lastNotes[len(lastNotes)-4:]
		}
		for _, n := range lastNotes {
			fmt.Fprintf(&b, "- Catatan: %s\n", n)
		}
	}
	b.WriteString("- Jangan ulangi langkah yang sudah selesai di atas; lanjutkan langsung ke langkah berikutnya atau rangkum hasil akhir.")
	return b.String()
}
