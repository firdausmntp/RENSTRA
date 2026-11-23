package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
)

// ANSI color codes for CLI rendering.
const (
	cReset         = "\033[0m"
	cDim           = "\033[0;2m"
	cCyan          = "\033[1;36m"
	cBrightCyan    = "\033[1;96m"
	cMagenta       = "\033[1;35m"
	cBrightMagenta = "\033[1;95m"
	cYellow        = "\033[1;33m"
	cGreen         = "\033[1;32m"
	cBlue          = "\033[1;34m"
	cRed           = "\033[1;31m"
	cWhite         = "\033[1;37m"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// cliStreamWriter renders streamed assistant text with an animated loading
// spinner and live status line (elapsed time, running tool, context usage).
type cliStreamWriter struct {
	mu          sync.Mutex
	out         io.Writer
	start       time.Time
	toolCalls   int
	toolNote    string
	loadingMsg  string
	spinIdx     int
	atLineStart bool
	statusOn    bool
	paused      bool
	closed      bool
	interactive bool
	contextWin  int
	contextUsed int

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
	started  bool
}

func newCLIStreamWriter(out io.Writer, contextWin int, interactive bool) *cliStreamWriter {
	return &cliStreamWriter{
		out:         out,
		start:       time.Now(),
		loadingMsg:  "Menghubungi model...",
		atLineStart: true,
		contextWin:  contextWin,
		interactive: interactive,
	}
}

// StartLoading begins the animated spinner status line when interactive.
func (w *cliStreamWriter) StartLoading() {
	if !w.interactive {
		return
	}
	w.mu.Lock()
	if w.started || w.closed {
		w.mu.Unlock()
		return
	}
	w.started = true
	w.stopCh = make(chan struct{})
	w.doneCh = make(chan struct{})
	w.renderStatusLocked()
	w.mu.Unlock()

	go func() {
		defer close(w.doneCh)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-w.stopCh:
				return
			case <-ticker.C:
				w.mu.Lock()
				if !w.closed && !w.paused {
					w.spinIdx = (w.spinIdx + 1) % len(spinnerFrames)
					w.clearStatusLocked()
					w.renderStatusLocked()
				}
				w.mu.Unlock()
			}
		}
	}()
}

// Pause temporarily hides the live status/spinner (e.g. during an approval prompt).
func (w *cliStreamWriter) Pause() {
	if !w.interactive {
		return
	}
	w.mu.Lock()
	w.paused = true
	w.clearStatusLocked()
	w.mu.Unlock()
}

// Resume restores the live status/spinner after Pause.
func (w *cliStreamWriter) Resume() {
	if !w.interactive {
		return
	}
	w.mu.Lock()
	w.paused = false
	if !w.closed {
		w.renderStatusLocked()
	}
	w.mu.Unlock()
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
	w.loadingMsg = ""
	if !w.interactive {
		// Piped/redirected output: plain passthrough, no live status line.
		w.out.Write(p)
		w.atLineStart = strings.HasSuffix(string(p), "\n")
		return len(p), nil
	}
	w.clearStatusLocked()
	w.out.Write(p)
	w.atLineStart = strings.HasSuffix(string(p), "\n")
	if !w.paused {
		w.renderStatusLocked()
	}
	return len(p), nil
}

// OnTool shows which tool is currently running (with a brief safe target summary).
func (w *cliStreamWriter) OnTool(name, args string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.toolCalls++
	label := name
	if detail := summarizeCLIArg(name, args); detail != "" {
		label = name + ": " + detail
	}
	w.toolNote = "⚙ " + name
	w.loadingMsg = "Menjalankan " + label + "..."
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
	if !w.paused {
		w.renderStatusLocked()
	}
}

// Finalize stops any active spinner, erases the status line, and leaves the cursor on a fresh line.
func (w *cliStreamWriter) Finalize() {
	w.mu.Lock()
	started := w.started
	stopCh := w.stopCh
	doneCh := w.doneCh
	w.mu.Unlock()

	if started && stopCh != nil {
		w.stopOnce.Do(func() { close(stopCh) })
		if doneCh != nil {
			<-doneCh
		}
	}

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

// clearStatusLocked erases the status line when one is on screen using saved cursor position.
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
	var parts []string
	if w.loadingMsg != "" {
		frame := spinnerFrames[w.spinIdx%len(spinnerFrames)]
		parts = append(parts, cBrightCyan+frame+cReset+" "+cWhite+w.loadingMsg+cReset)
	}
	parts = append(parts, cDim+"⏱ "+formatDuration(time.Since(w.start))+cReset)
	if w.toolNote != "" && w.loadingMsg == "" {
		parts = append(parts, cYellow+w.toolNote+cReset)
	} else if w.toolNote != "" {
		parts = append(parts, cYellow+w.toolNote+cReset)
	}
	if w.toolCalls > 0 {
		parts = append(parts, cCyan+fmt.Sprintf("%d tool", w.toolCalls)+cReset)
	}
	if w.contextWin > 0 && w.contextUsed > 0 {
		pct := float64(w.contextUsed) / float64(w.contextWin) * 100
		ctxColor := cGreen
		if pct >= 80 {
			ctxColor = cRed
		} else if pct >= 50 {
			ctxColor = cYellow
		}
		parts = append(parts, ctxColor+fmt.Sprintf("ctx %.0f%%", pct)+cReset)
	}
	return strings.Join(parts, cDim+" │ "+cReset)
}

func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func summarizeCLIArg(name, rawArgs string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &m); err != nil {
		return ""
	}
	var val string
	switch name {
	case "terminal":
		val, _ = m["cmd"].(string)
	case "read_file", "write_file", "patch", "send_file":
		val, _ = m["path"].(string)
	case "web_fetch":
		val, _ = m["url"].(string)
	case "web_search":
		val, _ = m["query"].(string)
	}
	val = strings.Join(strings.Fields(val), " ")
	if len([]rune(val)) > 48 {
		return string([]rune(val)[:48]) + "..."
	}
	return val
}
