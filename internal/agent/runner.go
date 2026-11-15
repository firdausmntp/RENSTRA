package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"renstra/internal/provider"
	"renstra/internal/store"
	"renstra/internal/tool"
)

const (
	defaultMaxIters = 40
	maxToolOutput   = 30 * 1024
)

type Runner struct {
	Provider  provider.Provider
	Tools     *tool.Registry
	Store     *store.Store
	Model     string
	Cwd       string
	MaxIters  int
	MaxTokens int
	Approve   func(ctx context.Context, name string, args json.RawMessage) (bool, error)
	Out       io.Writer

	cachedPrompt string
}

// Run executes one user turn: stream, run tools, repeat until the model
// answers without tool calls or the iteration budget runs out.
func (r *Runner) Run(ctx context.Context, chatID int64, userText string) (string, error) {
	history, err := r.Store.Messages(ctx, chatID)
	if err != nil {
		return "", err
	}
	user := provider.Message{Role: provider.RoleUser, Content: userText}
	if _, err := r.Store.AppendMessage(ctx, chatID, user); err != nil {
		return "", err
	}
	history = append(history, user)

	maxIters := r.MaxIters
	if maxIters <= 0 {
		maxIters = defaultMaxIters
	}

	for i := 0; i < maxIters; i++ {
		req := provider.Request{
			Model:     r.Model,
			Messages:  append([]provider.Message{{Role: provider.RoleSystem, Content: r.system()}}, history...),
			Tools:     r.Tools.Defs(),
			MaxTokens: r.MaxTokens,
		}

		ch, err := r.Provider.Stream(ctx, req)
		if err != nil {
			return "", err
		}

		var text strings.Builder
		var calls []provider.ToolCall
		for ev := range ch {
			if ev.Err != nil {
				return "", ev.Err
			}
			if ev.Text != "" {
				text.WriteString(ev.Text)
				if r.Out != nil {
					io.WriteString(r.Out, ev.Text)
				}
			}
			if len(ev.ToolCalls) > 0 {
				calls = ev.ToolCalls
			}
		}

		assistant := provider.Message{Role: provider.RoleAssistant, Content: text.String(), ToolCalls: calls}

		if ctx.Err() != nil {
			if _, err := r.Store.AppendMessage(context.Background(), chatID, assistant); err != nil {
				return text.String(), err
			}
			return text.String(), ctx.Err()
		}

		if _, err := r.Store.AppendMessage(ctx, chatID, assistant); err != nil {
			return "", err
		}
		history = append(history, assistant)

		if len(calls) == 0 {
			return text.String(), nil
		}

		for _, call := range calls {
			result := r.executeTool(ctx, call)
			msg := provider.Message{Role: provider.RoleTool, Content: result, ToolCallID: call.ID}
			if _, err := r.Store.AppendMessage(ctx, chatID, msg); err != nil {
				return "", err
			}
			history = append(history, msg)
		}
	}
	return "", fmt.Errorf("batas iterasi (%d) tercapai tanpa jawaban akhir", maxIters)
}

func (r *Runner) executeTool(ctx context.Context, call provider.ToolCall) string {
	t, ok := r.Tools.Get(call.Name)
	if !ok {
		return "tool tidak dikenal: " + call.Name
	}
	if !json.Valid([]byte(call.Arguments)) {
		return "argumen tool tidak valid: bukan JSON"
	}
	if t.Danger() && r.Approve != nil {
		allowed, err := r.Approve(ctx, call.Name, json.RawMessage(call.Arguments))
		if err != nil {
			return "approval gagal: " + err.Error()
		}
		if !allowed {
			return "ditolak user"
		}
	}

	out, err := t.Execute(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		return "error: " + err.Error()
	}
	truncated, terr := tool.Truncate(out, maxToolOutput, r.Cwd, call.ID)
	if terr != nil {
		return out
	}
	return truncated
}

// system is cached per Runner; memory arrives in Plan 3.
func (r *Runner) system() string {
	if r.cachedPrompt == "" {
		r.cachedPrompt = BuildPrompt(BuildParams{
			Cwd:   r.Cwd,
			Model: r.Model,
			Tools: r.Tools.Defs(),
		})
	}
	return r.cachedPrompt
}
