package agent

import (
	"context"
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

		req := provider.Request{
			Model:     target.Model,
			Messages:  append([]provider.Message{{Role: provider.RoleSystem, Content: sysPrompt}}, history...),
			Tools:     r.Tools.Defs(),
			MaxTokens: maxTok,
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
				if r.Out != nil {
					io.WriteString(r.Out, ev.Text)
				}
				if sw != nil {
					io.WriteString(sw, ev.Text)
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

		return streamResult{
			text:      text.String(),
			calls:     calls,
			usage:     usage,
			streamErr: streamErr,
			priceIn:   target.PriceIn,
			priceOut:  target.PriceOut,
		}, nil
	}

	return streamResult{}, lastErr
}
