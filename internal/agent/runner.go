package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

type streamWriterKey struct{}
type toolHookKey struct{}
type unattendedKey struct{}

// WithStreamWriter attaches a per-turn writer for streaming delta text.
func WithStreamWriter(ctx context.Context, w io.Writer) context.Context {
	return context.WithValue(ctx, streamWriterKey{}, w)
}

func streamWriterFrom(ctx context.Context) io.Writer {
	w, _ := ctx.Value(streamWriterKey{}).(io.Writer)
	return w
}

// WithToolHook attaches a per-turn callback invoked before a tool executes.
func WithToolHook(ctx context.Context, fn func(toolName, args string)) context.Context {
	return context.WithValue(ctx, toolHookKey{}, fn)
}

func toolHookFrom(ctx context.Context) func(string, string) {
	fn, _ := ctx.Value(toolHookKey{}).(func(string, string))
	return fn
}

// WithUnattended marks a context as unattended (e.g. cron job), causing
// dangerous tools that require interactive approval to be denied immediately.
func WithUnattended(ctx context.Context) context.Context {
	return context.WithValue(ctx, unattendedKey{}, true)
}

func isUnattended(ctx context.Context) bool {
	v, _ := ctx.Value(unattendedKey{}).(bool)
	return v
}

type Runner struct {
	Provider           provider.Provider
	Tools              *tool.Registry
	Store              *store.Store
	Model              string
	Cwd                string
	MaxIters           int
	MaxTokens          int
	Memory             *memory.Store
	Skills             *skill.Registry
	Approve            func(ctx context.Context, name string, args json.RawMessage) (bool, error)
	Out                io.Writer
	PriceIn            float64           // USD per 1M tokens
	PriceOut           float64           // USD per 1M tokens
	ContextWindow      int               // Model context tokens; defaults to 32000
	AuxModel           string            // Model used to summarize during compaction; defaults to Model
	AuxProvider        provider.Provider // Optional separate provider for AuxModel
	AuxPriceIn         float64           // USD per 1M input tokens for AuxModel
	AuxPriceOut        float64           // USD per 1M output tokens for AuxModel
	AutoCompact        bool              // Whether to auto-compact when nearing context window
	DisableAutoCompact bool              // Explicitly disables auto-compaction when true
	Threshold          float64           // Compaction trigger threshold; defaults to 0.7
	Vision             bool              // Whether the active model accepts image inputs
	ToolsDisabled      bool              // Whether native tool calling is unsupported (use prompted JSON tools)
	Depth              int               // Subagent nesting depth
	TurnTimeout        time.Duration
	Limits             config.Limits
	PromptPrefix       string // Custom system prompt prefix
	ApprovalCfg        config.ApprovalConfig
	Fallbacks          []FallbackTarget
	SubagentCall       func(ctx context.Context, task string) (string, error)
	Logger             *slog.Logger

	mu           sync.Mutex
	cachedPrompt string
	locks        *chatLockRegistry
}

type chatLock struct {
	mu   sync.Mutex
	refs int
}

type chatLockRegistry struct {
	mu    sync.Mutex
	locks map[int64]*chatLock
}

func newChatLockRegistry() *chatLockRegistry {
	return &chatLockRegistry{locks: map[int64]*chatLock{}}
}

func (reg *chatLockRegistry) acquire(chatID int64) func() {
	reg.mu.Lock()
	l := reg.locks[chatID]
	if l == nil {
		l = &chatLock{}
		reg.locks[chatID] = l
	}
	l.refs++
	reg.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		reg.mu.Lock()
		l.refs--
		if l.refs <= 0 && reg.locks[chatID] == l {
			delete(reg.locks, chatID)
		}
		reg.mu.Unlock()
	}
}

func (r *Runner) lockRegistry() *chatLockRegistry {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = newChatLockRegistry()
	}
	return r.locks
}

