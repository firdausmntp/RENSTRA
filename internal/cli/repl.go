package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"renstra/internal/agent"
)

// RunOptions holds optional hooks and metadata for interactive CLI commands.
type RunOptions struct {
	SwitchModel  func(ref string) (string, error)
	ModelList    func() string // Formatted provider/model/alias listing for /model
	BindApprover func(reader *bufio.Reader)
	Version      string
	Provider     string
	Lang         string // "id" (default) or "en"
	UpdateInfo   string // Notice when a newer version is available
}

// Run reads lines from in until EOF or /exit. Ctrl+C cancels the running turn
// instead of killing the process.
func Run(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer) error {
	return RunWithOptions(ctx, r, chatID, in, out, RunOptions{})
}

// RunWithOptions reads lines from in until EOF or /exit with optional command hooks.
func RunWithOptions(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer, opts RunOptions) error {
	r.Out = io.Discard
	interactive := isTTY(out)
	out = plainIfNotTTY(out)
	reader := bufio.NewReader(in)
	if opts.BindApprover != nil {
		opts.BindApprover(reader)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	for {
		fmt.Fprintf(out, "\033[1;36m╭─ User\033[0;2m · %s\033[0m\n\033[1;36m╰─>\033[0m ", time.Now().Format("15:04:05"))
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			if handleCommand(ctx, r, chatID, line, out, &opts) {
				if line == "/exit" || line == "/quit" {
					return nil
				}
			} else if !runTurn(ctx, r, chatID, line, sigCh, out, interactive, opts.UpdateInfo) {
				return nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func runTurn(ctx context.Context, r *agent.Runner, chatID int64, line string, sigCh <-chan os.Signal, out io.Writer, interactive bool, updateInfo string) bool {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctxWin := r.ContextWindow
	if ctxWin <= 0 {
		ctxWin = 32000
	}
	sw := newCLIStreamWriter(out, ctxWin, interactive)
	if msgs, merr := r.Store.Messages(turnCtx, chatID); merr == nil {
		used := agent.EstimateTokens(msgs)
		if r.Tools != nil {
			used += agent.EstimateRequestTokens("", r.Tools.Defs(), nil)
		}
		sw.SetContextUsage(used)
	}
	var stats agent.TurnStats
	turnCtx = agent.WithStreamWriter(turnCtx, sw)
	turnCtx = agent.WithToolHook(turnCtx, sw.OnTool)
	turnCtx = agent.WithTurnStats(turnCtx, &stats)
	turnCtx = withStreamPause(turnCtx, sw.Pause, sw.Resume)

	fmt.Fprintf(out, "\n%s╭─ RENSTRA %s(%s)%s\n", cBrightMagenta, cDim, r.GetModel(), cReset)
	sw.StartLoading()

	done := make(chan error, 1)
	go func() {
		_, err := r.Run(turnCtx, chatID, line)
		done <- err
	}()

	for {
		select {
		case err := <-done:
			sw.Finalize()
			summary := buildStatsFooter(&stats, ctxWin)
			switch {
			case errors.Is(err, context.Canceled):
				fmt.Fprintf(out, "%s[dibatalkan]%s %s\n\n", cYellow, cReset, summary)
			case err != nil:
				fmt.Fprint(out, FormatCLIError(err))
				if summary != "" {
					fmt.Fprintf(out, "%s\n\n", summary)
				}
			default:
				fmt.Fprintf(out, "%s╰─%s %s\n", cBrightMagenta, cReset, summary)
				if strings.TrimSpace(updateInfo) != "" {
					fmt.Fprintf(out, "   %s💡 %s%s\n", cYellow, strings.TrimSpace(updateInfo), cReset)
				}
				fmt.Fprintln(out)
			}
			return true
		case <-sigCh:
			cancel()
			<-done
			sw.Finalize()
			fmt.Fprintf(out, "%s[dibatalkan]%s %s\n\n", cYellow, cReset, buildStatsFooter(&stats, ctxWin))
			return true
		case <-ctx.Done():
			cancel()
			<-done
			sw.Finalize()
			return false
		}
	}
}

func buildStatsFooter(s *agent.TurnStats, fallbackWin int) string {
	if s == nil {
		return ""
	}
	parts := []string{"⏱ " + formatDuration(s.Elapsed)}
	if s.TokensIn > 0 || s.TokensOut > 0 {
		parts = append(parts, fmt.Sprintf("↑%s ↓%s tok", humanTokens(s.TokensIn), humanTokens(s.TokensOut)))
		if secs := s.Elapsed.Seconds(); secs > 0 && s.TokensOut > 0 {
			parts = append(parts, fmt.Sprintf("%.1f tok/s", float64(s.TokensOut)/secs))
		}
	}
	if s.ToolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tool", s.ToolCalls))
	}
	win := s.ContextWindow
	if win <= 0 {
		win = fallbackWin
	}
	if win > 0 && s.ContextUsed > 0 {
		pct := float64(s.ContextUsed) / float64(win) * 100
		parts = append(parts, fmt.Sprintf("ctx %.0f%% (%s/%s)", pct, humanTokens(s.ContextUsed), humanTokens(win)))
	}
	if s.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", s.CostUSD))
	}
	return cDim + strings.Join(parts, " │ ") + cReset
}

func humanTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}
