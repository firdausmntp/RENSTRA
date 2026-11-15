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

	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/tool"

	"renstra/internal/config"
)

const (
	defaultMaxIters = 40
	maxToolOutput   = 30 * 1024
	saveTimeout     = 3 * time.Second
)

type Runner struct {
	Provider      provider.Provider
	Tools         *tool.Registry
	Store         *store.Store
	Model         string
	Cwd           string
	MaxIters      int
	MaxTokens     int
	Memory        *memory.Store
	Skills        *skill.Registry
	Approve       func(ctx context.Context, name string, args json.RawMessage) (bool, error)
	Out           io.Writer
	PriceIn       float64 // USD per 1M tokens
	PriceOut      float64 // USD per 1M tokens
	ContextWindow int     // Model context tokens; defaults to 32000
	AuxModel      string  // Model used to summarize during compaction; defaults to Model
	AutoCompact   bool    // Whether to auto-compact when nearing context window
	Threshold     float64 // Compaction trigger threshold; defaults to 0.7
	PromptPrefix  string  // Custom system prompt prefix
	ApprovalCfg   config.ApprovalConfig
	SubagentCall  func(ctx context.Context, task string) (string, error)

	mu           sync.Mutex
	cachedPrompt string
	chatLocks    map[int64]*sync.Mutex
}

// lockChat serializes turns per chat. The Telegram gateway already interrupts
// a running turn, but cron fires on its own schedule, so without this two
// writers could interleave one chat's history and break tool-call pairing.
func (r *Runner) lockChat(chatID int64) func() {
	r.mu.Lock()
	if r.chatLocks == nil {
		r.chatLocks = map[int64]*sync.Mutex{}
	}
	l := r.chatLocks[chatID]
	if l == nil {
		l = &sync.Mutex{}
		r.chatLocks[chatID] = l
	}
	r.mu.Unlock()

	l.Lock()
	return l.Unlock
}

// Run executes one user turn.
func (r *Runner) Run(ctx context.Context, chatID int64, userText string) (string, error) {
	unlock := r.lockChat(chatID)
	defer unlock()

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
		win := r.ContextWindow
		if win <= 0 {
			win = 32000
		}
		if ShouldCompact(history, win, r.threshold()) {
			summary := r.summarize(ctx, history)
			if compacted, err := r.Store.Compact(ctx, chatID, keepTurns, summary); err == nil && len(compacted) > 0 {
				history = compacted
			} else {
				history = CompactHistory(history, keepTurns, summary)
			}
		}

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
		var usage *provider.Usage

		for ev := range ch {
			if ev.Err != nil && streamErr == nil {
				streamErr = ev.Err
			}
			if ev.Usage != nil {
				usage = ev.Usage
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

		if usage != nil {
			r.recordUsage(usage)
		}

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
			toolCtx := tool.WithChatID(ctx, chatID)
			result := r.executeTool(toolCtx, call)
			msg := provider.Message{Role: provider.RoleTool, Content: result, ToolCallID: call.ID}
			if _, err := r.appendWithFallback(ctx, chatID, msg); err != nil {
				return "", err
			}
			history = append(history, msg)
		}
	}
	return "", fmt.Errorf("batas iterasi (%d) tercapai tanpa jawaban akhir", maxIters)
}

func (r *Runner) threshold() float64 {
	if r.Threshold <= 0 {
		return compactThreshold
	}
	return r.Threshold
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
		// 1. Check declarative approval rules from config first.
		if decided, allowed := r.ApprovalCfg.Check(call.Name, []byte(call.Arguments)); decided {
			if !allowed {
				return "ditolak: aturan approval konfigurasi menolak perintah ini"
			}
			// Pre-approved by config rule (e.g. mode: "auto" or always_allowed)
		} else {
			// 2. Fall back to interactive prompt (Telegram inline keyboard / CLI y/n)
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
	}

	out, err := t.Execute(ctx, json.RawMessage(call.Arguments))
	if err != nil {
		return "error: " + err.Error()
	}
	// Memory and skill writes change the system prompt, so the frozen snapshot
	// is dropped only when a tool actually mutated it.
	if call.Name == "memory" || call.Name == "skill" {
		r.invalidatePrompt()
	}
	truncated, terr := tool.Truncate(out, maxToolOutput, r.Cwd, call.ID)
	if terr != nil {
		return out
	}
	return truncated
}

// recordUsage accumulates token counts and estimated cost for today.
func (r *Runner) recordUsage(u *provider.Usage) {
	cost := (float64(u.InputTokens)/1e6)*r.PriceIn + (float64(u.OutputTokens)/1e6)*r.PriceOut
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	_ = r.Store.RecordUsage(ctx, time.Now().Format("2006-01-02"), int64(u.InputTokens), int64(u.OutputTokens), cost)
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
		params := BuildParams{
			Cwd:          r.Cwd,
			Model:        r.Model,
			Tools:        r.Tools.Defs(),
			PromptPrefix: r.PromptPrefix,
		}
		if r.Memory != nil {
			snap := r.Memory.Snapshot()
			params.Memory = snap.Memory
			params.UserProfile = snap.User
		}
		if r.Skills != nil {
			params.SkillIndex = r.Skills.Index()
		}
		r.cachedPrompt = BuildPrompt(params)
	}
	return r.cachedPrompt
}

func (r *Runner) InvalidatePrompt() {
	r.invalidatePrompt()
}

func (r *Runner) invalidatePrompt() {
	r.mu.Lock()
	r.cachedPrompt = ""
	r.mu.Unlock()
}

// RunSubagent fulfills tool.SubagentRunner. It creates an isolated child
// session with its own history and budget, running under the same provider.
func (r *Runner) RunSubagent(ctx context.Context, task string) (string, error) {
	if r.SubagentCall != nil {
		return r.SubagentCall(ctx, task)
	}
	child := &Runner{
		Provider:      r.Provider,
		Tools:         r.Tools,
		Store:         r.Store,
		Model:         r.Model,
		AuxModel:      r.AuxModel,
		Cwd:           r.Cwd,
		MaxIters:      15,
		MaxTokens:     r.MaxTokens,
		PriceIn:       r.PriceIn,
		PriceOut:      r.PriceOut,
		ContextWindow: r.ContextWindow,
		Memory:        r.Memory,
		Skills:        r.Skills,
		Approve:       r.Approve,
		Out:           io.Discard,
	}
	// Synthetic high chat_id to keep subagent history separate from the main chat.
	subID := time.Now().UnixNano()
	return child.Run(ctx, subID, task)
}
