package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/tool"
)

const (
	defaultMaxIters = 80
	maxToolOutput   = 30 * 1024
	saveTimeout     = 3 * time.Second
)

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
	fbHealth     *fallbackHealth
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
	origChatID := tool.ChatIDFrom(ctx)
	ctx = tool.WithDeliveryTracker(ctx)

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
	if chatID != 0 && chatID < 1_000_000_000_000_000 {
		turnID, _ = r.Store.StartTurn(ctx, chatID)
	}
	turnStarted := time.Now()
	var turnTokensIn, turnTokensOut, turnToolCalls int
	var turnCost float64
	var lastUsageIn int
	iterCount := 0
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
		if stats := turnStatsFrom(ctx); stats != nil {
			stats.TokensIn = turnTokensIn
			stats.TokensOut = turnTokensOut
			stats.ToolCalls = turnToolCalls
			stats.Iterations = iterCount
			stats.CostUSD = turnCost
			stats.Elapsed = time.Since(turnStarted)
			win := r.primaryTarget().ContextWindow
			if win <= 0 {
				win = 32000
			}
			stats.ContextWindow = win
			stats.ContextUsed = lastUsageIn
			if stats.ContextUsed == 0 {
				var defs []provider.ToolDef
				if r.Tools != nil {
					defs = r.Tools.Defs()
				}
				stats.ContextUsed = EstimateRequestTokens(r.system(), defs, history)
			}
		}
		if r.Logger != nil && chatID != 0 && chatID < 1_000_000_000_000_000 {
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
	var stepsLog []string
	activePhase := ""
	phaseStart := turnStarted

	var recalledFacts string
	if r.Store != nil {
		scope := fmt.Sprintf("chat:%d", chatID)
		if facts, ferr := r.Store.SearchMemoryFacts(ctx, scope, userText, 3); ferr == nil && len(facts) > 0 {
			var fb strings.Builder
			fb.WriteString("\n[Memori Relevan Otomatis (Smart Memory):\n")
			for _, f := range facts {
				fmt.Fprintf(&fb, "- #%d: %s\n", f.ID, f.Content)
			}
			fb.WriteString("]")
			recalledFacts = fb.String()
		}
	}

	for i := 0; i < maxIters; i++ {
		iterCount = i + 1
		if sw := streamWriterFrom(ctx); sw != nil {
			if stepper, ok := any(sw).(interface{ SetStep(iter, maxIter int) }); ok {
				stepper.SetStep(iterCount, maxIters)
			}
		}
		primary := r.primaryTarget()
		win := primary.ContextWindow
		if win <= 0 {
			win = 32000
		}
		sysPrompt := r.system()
		if recalledFacts != "" {
			sysPrompt += recalledFacts
		}
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
		if i >= maxIters-3 {
			sysPrompt += fmt.Sprintf("\n[PERINGATAN LONG-HORIZON: ini iterasi %d/%d. Segera selesaikan langkah saat ini dan tulis ringkasan final untuk user!]", iterCount, maxIters)
		} else if dl, ok := ctx.Deadline(); ok && time.Until(dl) < 90*time.Second {
			sysPrompt += fmt.Sprintf("\n[PERINGATAN WAKTU: sisa waktu turn %s. Segera rangkum hasil sejauh ini dan laporkan ke user!]", time.Until(dl).Truncate(time.Second))
		}

		activePhase = fmt.Sprintf("menunggu respons model %s (%s)", primary.Model, primary.Provider.Name())
		phaseStart = time.Now()
		res, err := r.streamWithFallback(ctx, sysPrompt, history)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) && (tool.WasChatDelivered(ctx, chatID) || tool.WasChatDelivered(ctx, origChatID)) {
				return "", nil
			}
			return "", wrapTimeoutErr(err, activePhase, phaseStart, iterCount, r.TurnTimeout)
		}

		text := res.text
		calls := res.calls
		streamErr := res.streamErr
		usage := res.usage

		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) && (tool.WasChatDelivered(ctx, chatID) || tool.WasChatDelivered(ctx, origChatID)) {
				r.savePartial(chatID, text)
				return "", nil
			}
			r.savePartial(chatID, text)
			return text, wrapTimeoutErr(ctx.Err(), activePhase, phaseStart, iterCount, r.TurnTimeout)
		}
		if streamErr != nil {
			if text != "" {
				r.savePartial(chatID, text)
			}
			return text, wrapTimeoutErr(streamErr, activePhase, phaseStart, iterCount, r.TurnTimeout)
		}

		if len(calls) == 0 && strings.TrimSpace(text) == "" {
			return "", errors.New("model mengembalikan respons kosong; silakan coba ulang atau ganti model")
		}

		assistant := provider.Message{Role: provider.RoleAssistant, Content: text, ToolCalls: calls}
		if _, err := r.Store.AppendMessage(ctx, chatID, assistant); err != nil {
			return "", err
		}
		history = append(history, assistant)

		if usage != nil {
			turnTokensIn += usage.InputTokens
			turnTokensOut += usage.OutputTokens
			lastUsageIn = usage.InputTokens
			if usage.InputTokens > win {
				r.SetContextWindow(autoScaleContextWindow(usage.InputTokens))
			}
			turnCost += (float64(usage.InputTokens)/1e6)*res.priceIn + (float64(usage.OutputTokens)/1e6)*res.priceOut
			r.recordUsageWithPrices(usage, res.model, res.priceIn, res.priceOut)
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

		for idx, call := range calls {
			if ctx.Err() != nil {
				r.fillCancelledTools(chatID, calls[idx:])
				if errors.Is(ctx.Err(), context.DeadlineExceeded) && (tool.WasChatDelivered(ctx, chatID) || tool.WasChatDelivered(ctx, origChatID)) {
					return "", nil
				}
				return text, wrapTimeoutErr(ctx.Err(), activePhase, phaseStart, iterCount, r.TurnTimeout)
			}
			stepsLog = append(stepsLog, call.Name)
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
				activePhase = describeToolPhase(call.Name, call.Arguments)
				phaseStart = time.Now()
				toolCtx := ctx
				if tool.ChatIDFrom(toolCtx) == 0 {
					toolCtx = tool.WithChatID(toolCtx, chatID)
				}
				result = r.executeTool(toolCtx, call)
				if ctx.Err() != nil {
					r.fillCancelledTools(chatID, calls[idx:])
					if errors.Is(ctx.Err(), context.DeadlineExceeded) && (tool.WasChatDelivered(ctx, chatID) || tool.WasChatDelivered(ctx, origChatID)) {
						return "", nil
					}
					return text, wrapTimeoutErr(ctx.Err(), activePhase, phaseStart, iterCount, r.TurnTimeout)
				}
			}
			msg := provider.Message{Role: provider.RoleTool, Content: result, ToolCallID: call.ID}
			if _, err := r.appendWithFallback(ctx, chatID, msg); err != nil {
				return "", err
			}
			history = append(history, msg)
		}
	}
	if tool.WasChatDelivered(ctx, chatID) || tool.WasChatDelivered(ctx, origChatID) {
		return "", nil
	}
	if len(stepsLog) > 0 {
		return "", fmt.Errorf("batas iterasi (%d) tercapai setelah %d tool (%s); ketik `lanjut` bila ingin meneruskan", maxIters, len(stepsLog), strings.Join(lastN(stepsLog, 5), ", "))
	}
	return "", fmt.Errorf("batas iterasi (%d) tercapai tanpa jawaban akhir", maxIters)
}

func lastN(items []string, n int) []string {
	if len(items) <= n {
		return items
	}
	return items[len(items)-n:]
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

func autoScaleContextWindow(inputTokens int) int {
	for _, tier := range []int{64000, 128000, 200000, 400000, 1000000, 2000000} {
		if inputTokens < tier {
			return tier
		}
	}
	return inputTokens * 2
}
