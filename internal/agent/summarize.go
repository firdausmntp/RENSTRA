package agent

import (
	"context"
	"fmt"
	"strings"

	"renstra/internal/provider"
)

// compactThreshold is the fraction of the context window that triggers a
// compaction. Below it the transcript is sent verbatim.
const compactThreshold = 0.7

// keepTurns is how many trailing messages stay verbatim after compaction.
const keepTurns = 6

// summarize asks the auxiliary model for a summary of the messages that are
// about to be compacted. When no auxiliary model is configured, or the call
// fails, a deterministic fallback is used so the agent never silently forgets
// everything that happened before the cut.
func (r *Runner) summarize(ctx context.Context, msgs []provider.Message) string {
	if len(msgs) == 0 {
		return ""
	}

	primary := r.primaryTarget()
	fallback := fallbackSummary(msgs)

	prov := r.AuxProvider
	priceIn, priceOut := r.AuxPriceIn, r.AuxPriceOut
	if prov == nil {
		prov = primary.Provider
		priceIn, priceOut = primary.PriceIn, primary.PriceOut
	}
	if prov == nil {
		return fallback
	}

	model := r.AuxModel
	if model == "" {
		model = primary.Model
	}

	req := provider.Request{
		Model: model,
		Messages: []provider.Message{
			{Role: provider.RoleUser, Content: buildSummaryPrompt(msgs)},
		},
		MaxTokens: 800,
	}

	ch, err := prov.Stream(ctx, req)
	if err != nil {
		return fallback
	}

	var b strings.Builder
	var usage *provider.Usage
	for ev := range ch {
		if ev.Err != nil {
			return fallback
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
		b.WriteString(ev.Text)
	}
	if usage != nil {
		r.recordUsageWithPrices(usage, model, priceIn, priceOut)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return fallback
	}
	return "Ringkasan percakapan sebelumnya:\n" + out
}

// fallbackSummary keeps the skeleton of the dropped messages: who said what and
// which tools ran. It is cheap and deterministic, and better than losing the
// whole session.
func fallbackSummary(msgs []provider.Message) string {
	var b strings.Builder
	b.WriteString("Ringkasan percakapan sebelumnya (ringkas otomatis):\n")

	users := 0
	var tools []string
	for _, m := range msgs {
		switch m.Role {
		case provider.RoleUser:
			users++
			if users <= 5 {
				fmt.Fprintf(&b, "- user: %s\n", clip(m.Content, 200))
			}
		case provider.RoleAssistant:
			for _, c := range m.ToolCalls {
				tools = append(tools, c.Name)
			}
			if t := strings.TrimSpace(m.Content); t != "" {
				fmt.Fprintf(&b, "- asisten: %s\n", clip(t, 200))
			}
		}
	}
	if users > 5 {
		fmt.Fprintf(&b, "- (%d pesan user lainnya)\n", users-5)
	}
	if len(tools) > 0 {
		if len(tools) > 10 {
			tools = tools[:10]
		}
		fmt.Fprintf(&b, "- tool yang dipakai: %s\n", strings.Join(tools, ", "))
	}
	return b.String()
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}
