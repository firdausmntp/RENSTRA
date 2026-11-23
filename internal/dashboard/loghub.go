package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var secretMaskRE = regexp.MustCompile(`(?i)(sk-[a-zA-Z0-9_\-]{8,}|ghp_[a-zA-Z0-9]{8,}|xox[baprs]-[a-zA-Z0-9_\-]{8,}|AIza[a-zA-Z0-9_\-]{12,}|bot\d{6,}:[a-zA-Z0-9_\-]{16,})`)

func maskSecrets(s string) string {
	return secretMaskRE.ReplaceAllStringFunc(s, func(m string) string {
		if len(m) > 7 {
			return m[:4] + "****"
		}
		return "****"
	})
}

// LogEntry represents one structured log event captured by LogHub.
type LogEntry struct {
	ID      int64          `json:"id"`
	Time    string         `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

type logHubCore struct {
	mu      sync.Mutex
	out     io.Writer
	logPath string
	maxCap  int
	seq     int64
	entries []LogEntry
	subs    map[chan LogEntry]struct{}
	errCnt  int64
	warnCnt int64
}

// LogHub is a thread-safe slog.Handler that forwards logs to an io.Writer,
// appends JSON lines to disk, keeps an in-memory ring buffer, and broadcasts
// live entries to SSE subscribers.
type LogHub struct {
	*logHubCore
	attrs []slog.Attr
	group string
}

// NewLogHub creates a LogHub writing to logDir/renstra.log and out.
func NewLogHub(logDir string, maxCap int, out io.Writer) *LogHub {
	if maxCap <= 0 {
		maxCap = 1000
	}
	var logPath string
	if strings.TrimSpace(logDir) != "" {
		_ = os.MkdirAll(logDir, 0o755)
		logPath = filepath.Join(logDir, "renstra.log")
	}
	return &LogHub{
		logHubCore: &logHubCore{
			out:     out,
			logPath: logPath,
			maxCap:  maxCap,
			subs:    map[chan LogEntry]struct{}{},
		},
	}
}

func (h *LogHub) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *LogHub) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any)
	addAttr := func(a slog.Attr) {
		if a.Equal(slog.Attr{}) {
			return
		}
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		val := a.Value.Resolve().Any()
		if str, ok := val.(string); ok {
			val = maskSecrets(str)
		} else if err, ok := val.(error); ok && err != nil {
			val = maskSecrets(err.Error())
		}
		attrs[key] = val
	}
	for _, a := range h.attrs {
		addAttr(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		addAttr(a)
		return true
	})

	lvl := strings.ToUpper(r.Level.String())
	ts := r.Time
	if ts.IsZero() {
		ts = time.Now()
	}

	h.mu.Lock()
	h.seq++
	entry := LogEntry{
		ID:      h.seq,
		Time:    ts.Format(time.RFC3339),
		Level:   lvl,
		Message: maskSecrets(r.Message),
	}
	if len(attrs) > 0 {
		entry.Attrs = attrs
	}
	if r.Level >= slog.LevelError {
		h.errCnt++
	} else if r.Level >= slog.LevelWarn {
		h.warnCnt++
	}
	h.entries = append(h.entries, entry)
	if len(h.entries) > h.maxCap {
		h.entries = append([]LogEntry(nil), h.entries[len(h.entries)-h.maxCap:]...)
	}
	subs := make([]chan LogEntry, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	lineBytes, _ := json.Marshal(entry)
	if h.out != nil {
		_, _ = h.out.Write(append(lineBytes, '\n'))
	}
	h.appendDiskLocked(lineBytes)
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- entry:
		default:
		}
	}
	return nil
}

func (h *logHubCore) appendDiskLocked(line []byte) {
	if h.logPath == "" || len(line) == 0 {
		return
	}
	if fi, err := os.Stat(h.logPath); err == nil && fi.Size() > 5*1024*1024 {
		_ = os.Rename(h.logPath, h.logPath+".1")
	}
	f, err := os.OpenFile(h.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func (h *LogHub) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &LogHub{
		logHubCore: h.logHubCore,
		attrs:      append(append([]slog.Attr(nil), h.attrs...), attrs...),
		group:      h.group,
	}
}

func (h *LogHub) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &LogHub{
		logHubCore: h.logHubCore,
		attrs:      append([]slog.Attr(nil), h.attrs...),
		group:      g,
	}
}

// Query returns matching log entries ordered newest-first.
func (h *LogHub) Query(level, search string, limit int) []LogEntry {
	if limit <= 0 {
		limit = 200
	}
	level = strings.ToUpper(strings.TrimSpace(level))
	search = strings.ToLower(strings.TrimSpace(search))

	h.mu.Lock()
	snap := append([]LogEntry(nil), h.entries...)
	h.mu.Unlock()

	var out []LogEntry
	for i := len(snap) - 1; i >= 0 && len(out) < limit; i-- {
		e := snap[i]
		if level != "" && level != "ALL" && e.Level != level {
			continue
		}
		if search != "" {
			raw, _ := json.Marshal(e)
			if !strings.Contains(strings.ToLower(string(raw)), search) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// Counts returns total captured errors and warnings since startup.
func (h *LogHub) Counts() (errorsCount, warnsCount int64, buffered int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.errCnt, h.warnCnt, len(h.entries)
}

// Clear empties the ring buffer and truncates the on-disk log file.
func (h *LogHub) Clear() error {
	h.mu.Lock()
	h.entries = nil
	h.errCnt = 0
	h.warnCnt = 0
	logPath := h.logPath
	h.mu.Unlock()
	if logPath != "" {
		if err := os.WriteFile(logPath, nil, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("gagal menghapus log: %w", err)
		}
	}
	return nil
}

// Subscribe registers a live log channel and returns an unsubscribe function.
func (h *LogHub) Subscribe() (<-chan LogEntry, func()) {
	ch := make(chan LogEntry, 64)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}
