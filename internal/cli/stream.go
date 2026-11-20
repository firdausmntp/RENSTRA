package cli

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ANSI color codes for CLI rendering.
const (
	cReset   = "\033[0m"
	cDim     = "\033[0;2m"
	cCyan    = "\033[1;36m"
	cMagenta = "\033[1;35m"
	cYellow  = "\033[1;33m"
	cGreen   = "\033[1;32m"
	cWhite   = "\033[1;37m"
)

// cliStreamWriter renders streamed assistant text with a live status line
// (elapsed time, running tool, context usage). It is attached per turn via
// agent.WithStreamWriter / agent.WithToolHook; the runner's plain Out stays
// io.Discard in CLI mode so text is never double-written.
type cliStreamWriter struct {
	mu          sync.Mutex
	out         io.Writer
	start       time.Time
	toolCalls   int
	toolNote    string
	atLineStart bool
	statusOn    bool
	closed      bool
	interactive bool
	contextWin  int
	contextUsed int
}

func newCLIStreamWriter(out io.Writer, contextWin int, interactive bool) *cliStreamWriter {
	return &cliStreamWriter{
		out:         out,
		start:       time.Now(),
		atLineStart: true,
		contextWin:  contextWin,
		interactive: interactive,
	}
}

// Write renders one streamed text delta and refreshes the status line.
func (w *cliStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return len(p), nil
	}
	w.toolNote = ""
	if !w.interactive {
		// Piped/redirected output: plain passthrough, no live status line.
		w.out.Write(p)
		w.atLineStart = strings.HasSuffix(string(p), "\n")
		return len(p), nil
	}
	w.clearStatusLocked()
	w.out.Write(p)
	w.atLineStart = strings.HasSuffix(string(p), "\n")
	w.renderStatusLocked()
	return len(p), nil
}

// OnTool shows which tool is currently running (args stay private).
func (w *cliStreamWriter) OnTool(name, args string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.toolCalls++
	w.toolNote = "⚙ " + name
	if !w.interactive {
		if !w.atLineStart {
			fmt.Fprintln(w.out)
		}
		fmt.Fprintf(w.out, "⚙ %s\n", name)
		w.atLineStart = true
		return
	}
	w.clearStatusLocked()
	if !w.atLineStart {
		fmt.Fprintln(w.out)
		w.atLineStart = true
	}
	w.renderStatusLocked()
}

// Finalize erases the status line and leaves the cursor on a fresh line.
func (w *cliStreamWriter) Finalize() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	w.clearStatusLocked()
	if !w.atLineStart {
		fmt.Fprintln(w.out)
		w.atLineStart = true
	}
}

// SetContextUsage updates the context estimate shown in the status line.
func (w *cliStreamWriter) SetContextUsage(used int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.contextUsed = used
}

// clearStatusLocked erases the status line when one is on screen. The cursor
// always sits at the end of the status line (or of the streamed text), so a
// carriage return + erase-line is enough; no cursor-up gymnastics.
func (w *cliStreamWriter) clearStatusLocked() {
	if w.statusOn {
		fmt.Fprint(w.out, "\033[u\033[K")
		w.statusOn = false
	}
}

func (w *cliStreamWriter) renderStatusLocked() {
	fmt.Fprint(w.out, "\033[s"+w.statusLineLocked())
	w.statusOn = true
}

func (w *cliStreamWriter) statusLineLocked() string {
	parts := []string{"⏱ " + formatDuration(time.Since(w.start))}
	if w.toolNote != "" {
		parts = append(parts, w.toolNote)
	}
	if w.toolCalls > 0 {
		parts = append(parts, fmt.Sprintf("%d tool", w.toolCalls))
	}
	if w.contextWin > 0 && w.contextUsed > 0 {
		pct := float64(w.contextUsed) / float64(w.contextWin) * 100
		parts = append(parts, fmt.Sprintf("ctx %.0f%%", pct))
	}
	return cDim + strings.Join(parts, " │ ") + cReset
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}