// Clone returns a shallow copy of Runner with isolated mutable model state
// while sharing stores, tools, fallbacks, logger, and chat locks.
func (r *Runner) Clone() *Runner {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = newChatLockRegistry()
	}
	return &Runner{
		Provider:           r.Provider,
		Tools:              r.Tools,
		Store:              r.Store,
		Model:              r.Model,
		Cwd:                r.Cwd,
		MaxIters:           r.MaxIters,
		MaxTokens:          r.MaxTokens,
		Memory:             r.Memory,
		Skills:             r.Skills,
		Approve:            r.Approve,
		Out:                r.Out,
		PriceIn:            r.PriceIn,
		PriceOut:           r.PriceOut,
		ContextWindow:      r.ContextWindow,
		AuxModel:           r.AuxModel,
		AuxProvider:        r.AuxProvider,
		AuxPriceIn:         r.AuxPriceIn,
		AuxPriceOut:        r.AuxPriceOut,
		AutoCompact:        r.AutoCompact,
		DisableAutoCompact: r.DisableAutoCompact,
		Threshold:          r.Threshold,
		Vision:             r.Vision,
		ToolsDisabled:      r.ToolsDisabled,
		Depth:              r.Depth,
		TurnTimeout:        r.TurnTimeout,
		Limits:             r.Limits,
		PromptPrefix:       r.PromptPrefix,
		ApprovalCfg:        r.ApprovalCfg,
		Fallbacks:          r.Fallbacks,
		SubagentCall:       r.SubagentCall,
		Logger:             r.Logger,
		locks:              r.locks,
	}
}

// SetModel updates the active model (and provider capabilities if prov is non-nil)
// safely across concurrent turns and clears the cached system prompt.
func (r *Runner) SetModel(prov provider.Provider, model string, maxTokens, contextWin int, priceIn, priceOut float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Model = model
	if prov != nil {
		r.Provider = prov
		r.MaxTokens = maxTokens
		r.ContextWindow = contextWin
		r.PriceIn = priceIn
		r.PriceOut = priceOut
	}
	r.cachedPrompt = ""
}

// SetCapabilities updates vision and prompted-tools flags safely.
func (r *Runner) SetCapabilities(vision, toolsDisabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.Vision = vision
	r.ToolsDisabled = toolsDisabled
}

// GetModel returns the active model name safely.
func (r *Runner) GetModel() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Model
}

func (r *Runner) primaryTarget() FallbackTarget {
	r.mu.Lock()
	defer r.mu.Unlock()
	return FallbackTarget{
		Provider:      r.Provider,
		Model:         r.Model,
		MaxTokens:     r.MaxTokens,
		PriceIn:       r.PriceIn,
		PriceOut:      r.PriceOut,
		ContextWindow: r.ContextWindow,
		Vision:        r.Vision,
		ToolsDisabled: r.ToolsDisabled,
	}
}

// lockChat serializes turns per chat. The Telegram gateway already interrupts
// a running turn, but cron fires on its own schedule, so without this two
// writers could interleave one chat's history and break tool-call pairing.
func (r *Runner) lockChat(chatID int64) func() {
	return r.lockRegistry().acquire(chatID)
}

