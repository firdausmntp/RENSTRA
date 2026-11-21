package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"renstra/internal/telegram"
)

const (
	maxErrorRingEntries = 40
	maxErrorLogBytes    = 512 * 1024
)

type errorLogRecorder struct {
	mu      sync.Mutex
	home    string
	version string
	entries []string
}

func newErrorLogRecorder(home, ver string) *errorLogRecorder {
	return &errorLogRecorder{
		home:    home,
		version: ver,
	}
}

func (r *errorLogRecorder) errorsFile() string {
	return filepath.Join(r.home, "logs", "errors.log")
}

func (r *errorLogRecorder) recordAndWriteSnapshot(chatID int64, model string, err error) string {
	if r == nil || err == nil {
		return ""
	}
	msg := telegram.RedactSecrets(strings.TrimSpace(err.Error()))
	line := fmt.Sprintf("[%s] chat=%d model=%s error=%s",
		time.Now().Format(time.RFC3339), chatID, model, msg)

	r.mu.Lock()
	r.entries = append(r.entries, line)
	if len(r.entries) > maxErrorRingEntries {
		r.entries = r.entries[len(r.entries)-maxErrorRingEntries:]
	}
	_ = appendBoundedLog(r.errorsFile(), line+"\n", maxErrorLogBytes)
	r.mu.Unlock()

	return r.writeSnapshot(chatID, model, msg)
}

func (r *errorLogRecorder) writeSnapshot(chatID int64, model, latestErr string) string {
	tmpDir := filepath.Join(r.home, "workspace", "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		tmpDir = os.TempDir()
	}

	var b strings.Builder
	fmt.Fprintf(&b, "=== RENSTRA Error Diagnostic Log ===\n")
	fmt.Fprintf(&b, "Time    : %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "Version : %s (%s/%s)\n", r.version, runtime.GOOS, runtime.GOARCH)
	fmt.Fprintf(&b, "Chat ID : %d\n", chatID)
	if model != "" {
		fmt.Fprintf(&b, "Model   : %s\n", model)
	}
	if latestErr != "" {
		fmt.Fprintf(&b, "\n--- Latest Error ---\n%s\n", latestErr)
	}

	r.mu.Lock()
	ring := append([]string(nil), r.entries...)
	r.mu.Unlock()

	if len(ring) > 0 {
		fmt.Fprintf(&b, "\n--- Recent Errors (%d) ---\n", len(ring))
		for _, e := range ring {
			b.WriteString(e + "\n")
		}
	} else if raw, err := tailFile(r.errorsFile(), 25); err == nil && raw != "" {
		fmt.Fprintf(&b, "\n--- Stored Error Log ---\n%s\n", telegram.RedactSecrets(raw))
	}

	if gwTail, err := tailFile(filepath.Join(r.home, "renstra.log"), 30); err == nil && gwTail != "" {
		fmt.Fprintf(&b, "\n--- Gateway Log Tail ---\n%s\n", telegram.RedactSecrets(gwTail))
	}

	outPath := filepath.Join(tmpDir, fmt.Sprintf("error-%d-%s.log", chatID, time.Now().Format("20060102-150405")))
	if err := os.WriteFile(outPath, []byte(b.String()), 0o600); err != nil {
		return ""
	}
	return outPath
}

func (r *errorLogRecorder) clear() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	r.entries = nil
	r.mu.Unlock()
	err := os.Remove(r.errorsFile())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func appendBoundedLog(path, line string, maxBytes int64) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > maxBytes {
		if tail, terr := tailFile(path, 100); terr == nil {
			_ = os.WriteFile(path, []byte(tail+"\n"), 0o600)
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func tailFile(path string, maxLines int) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n"), nil
}

func (h *telegramHandler) handleLogsCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	if h.errLog == nil {
		return "🪵 Pencatat log error belum aktif.", nil
	}
	if strings.EqualFold(strings.TrimSpace(arg), "clear") {
		if err := h.errLog.clear(); err != nil {
			return "", err
		}
		return "🧹 Riwayat log error berhasil dibersihkan.", nil
	}
	snapPath := h.errLog.writeSnapshot(chatID, h.runnerForChat(ctx, chatID).GetModel(), "")
	if snapPath == "" {
		return "🪵 Belum ada log error yang tersedia.", nil
	}
	if h.bot != nil {
		if err := h.bot.SendDocument(ctx, chatID, snapPath, "🪵 Log error & diagnostik RENSTRA"); err == nil {
			return "", nil
		}
	}
	return fmt.Sprintf("🪵 Log diagnostik disimpan ke:\n`%s`", snapPath), nil
}
