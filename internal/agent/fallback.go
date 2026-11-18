package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"renstra/internal/provider"
)

// FallbackTarget is a secondary provider used when the primary fails.
type FallbackTarget struct {
	Provider      provider.Provider
	Model         string
	MaxTokens     int
	PriceIn       float64
	PriceOut      float64
	ContextWindow int
	Vision        bool
	ToolsDisabled bool
}

type streamResult struct {
	text      string
	calls     []provider.ToolCall
	usage     *provider.Usage
	streamErr error
	priceIn   float64
	priceOut  float64
}

// streamWithFallback tries the primary provider, and on immediate error or
// zero-output failure, tries each fallback target in sequence.
func (r *Runner) streamWithFallback(ctx context.Context, sysPrompt string, history []provider.Message) (streamResult, error) {
	primary := r.primaryTarget()
	candidates := make([]FallbackTarget, 0, 1+len(r.Fallbacks))
	candidates = append(candidates, primary)
	candidates = append(candidates, r.Fallbacks...)

	sw := streamWriterFrom(ctx)
	var lastErr error
	for i, target := range candidates {
		if ctx.Err() != nil {
			return streamResult{streamErr: ctx.Err()}, nil
		}

		maxTok := target.MaxTokens
		if maxTok <= 0 {
			maxTok = primary.MaxTokens
		}

		var toolDefs []provider.ToolDef
		effectivePrompt := sysPrompt
		if r.Tools != nil {
			toolDefs = r.Tools.Defs()
		}
		promptedMode := target.ToolsDisabled && len(toolDefs) > 0
		if promptedMode {
			effectivePrompt += buildPromptedToolsSection(toolDefs)
			toolDefs = nil
		}

		req := provider.Request{
			Model:     target.Model,
			Messages:  append([]provider.Message{{Role: provider.RoleSystem, Content: effectivePrompt}}, history...),
			Tools:     toolDefs,
			MaxTokens: maxTok,
		}

		if i > 0 && r.Logger != nil {
			r.Logger.Warn("provider_fallback",
				"provider", target.Provider.Name(),
				"model", target.Model,
				"attempt", i+1,
				"prev_error", lastErr,
			)
		}

		ch, err := target.Provider.Stream(ctx, req)
		if err != nil {
			lastErr = fmt.Errorf("provider %s (%s): %w", target.Provider.Name(), target.Model, err)
			continue
		}

		var text strings.Builder
		var calls []provider.ToolCall
		var streamErr error
		var usage *provider.Usage
		hasAnyOutput := false

		for ev := range ch {
			if ev.Err != nil && streamErr == nil {
				streamErr = ev.Err
			}
			if ev.Usage != nil {
				usage = ev.Usage
			}
			if ev.Text != "" {
				hasAnyOutput = true
				text.WriteString(ev.Text)
				if !promptedMode {
					if r.Out != nil {
						io.WriteString(r.Out, ev.Text)
					}
					if sw != nil {
						io.WriteString(sw, ev.Text)
					}
				}
			}
			if len(ev.ToolCalls) > 0 {
				hasAnyOutput = true
				calls = ev.ToolCalls
			}
		}

		if ctx.Err() != nil {
			return streamResult{text: text.String(), streamErr: ctx.Err()}, nil
		}

		// If the stream failed completely without emitting any output and we have
		// another fallback to try, proceed to the next fallback.
		if streamErr != nil && !hasAnyOutput && i < len(candidates)-1 {
			lastErr = fmt.Errorf("provider %s (%s): %w", target.Provider.Name(), target.Model, streamErr)
			continue
		}

		finalText := text.String()
		if promptedMode && len(calls) == 0 && streamErr == nil {
			if parsed, ok := parsePromptedToolCall(finalText); ok {
				calls = []provider.ToolCall{parsed}
				finalText = ""
			} else if finalText != "" {
				if r.Out != nil {
					io.WriteString(r.Out, finalText)
				}
				if sw != nil {
					io.WriteString(sw, finalText)
				}
			}
		}

		return streamResult{
			text:      finalText,
			calls:     calls,
			usage:     usage,
			streamErr: streamErr,
			priceIn:   target.PriceIn,
			priceOut:  target.PriceOut,
		}, nil
	}

	return streamResult{}, lastErr
}

func buildPromptedToolsSection(defs []provider.ToolDef) string {
	var b strings.Builder
	b.WriteString("\n\nmode prompted-tools: jika perlu memanggil tool, balas HANYA dengan satu blok JSON persis seperti:\n")
	b.WriteString("{\"tool_call\":{\"name\":\"nama_tool\",\"arguments\":{...}}}\n")
	b.WriteString("daftar schema tool:\n")
	for _, d := range defs {
		fmt.Fprintf(&b, "- %s: %s (schema: %s)\n", d.Name, d.Description, string(d.Schema))
	}
	return b.String()
}

func parsePromptedToolCall(raw string) (provider.ToolCall, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return provider.ToolCall{}, false
	}
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s, "\n"); idx >= 0 {
			s = s[idx+1:]
		}
		if end := strings.LastIndex(s, "```"); end >= 0 {
			s = s[:end]
		}
		s = strings.TrimSpace(s)
	}
	var wrap struct {
		ToolCall *struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_call"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal([]byte(s), &wrap); err != nil {
		return provider.ToolCall{}, false
	}
	name := wrap.Name
	args := wrap.Arguments
	if wrap.ToolCall != nil {
		name = wrap.ToolCall.Name
		args = wrap.ToolCall.Arguments
	}
	if strings.TrimSpace(name) == "" {
		return provider.ToolCall{}, false
	}
	argStr := "{}"
	if len(args) > 0 && string(args) != "null" {
		argStr = string(args)
	}
	return provider.ToolCall{ID: "prompted_1", Name: name, Arguments: argStr}, true
}