// Run executes one user turn.
func (r *Runner) Run(ctx context.Context, chatID int64, userText string) (resText string, resErr error) {
	unlock := r.lockChat(chatID)
	defer unlock()

	if r.TurnTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.TurnTimeout)
		defer cancel()
	}

	today := time.Now().Format("2006-01-02")
	if r.Limits.MaxDailyUSD > 0 || r.Limits.MaxDailyTokens > 0 {
		if u, err := r.Store.UsageForDay(ctx, today); err == nil {
			if r.Limits.MaxDailyUSD > 0 && u.CostUSD >= r.Limits.MaxDailyUSD {
				return "", fmt.Errorf("batas biaya harian tercapai ($%.4f / $%.2f)", u.CostUSD, r.Limits.MaxDailyUSD)
			}
			totalTok := u.TokensIn + u.TokensOut
			if r.Limits.MaxDailyTokens > 0 && totalTok >= r.Limits.MaxDailyTokens {
				return "", fmt.Errorf("batas token harian tercapai (%d / %d token)", totalTok, r.Limits.MaxDailyTokens)
			}
		}
	}

	history, err := r.Store.Messages(ctx, chatID)
	if err != nil {
		return "", err
	}
	user := provider.Message{Role: provider.RoleUser, Content: userText}
	if _, err := r.Store.AppendMessage(ctx, chatID, user); err != nil {
		return "", err
	}
	if r.primaryTarget().Vision {
		user.Attachments = extractImageAttachments(userText)
	}
	history = append(history, user)

	var turnID int64
	if chatID > 0 && chatID < 1_000_000_000_000_000 {
		turnID, _ = r.Store.StartTurn(ctx, chatID)
	}
	turnStarted := time.Now()
	var turnTokensIn, turnTokensOut, turnToolCalls int
	defer func() {
		status := "done"
		errMsg := ""
		if resErr != nil {
			errMsg = resErr.Error()
			if ctx.Err() != nil {
				status = "interrupted"
			} else {
				status = "error"
			}
		}
		if r.Logger != nil && chatID > 0 && chatID < 1_000_000_000_000_000 {
			r.Logger.Info("turn_finished",
				"chat_id", chatID,
				"model", r.GetModel(),
				"status", status,
				"duration_ms", time.Since(turnStarted).Milliseconds(),
				"tokens_in", turnTokensIn,
				"tokens_out", turnTokensOut,
				"tool_calls", turnToolCalls,
				"error", errMsg,
			)
		}
		if turnID <= 0 {
			return
		}
		fctx, fcancel := context.WithTimeout(context.Background(), saveTimeout)
		defer fcancel()
		_ = r.Store.FinishTurn(fctx, turnID, status, errMsg)
	}()

	userCount := 0
	for _, m := range history {
		if m.Role == provider.RoleUser {
			userCount++
		}
	}

	maxIters := r.MaxIters
	if maxIters <= 0 {
		maxIters = defaultMaxIters
	}

	var lastSig string
	var repeatCount int

	for i := 0; i < maxIters; i++ {
		primary := r.primaryTarget()
		win := primary.ContextWindow
		if win <= 0 {
			win = 32000
		}
		sysPrompt := r.system()
		var toolDefs []provider.ToolDef
		if r.Tools != nil {
			toolDefs = r.Tools.Defs()
		}
		if !r.DisableAutoCompact && ShouldCompactRequest(sysPrompt, toolDefs, history, win, r.threshold()) {
			summary := r.summarize(ctx, history)
			if compacted, err := r.Store.Compact(ctx, chatID, keepTurns, summary); err == nil && len(compacted) > 0 {
				history = compacted
			} else {
				history = CompactHistory(history, keepTurns, summary)
			}
		}

		if i == 0 && userCount > 0 && userCount%12 == 0 && r.Memory != nil {
			sysPrompt += "\n[pengingat sistem: jika ada fakta penting baru tentang user atau keputusan jangka panjang, perbarui tool memory; jika ada prosedur baru, simpan ke tool skill]"
		}

		res, err := r.streamWithFallback(ctx, sysPrompt, history)
		if err != nil {
			return "", err
		}

		text := res.text
		calls := res.calls
		streamErr := res.streamErr
		usage := res.usage

		// A turn cancelled mid-stream may still have partial text on the board.
		// That partial text must be saved so the conversation history reflects
		// what the user actually saw, but we always return the cancel error.
		if ctx.Err() != nil {
			r.savePartial(chatID, text)
			return text, ctx.Err()
		}
		if streamErr != nil {
			if text != "" {
				r.savePartial(chatID, text)
			}
			return text, streamErr
		}

		assistant := provider.Message{Role: provider.RoleAssistant, Content: text, ToolCalls: calls}
		if _, err := r.Store.AppendMessage(ctx, chatID, assistant); err != nil {
			return "", err
		}
		history = append(history, assistant)

		if usage != nil {
			turnTokensIn += usage.InputTokens
			turnTokensOut += usage.OutputTokens
			r.recordUsageWithPrices(usage, res.priceIn, res.priceOut)
		}
		turnToolCalls += len(calls)

		if len(calls) == 0 {
			if r.Limits.WarnDailyUSD > 0 {
				if u, uerr := r.Store.UsageForDay(ctx, today); uerr == nil && u.CostUSD >= r.Limits.WarnDailyUSD {
					text += fmt.Sprintf("\n\n⚠️ Peringatan biaya: penggunaan hari ini $%.4f telah melewati batas peringatan ($%.2f).", u.CostUSD, r.Limits.WarnDailyUSD)
				}
			}
			return text, nil
		}

		// Execute tools. If the context is cancelled during execution, every
		// tool call that was not run must still get a synthetic tool result
		// so the assistant(tool_calls) message is never left unpaired.
		for idx, call := range calls {
			if ctx.Err() != nil {
				r.fillCancelledTools(chatID, calls[idx:])
				return text, ctx.Err()
			}
			sig := call.Name + ":" + strings.TrimSpace(call.Arguments)
			if sig == lastSig {
				repeatCount++
			} else {
				lastSig = sig
				repeatCount = 1
			}
			var result string
			if repeatCount >= 3 {
				result = "[loop terdeteksi: tool " + call.Name + " dengan argumen identik sudah dipanggil 3 kali berturut-turut. Hentikan pengulangan dan berikan jawaban akhir atau gunakan cara lain.]"
			} else {
				toolCtx := ctx
				if tool.ChatIDFrom(toolCtx) == 0 {
					toolCtx = tool.WithChatID(toolCtx, chatID)
				}
				result = r.executeTool(toolCtx, call)
			}
			msg := provider.Message{Role: provider.RoleTool, Content: result, ToolCallID: call.ID}
			if _, err := r.appendWithFallback(ctx, chatID, msg); err != nil {
				return "", err
			}
			history = append(history, msg)
		}
	}
	return "", fmt.Errorf("batas iterasi (%d) tercapai tanpa jawaban akhir", maxIters)
}

