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
	"time"

	"renstra/internal/provider"
	"renstra/internal/tool"
)

func (r *Runner) approvalCheck(toolName string, rawArgs []byte) (bool, bool) {
	r.mu.Lock()
	cfg := r.ApprovalCfg
	r.mu.Unlock()
	return cfg.Check(toolName, rawArgs)
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
		argHint := summarizeArgForLog(call.Name, call.Arguments)
		if decided, allowed := r.approvalCheck(call.Name, []byte(call.Arguments)); decided {
			if r.Logger != nil {
				if allowed {
					r.Logger.Debug("tool_approval", "tool", call.Name, "arg", argHint, "chat_id", chatID, "source", "config", "allowed", true)
				} else {
					r.Logger.Warn("tool_approval", "tool", call.Name, "arg", argHint, "chat_id", chatID, "source", "config", "allowed", false)
				}
			}
			if !allowed {
				return "ditolak: aturan approval konfigurasi menolak perintah ini"
			}
		} else {
			if isUnattended(ctx) {
				if r.Logger != nil {
					r.Logger.Warn("tool_approval", "tool", call.Name, "arg", argHint, "chat_id", chatID, "source", "unattended", "allowed", false)
				}
				return "ditolak: perintah berbahaya tidak diizinkan pada tugas otomatis (cron) tanpa pengawas"
			}
			if r.Approve == nil {
				if r.Logger != nil {
					r.Logger.Warn("tool_approval", "tool", call.Name, "arg", argHint, "chat_id", chatID, "source", "no_approver", "allowed", false)
				}
				return "ditolak: perintah berbahaya butuh persetujuan (tidak ada approver)"
			}
			allowed, err := r.Approve(ctx, call.Name, json.RawMessage(call.Arguments))
			if r.Logger != nil {
				r.Logger.Info("tool_approval", "tool", call.Name, "arg", argHint, "chat_id", chatID, "source", "interactive", "allowed", allowed, "err", err)
			}
			if err != nil {
				return "approval gagal: " + err.Error()
			}
			if !allowed {
				return "ditolak user"
			}
		}
	}

	toolStart := time.Now()
	out, err := t.Execute(ctx, json.RawMessage(call.Arguments))
	toolDurMs := time.Since(toolStart).Milliseconds()
	if err != nil {
		if r.Logger != nil {
			r.Logger.Warn("tool_executed",
				"tool", call.Name,
				"arg", summarizeArgForLog(call.Name, call.Arguments),
				"chat_id", tool.ChatIDFrom(ctx),
				"duration_ms", toolDurMs,
				"ok", false,
				"error", err.Error(),
			)
		}
		return "error: " + err.Error()
	}
	if call.Name == "memory" || call.Name == "skill" {
		r.invalidatePrompt()
	}
	truncated, terr := tool.Truncate(out, maxToolOutput, r.Cwd, call.ID)
	finalOut := truncated
	if terr != nil {
		finalOut = out
	}
	if r.Logger != nil {
		lvl := slog.LevelInfo
		if strings.HasPrefix(finalOut, "[ditolak:") || strings.Contains(finalOut, "[timeout setelah") {
			lvl = slog.LevelWarn
		}
		r.Logger.Log(ctx, lvl, "tool_executed",
			"tool", call.Name,
			"arg", summarizeArgForLog(call.Name, call.Arguments),
			"chat_id", tool.ChatIDFrom(ctx),
			"duration_ms", toolDurMs,
			"ok", lvl == slog.LevelInfo,
			"output_bytes", len(out),
			"output_preview", previewForLog(finalOut, 180),
		)
	}
	return finalOut
}

func (r *Runner) recordUsage(u *provider.Usage) {
	r.recordUsageWithPrices(u, r.GetModel(), r.PriceIn, r.PriceOut)
}

