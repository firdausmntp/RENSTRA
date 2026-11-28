package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

func (r *Runner) logTurnStart(turnID, chatID int64, maxIters, historyMsgs int, userText string) {
	if r.Logger == nil || chatID == 0 || chatID >= 1_000_000_000_000_000 {
		return
	}
	pt := r.primaryTarget()
	r.Logger.Info("turn_started",
		"turn_id", turnID,
		"chat_id", chatID,
		"model", pt.Model,
		"context_window", pt.ContextWindow,
		"max_iters", maxIters,
		"turn_timeout", r.TurnTimeout.String(),
		"history_msgs", historyMsgs,
		"prompt_preview", previewForLog(userText, 160),
	)
}

func (r *Runner) logLLMStep(turnID, chatID int64, iter int, res streamResult, elapsed time.Duration) {
	if r.Logger == nil || chatID == 0 || chatID >= 1_000_000_000_000_000 {
		return
	}
	var stepIn, stepOut int
	if res.usage != nil {
		stepIn, stepOut = res.usage.InputTokens, res.usage.OutputTokens
	}
	callNames := make([]string, 0, len(res.calls))
	for _, c := range res.calls {
		callNames = append(callNames, c.Name)
	}
	r.Logger.Info("llm_step",
		"turn_id", turnID,
		"chat_id", chatID,
		"iter", iter,
		"model", res.model,
		"provider", res.provider,
		"duration_ms", elapsed.Milliseconds(),
		"tokens_in", stepIn,
		"tokens_out", stepOut,
		"tools", strings.Join(callNames, ","),
		"text_preview", previewForLog(res.text, 140),
	)
}

func (r *Runner) logTurnFinish(turnID, chatID int64, status string, iterCount int, elapsed time.Duration, tokIn, tokOut, lastUsageIn, toolCalls int, cost float64, errMsg string) {
	if r.Logger == nil || chatID == 0 || chatID >= 1_000_000_000_000_000 {
		return
	}
	lvl := slog.LevelInfo
	if status == "error" {
		lvl = slog.LevelError
	} else if status == "interrupted" {
		lvl = slog.LevelWarn
	}
	r.Logger.Log(context.Background(), lvl, "turn_finished",
		"turn_id", turnID,
		"chat_id", chatID,
		"model", r.GetModel(),
		"status", status,
		"iterations", iterCount,
		"duration_ms", elapsed.Milliseconds(),
		"tokens_in", tokIn,
		"tokens_out", tokOut,
		"cost_usd", fmt.Sprintf("%.4f", cost),
		"context_used", lastUsageIn,
		"context_window", r.primaryTarget().ContextWindow,
		"tool_calls", toolCalls,
		"error", errMsg,
	)
}