// ForceCompact forces a history summarization and compaction for the given chatID.
func (r *Runner) ForceCompact(ctx context.Context, chatID int64) (string, error) {
	unlock := r.lockChat(chatID)
	defer unlock()

	history, err := r.Store.Messages(ctx, chatID)
	if err != nil {
		return "", err
	}
	if len(history) <= keepTurns {
		return "pesan belum cukup banyak untuk dikompaksi", nil
	}
	summary := r.summarize(ctx, history)
	if _, err := r.Store.Compact(ctx, chatID, keepTurns, summary); err != nil {
		return "", err
	}
	return summary, nil
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
	if hook := toolHookFrom(ctx); hook != nil {
		hook(call.Name, call.Arguments)
	}
	if isUnattended(ctx) && strings.HasPrefix(call.Name, "mcp__") {
		return "ditolak: tool MCP tidak diizinkan pada tugas otomatis (cron) tanpa pengawas"
	}
	if t.Danger() {
		chatID := tool.ChatIDFrom(ctx)
		// 1. Check declarative approval rules from config first.
		if decided, allowed := r.ApprovalCfg.Check(call.Name, []byte(call.Arguments)); decided {
			if r.Logger != nil {
				r.Logger.Info("tool_approval", "tool", call.Name, "chat_id", chatID, "source", "config", "allowed", allowed)
			}
			if !allowed {
				return "ditolak: aturan approval konfigurasi menolak perintah ini"
			}
			// Pre-approved by config rule (e.g. mode: "auto" or always_allowed)
		} else {
			if isUnattended(ctx) {
				if r.Logger != nil {
					r.Logger.Warn("tool_approval", "tool", call.Name, "chat_id", chatID, "source", "unattended", "allowed", false)
				}
				return "ditolak: perintah berbahaya tidak diizinkan pada tugas otomatis (cron) tanpa pengawas"
			}
			// 2. Fall back to interactive prompt (Telegram inline keyboard / CLI y/n)
			if r.Approve == nil {
				if r.Logger != nil {
					r.Logger.Warn("tool_approval", "tool", call.Name, "chat_id", chatID, "source", "no_approver", "allowed", false)
				}
				return "ditolak: perintah berbahaya butuh persetujuan (tidak ada approver)"
			}
			allowed, err := r.Approve(ctx, call.Name, json.RawMessage(call.Arguments))
			if r.Logger != nil {
				r.Logger.Info("tool_approval", "tool", call.Name, "chat_id", chatID, "source", "interactive", "allowed", allowed, "err", err)
			}
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
	r.recordUsageWithPrices(u, r.PriceIn, r.PriceOut)
}

// recordUsageWithPrices records usage using the price of the provider that
// actually answered, so a fallback's cheaper or pricier model is billed right.
func (r *Runner) recordUsageWithPrices(u *provider.Usage, priceIn, priceOut float64) {
	cost := (float64(u.InputTokens)/1e6)*priceIn + (float64(u.OutputTokens)/1e6)*priceOut
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
	if r.Skills != nil {
		_ = r.Skills.Reload()
	}
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
	primary := r.primaryTarget()
	childTools := r.Tools
	if r.Tools != nil {
		childTools = r.Tools.Clone()
	}
	child := &Runner{
		Provider:           primary.Provider,
		Tools:              childTools,
		Store:              r.Store,
		Model:              primary.Model,
		AuxModel:           r.AuxModel,
		AuxProvider:        r.AuxProvider,
		AuxPriceIn:         r.AuxPriceIn,
		AuxPriceOut:        r.AuxPriceOut,
		Cwd:                r.Cwd,
		MaxIters:           15,
		MaxTokens:          primary.MaxTokens,
		PriceIn:            primary.PriceIn,
		PriceOut:           primary.PriceOut,
		ContextWindow:      primary.ContextWindow,
		Vision:             primary.Vision,
		ToolsDisabled:      primary.ToolsDisabled,
		Depth:              r.Depth + 1,
		AutoCompact:        r.AutoCompact,
		DisableAutoCompact: r.DisableAutoCompact,
		Threshold:          r.Threshold,
		PromptPrefix:       r.PromptPrefix,
		ApprovalCfg:        r.ApprovalCfg,
		Fallbacks:          r.Fallbacks,
		Logger:             r.Logger,
		Memory:             r.Memory,
		Skills:             r.Skills,
		Approve:            r.Approve,
		Out:                io.Discard,
	}
	if childTools != nil {
		if _, ok := childTools.Get("delegate"); ok {
			childTools.Add(tool.NewDelegate(child, child.Depth))
		}
	}
	// Synthetic high chat_id to keep subagent history separate from the main chat.
	subID := time.Now().UnixNano()
	return child.Run(ctx, subID, task)
}

func extractImageAttachments(text string) []provider.Attachment {
	var out []provider.Attachment
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		const prefix = "[Foto diterima: "
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		end := strings.Index(rest, "]")
		if end <= 0 {
			continue
		}
		path := strings.TrimSpace(rest[:end])
		if _, err := os.Stat(path); err != nil {
			continue
		}
		mime := "image/jpeg"
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png":
			mime = "image/png"
		case ".webp":
			mime = "image/webp"
		case ".gif":
			mime = "image/gif"
		}
		out = append(out, provider.Attachment{Path: path, MimeType: mime})
	}
	return out
}
