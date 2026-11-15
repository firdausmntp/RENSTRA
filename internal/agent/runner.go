package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"renstra/internal/provider"
	"renstra/internal/store"
	"renstra/internal/tool"
)

const (
	defaultMaxIters = 40
	maxToolOutput   = 30 * 1024
	saveTimeout     = 3 * time.Second
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

	mu           sync.Mutex
	cachedPrompt string
}

// Run executes one user turn.
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
		var streamErr error

		for ev := range ch {
			if ev.Err != nil && streamErr == nil {
				streamErr = ev.Err
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

		// A turn cancelled mid-stream may still have partial text on the board.
		// That partial text must be saved so the conversation history reflects
		// what the user actually saw, but we always return the cancel error.
		if ctx.Err() != nil {
			r.savePartial(chatID, text.String())
			return text.String(), ctx.Err()
		}
		if streamErr != nil {
			if text.Len() > 0 {
				r.savePartial(chatID, text.String())
			}
			return text.String(), streamErr
		}

		assistant := provider.Message{Role: provider.RoleAssistant, Content: text.String(), ToolCalls: calls}
		if _, err := r.Store.AppendMessage(ctx, chatID, assistant); err != nil {
			return "", err
		}
		history = append(history, assistant)

		if len(calls) == 0 {
			return text.String(), nil
		}

		// Execute tools. If the context is cancelled during execution, every
		// tool call that was not run must still get a synthetic tool result
		// so the assistant(tool_calls) message is never left unpaired.
		for idx, call := range calls {
			if ctx.Err() != nil {
				r.fillCancelledTools(chatID, calls[idx:])
				return text.String(), ctx.Err()
			}
			result := r.executeTool(ctx, call)
			msg := provider.Message{Role: provider.RoleTool, Content: result, ToolCallID: call.ID}
			if _, err := r.appendWithFallback(ctx, chatID, msg); err != nil {
				return "", err
			}
			history = append(history, msg)
		}
	}
	return "", fmt.Errorf("batas iterasi (%d) tercapai tanpa jawaban akhir", maxIters)
}

func (r *Runner) executeTool(ctx context.Context, call provider.ToolCall) (res string) {
	defer func() {
		if rec := recover(); rec != nil {
			res = fmt.Sprintf("panic di tool %s: %v", call.Name, rec)
		}
	}()

	t, ok := r.Tools.Get(call.Name)
	if !ok {
		return "tool tidak dikenal: " + call.Name
	}
	if !json.Valid([]byte(call.Arguments)) {
		return "argumen tool tidak valid: bukan JSON"
	}
	if t.Danger() {
		if r.Approve == nil {
			return "ditolak: perintah berbahaya butuh persetujuan (tidak ada approver)"
		}
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

func (r *Runner) savePartial(chatID int64, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	_, _ = r.Store.AppendMessage(ctx, chatID, provider.Message{Role: provider.RoleAssistant, Content: text})
}

func (r *Runner) fillCancelledTools(chatID int64, remaining []provider.ToolCall) {
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	for _, call := range remaining {
		_, _ = r.Store.AppendMessage(ctx, chatID, provider.Message{
			Role:       provider.RoleTool,
			Content:    "dibatalkan sebelum dijalankan",
			ToolCallID: call.ID,
		})
	}
}

func (r *Runner) appendWithFallback(ctx context.Context, chatID int64, m provider.Message) (int64, error) {
	id, err := r.Store.AppendMessage(ctx, chatID, m)
	if err != nil && errors.Is(ctx.Err(), context.Canceled) {
		saveCtx, cancel := context.WithTimeout(context.Background(), saveTimeout)
		defer cancel()
		return r.Store.AppendMessage(saveCtx, chatID, m)
	}
	return id, err
}

func (r *Runner) system() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cachedPrompt == "" {
		r.cachedPrompt = BuildPrompt(BuildParams{
			Cwd:   r.Cwd,
			Model: r.Model,
			Tools: r.Tools.Defs(),
		})
	}
	return r.cachedPrompt
}
