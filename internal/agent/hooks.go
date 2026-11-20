package agent

import (
	"context"
	"io"
	"time"
)

type streamWriterKey struct{}
type toolHookKey struct{}
type unattendedKey struct{}
type turnStatsKey struct{}

// TurnStats receives per-turn metrics when attached via WithTurnStats.
// It is filled by Runner.Run just before returning (including on error or
// cancel), so the CLI can render tokens, speed, context usage, and cost.
type TurnStats struct {
	TokensIn      int
	TokensOut     int
	ToolCalls     int
	Iterations    int
	ContextUsed   int // best estimate of request tokens after the turn
	ContextWindow int
	CostUSD       float64
	Elapsed       time.Duration
}

// WithTurnStats attaches a stats sink for the turn.
func WithTurnStats(ctx context.Context, s *TurnStats) context.Context {
	return context.WithValue(ctx, turnStatsKey{}, s)
}

func turnStatsFrom(ctx context.Context) *TurnStats {
	s, _ := ctx.Value(turnStatsKey{}).(*TurnStats)
	return s
}

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
