package agent

import (
	"strings"
	"unicode/utf8"

	"renstra/internal/provider"
)

// ContextWindow returns the model's context size, with a conservative default
// so compaction never waits for a provider error to trigger.
func ContextWindow(cfg ModelCaps) int {
	if cfg.Context > 0 {
		return cfg.Context
	}
	return 32000
}

// ModelCaps is the subset of config.ModelConfig the agent needs.
type ModelCaps struct {
	Context   int
	MaxOutput int
}

// EstimateTokens approximates token usage at 4 bytes per token, which is the
// rule of thumb for mixed English/Indonesian text. Provider usage, when
// present, always wins.
func EstimateTokens(msgs []provider.Message) int {
	bytes := 0
	for _, m := range msgs {
		bytes += len(m.Content)
		for _, c := range m.ToolCalls {
			bytes += len(c.Arguments) + len(c.Name)
		}
	}
	return bytes / 4
}

// ShouldCompact reports whether the history has crossed the threshold.
func ShouldCompact(msgs []provider.Message, window int, threshold float64) bool {
	if window <= 0 {
		return false
	}
	return float64(EstimateTokens(msgs)) > float64(window)*threshold
}

// EstimateRequestTokens estimates tokens across the system prompt, tool
// schemas/descriptions, and conversation history.
func EstimateRequestTokens(sysPrompt string, tools []provider.ToolDef, msgs []provider.Message) int {
	bytes := len(sysPrompt)
	for _, d := range tools {
		bytes += len(d.Name) + len(d.Description) + len(d.Schema)
	}
	return bytes/4 + EstimateTokens(msgs)
}

// ShouldCompactRequest reports whether the full request (system prompt +
// tool definitions + conversation history) has crossed the threshold.
func ShouldCompactRequest(sysPrompt string, tools []provider.ToolDef, msgs []provider.Message, window int, threshold float64) bool {
	if window <= 0 {
		return false
	}
	return float64(EstimateRequestTokens(sysPrompt, tools, msgs)) > float64(window)*threshold
}

// CompactHistory replaces everything except the last keep messages with one
// summary message. Tool-call groups are never split: the cut point walks back
// until it lands on a user message, so an assistant(tool_calls) is always
// accompanied by its tool results.
func CompactHistory(msgs []provider.Message, keep int, summary string) []provider.Message {
	if len(msgs) <= keep {
		return msgs
	}
	cut := len(msgs) - keep
	for cut > 0 && msgs[cut].Role != provider.RoleUser {
		cut--
	}
	if cut <= 0 {
		return msgs
	}

	out := make([]provider.Message, 0, keep+1)
	if strings.TrimSpace(summary) != "" {
		out = append(out, provider.Message{Role: provider.RoleSystem, Content: summary})
	}
	out = append(out, msgs[cut:]...)
	return out
}

// buildSummaryPrompt is the instruction given to the auxiliary model when
// compacting. Kept short: it is paid for on every compaction.
func buildSummaryPrompt(msgs []provider.Message) string {
	var b strings.Builder
	b.WriteString("ringkas percakapan berikut dalam bahasa Indonesia, maksimal 400 kata. ")
	b.WriteString("WAJIB pertahankan: aturan/larangan eksplisit dari user (\"jangan ...\", format wajib, pantangan), keputusan, nama file/path penting, perintah yang sudah dijalankan, dan hal yang belum selesai.\n\n")
	for _, m := range msgs {
		if m.Role == provider.RoleSystem {
			continue
		}
		content := m.Content
		if len(content) > 2000 {
			cut := 2000
			for cut > 0 && !utf8.RuneStart(content[cut]) {
				cut--
			}
			content = content[:cut] + "…"
		}
		b.WriteString(string(m.Role))
		b.WriteString(": ")
		b.WriteString(content)
		b.WriteString("\n")
	}
	return b.String()
}
