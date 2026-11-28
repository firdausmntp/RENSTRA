package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"renstra/internal/provider"
)

const emptyResponseMaxRetries = 2

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
	model     string
	provider  string
}

// streamWithFallback uses smart capability + circuit-breaker routing across
// primary and fallback targets, retrying on error or empty response.
func (r *Runner) streamWithFallback(ctx context.Context, sysPrompt string, history []provider.Message) (streamResult, error) {
	primary := r.primaryTarget()
	health := r.healthRegistry()
	var allDefs []provider.ToolDef
	if r.Tools != nil {
		allDefs = r.Tools.Defs()
	}
	estTokens := EstimateRequestTokens(sysPrompt, allDefs, history)
	needVision := hasImageAttachments(history)
	candidates := rankSmartCandidates(primary, r.Fallbacks, health, needVision, estTokens)
	if len(candidates) == 0 {
		candidates = []FallbackTarget{primary}
	}

	sw := streamWriterFrom(ctx)
	var lastErr error
	minCtxWin := 0
	for i, target := range candidates {
		if ctx.Err() != nil {
			return streamResult{streamErr: ctx.Err()}, nil
		}
		if minCtxWin > 0 && target.ContextWindow > 0 && target.ContextWindow <= minCtxWin {
			continue
		}

		maxTok := target.MaxTokens
		if maxTok <= 0 {
			maxTok = primary.MaxTokens
		}

		var toolDefs []provider.ToolDef
		effectivePrompt := sysPrompt
		if len(allDefs) > 0 {
			toolDefs = allDefs
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

		if i > 0 {
			if resetter, ok := any(sw).(interface{ ResetStepText() }); ok {
				resetter.ResetStepText()
			}
			if r.Logger != nil {
				r.Logger.Warn("smart_fallback",
					"provider", target.Provider.Name(),
					"model", target.Model,
					"attempt", i+1,
					"prev_error", lastErr,
				)
			}
		}

		var (
			text         strings.Builder
			calls        []provider.ToolCall
			streamErr    error
			usage        *provider.Usage
			hasAnyOutput bool
			targetFailed bool
		)

		for attempt := 0; attempt <= emptyResponseMaxRetries; attempt++ {
			if ctx.Err() != nil {
				return streamResult{text: text.String(), streamErr: ctx.Err()}, nil
			}
			if attempt > 0 {
				if resetter, ok := any(sw).(interface{ ResetStepText() }); ok {
					resetter.ResetStepText()
				}
				if r.Logger != nil {
					r.Logger.Warn("retry_empty_response", "provider", target.Provider.Name(), "model", target.Model, "attempt", attempt+1)
				}
				select {
				case <-ctx.Done():
					return streamResult{text: text.String(), streamErr: ctx.Err()}, nil
				case <-time.After(time.Duration(attempt) * 750 * time.Millisecond):
				}
			}

			ch, err := target.Provider.Stream(ctx, req)
			if err != nil {
				kind := health.recordFailure(target, err)
				if kind == errKindContext && target.ContextWindow > minCtxWin {
					minCtxWin = target.ContextWindow
				}
				lastErr = fmt.Errorf("provider %s (%s): %w", target.Provider.Name(), target.Model, err)
				targetFailed = true
				break
			}

			text.Reset()
			calls = nil
			streamErr = nil
			usage = nil
			hasAnyOutput = false

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

			isEmpty := streamErr == nil && len(calls) == 0 && strings.TrimSpace(text.String()) == ""
			if isEmpty || (streamErr != nil && !hasAnyOutput && classifyFallbackErr(streamErr) == errKindTransient) {
				lastErr = fmt.Errorf("model %s (%s) mengembalikan respons kosong (percobaan %d/%d)", target.Model, target.Provider.Name(), attempt+1, emptyResponseMaxRetries+1)
				if streamErr != nil {
					lastErr = fmt.Errorf("provider %s (%s): %w", target.Provider.Name(), target.Model, streamErr)
				}
				continue
			}
			break
		}

		if targetFailed {
			continue
		}

		// Retry next fallback if stream failed without emitting output, OR if
		// stream ended cleanly with completely empty output after all retries.
		if streamErr != nil && !hasAnyOutput && i < len(candidates)-1 {
			kind := health.recordFailure(target, streamErr)
			if kind == errKindContext && target.ContextWindow > minCtxWin {
				minCtxWin = target.ContextWindow
			}
			lastErr = fmt.Errorf("provider %s (%s): %w", target.Provider.Name(), target.Model, streamErr)
			continue
		}

		finalText := text.String()
		if streamErr == nil && len(calls) == 0 && strings.TrimSpace(finalText) == "" {
			emptyErr := fmt.Errorf("model %s (%s) mengembalikan respons kosong setelah %d percobaan", target.Model, target.Provider.Name(), emptyResponseMaxRetries+1)
			health.recordFailure(target, emptyErr)
			lastErr = emptyErr
			if i < len(candidates)-1 {
				continue
			}
			return streamResult{}, emptyErr
		}

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

		if streamErr == nil && (len(calls) > 0 || strings.TrimSpace(finalText) != "") {
			health.recordSuccess(target)
		}

		return streamResult{
			text:      finalText,
			calls:     calls,
			usage:     usage,
			streamErr: streamErr,
			priceIn:   target.PriceIn,
			priceOut:  target.PriceOut,
			model:     target.Model,
			provider:  target.Provider.Name(),
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