func (r *Runner) recordUsageWithPrices(u *provider.Usage, model string, priceIn, priceOut float64) {
	cost := (float64(u.InputTokens)/1e6)*priceIn + (float64(u.OutputTokens)/1e6)*priceOut
	ctx, cancel := context.WithTimeout(context.Background(), saveTimeout)
	defer cancel()
	day := time.Now().Format("2006-01-02")
	_ = r.Store.RecordUsage(ctx, day, int64(u.InputTokens), int64(u.OutputTokens), cost)
	if model == "" {
		model = r.GetModel()
	}
	_ = r.Store.RecordModelUsage(ctx, day, model, int64(u.InputTokens), int64(u.OutputTokens), cost)
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
	var memStamp int64
	if r.Memory != nil {
		memStamp = r.Memory.ModStamp()
	}
	if r.cachedPrompt == "" || (r.Memory != nil && memStamp != r.cachedMemStamp) {
		params := BuildParams{
			Cwd:          r.Cwd,
			Model:        r.Model,
			Tools:        r.Tools.Defs(),
			PromptPrefix: r.PromptPrefix,
		}
		if r.Memory != nil {
			snap := r.Memory.Snapshot()
			params.Soul = snap.Soul
			params.Memory = snap.Memory
			params.UserProfile = snap.User
			r.cachedMemStamp = memStamp
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

// RunSubagent fulfills tool.SubagentRunner.
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
	subID := time.Now().UnixNano()
	return child.Run(ctx, subID, task)
}

func extractImageAttachments(text string) []provider.Attachment {
	var out []provider.Attachment
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		var path string
		if strings.HasPrefix(line, "[Foto diterima: ") {
			rest := strings.TrimPrefix(line, "[Foto diterima: ")
			end := strings.Index(rest, "]")
			if end > 0 {
				path = strings.TrimSpace(rest[:end])
			}
		} else if strings.HasPrefix(line, "[File diterima: ") {
			rest := strings.TrimPrefix(line, "[File diterima: ")
			end := strings.Index(rest, "]")
			if end > 0 {
				candidate := strings.TrimSpace(rest[:end])
				if idx := strings.Index(candidate, " (mime:"); idx > 0 {
					candidate = strings.TrimSpace(candidate[:idx])
				}
				ext := strings.ToLower(filepath.Ext(candidate))
				if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".gif" {
					path = candidate
				}
			}
		}
		if path == "" {
			continue
		}
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

func previewForLog(s string, maxRunes int) string {
	clean := strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	r := []rune(clean)
	if len(r) <= maxRunes {
		return clean
	}
	return string(r[:maxRunes]) + "..."
}

func summarizeArgForLog(name, rawArgs string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &m); err == nil {
		for _, k := range []string{"cmd", "path", "url", "query", "task"} {
			if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
				s := strings.Join(strings.Fields(v), " ")
				if len([]rune(s)) > 72 {
					s = string([]rune(s)[:72]) + "..."
				}
				return s
			}
		}
	}
	return ""
}

func describeToolPhase(name, rawArgs string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &m); err == nil {
		for _, k := range []string{"cmd", "path", "url", "query", "task"} {
			if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
				s := strings.Join(strings.Fields(v), " ")
				if len([]rune(s)) > 80 {
					s = string([]rune(s)[:80]) + "..."
				}
				return fmt.Sprintf("menjalankan tool %s (%s: %s)", name, k, s)
			}
		}
	}
	return fmt.Sprintf("menjalankan tool %s", name)
}

func wrapTimeoutErr(err error, phase string, since time.Time, iter int, turnLimit time.Duration) error {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "turn timeout") {
		return err
	}
	elapsed := time.Since(since).Truncate(time.Second)
	if phase != "" {
		if turnLimit > 0 {
			return fmt.Errorf("turn timeout (batas %s, iterasi %d) saat %s (berjalan %s): %w", turnLimit, iter, phase, elapsed, err)
		}
		return fmt.Errorf("timeout (iterasi %d) saat %s (berjalan %s): %w", iter, phase, elapsed, err)
	}
	return err
}
