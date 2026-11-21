package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"renstra/internal/agent"
	"renstra/internal/memory"
)

func handleRememberCLI(r *agent.Runner, arg string, out io.Writer) {
	if r.Memory == nil {
		fmt.Fprintln(out, "Memory store tidak aktif.")
		return
	}
	note := strings.TrimSpace(arg)
	if note == "" {
		fmt.Fprintln(out, "Pemakaian: /remember <catatan>  (awalan `user:` untuk USER.md)")
		return
	}
	kind := memory.KindMemory
	label := "MEMORY.md"
	if strings.HasPrefix(strings.ToLower(note), "user:") {
		kind = memory.KindUser
		label = "USER.md"
		note = strings.TrimSpace(note[len("user:"):])
	}
	if note == "" {
		fmt.Fprintln(out, "Catatan tidak boleh kosong.")
		return
	}
	snap := r.Memory.Snapshot()
	existing := snap.Memory
	if kind == memory.KindUser {
		existing = snap.User
	}
	existing = strings.TrimRight(existing, "\r\n")
	updated := "- " + note
	if existing != "" {
		updated = existing + "\n- " + note
	}
	if err := r.Memory.Write(kind, updated); err != nil {
		fmt.Fprintf(out, "%s✖ gagal menyimpan ke %s: %v%s\n", cRed, label, err, cReset)
		return
	}
	r.InvalidatePrompt()
	fmt.Fprintf(out, "%s🧠 Disimpan ke %s:%s %s\n", cGreen, label, cReset, note)
}

func handleExportCLI(ctx context.Context, r *agent.Runner, chatID int64, arg string, out io.Writer) {
	if r.Store == nil {
		fmt.Fprintln(out, "Store tidak aktif.")
		return
	}
	msgs, err := r.Store.Messages(ctx, chatID)
	if err != nil {
		fmt.Fprintf(out, "%s✖ gagal membaca riwayat: %v%s\n", cRed, err, cReset)
		return
	}
	if len(msgs) == 0 {
		fmt.Fprintln(out, "Belum ada pesan di sesi aktif untuk di-export.")
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# RENSTRA Chat Transcript\n\n")
	fmt.Fprintf(&b, "- Chat ID: `%d`\n", chatID)
	fmt.Fprintf(&b, "- Exported: `%s`\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&b, "- Total Messages: %d\n\n---\n\n", len(msgs))
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", strings.ToUpper(string(m.Role)))
		if m.Content != "" {
			b.WriteString(m.Content)
			b.WriteString("\n\n")
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&b, "*Tool Call:* `%s(%s)`\n\n", tc.Name, tc.Arguments)
		}
	}

	outPath := strings.TrimSpace(arg)
	if outPath == "" {
		ws := r.Cwd
		if ws == "" {
			ws = "."
		}
		tmpDir := filepath.Join(ws, "tmp")
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			fmt.Fprintf(out, "%s✖ gagal membuat folder export: %v%s\n", cRed, err, cReset)
			return
		}
		outPath = filepath.Join(tmpDir, fmt.Sprintf("chat-%d-%s.md", chatID, time.Now().Format("20060102-150405")))
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(out, "%s✖ gagal membuat direktori tujuan: %v%s\n", cRed, err, cReset)
		return
	}
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		fmt.Fprintf(out, "%s✖ gagal menulis file export: %v%s\n", cRed, err, cReset)
		return
	}
	fmt.Fprintf(out, "%s📄 Transkrip (%d pesan) di-export ke:%s %s\n", cGreen, len(msgs), cReset, outPath)
}

func handleFallbackCLI(r *agent.Runner, arg string, out io.Writer) {
	if strings.EqualFold(strings.TrimSpace(arg), "reset") || strings.EqualFold(strings.TrimSpace(arg), "clear") {
		r.ResetFallbacks()
		fmt.Fprintf(out, "%s🔄 Semua cooldown Smart Fallback telah direset.%s\n", cGreen, cReset)
		return
	}
	statuses := r.FallbackStatus()
	fmt.Fprintf(out, "%s╭─ Smart Fallback & Circuit Breaker%s\n", cCyan, cReset)
	if len(statuses) == 0 {
		fmt.Fprintf(out, "  %s(belum ada target provider aktif)%s\n", cDim, cReset)
		return
	}
	for i, st := range statuses {
		role := "Cadangan"
		if i == 0 {
			role = "Utama"
		}
		state := cGreen + "Ready" + cReset
		if st.CooldownLeft > 0 {
			sec := int(st.CooldownLeft.Round(time.Second).Seconds())
			if sec < 1 {
				sec = 1
			}
			col := cYellow
			if st.LastKind == "auth" {
				col = cRed
			}
			state = fmt.Sprintf("%sCooldown %ds%s", col, sec, cReset)
		}
		fmt.Fprintf(out, "  %d. %s[%s]%s %s:%s (%s)\n", i+1, cDim, role, cReset, st.Provider, st.Model, state)
		if st.LastError != "" && st.CooldownLeft > 0 {
			fmt.Fprintf(out, "     %sAlasan: %s%s\n", cDim, st.LastError, cReset)
		}
	}
	fmt.Fprintf(out, "%sReset cooldown: /fallback reset%s\n", cDim, cReset)
}

func handleLogsCLI(r *agent.Runner, arg string, out io.Writer) {
	home := filepath.Dir(r.Cwd)
	if r.Cwd == "" || filepath.Base(r.Cwd) != "workspace" {
		home = "."
	}
	errFile := filepath.Join(home, "logs", "errors.log")
	if strings.EqualFold(strings.TrimSpace(arg), "clear") {
		_ = os.Remove(errFile)
		fmt.Fprintf(out, "%s🧹 Log error dibersihkan.%s\n", cGreen, cReset)
		return
	}
	raw, err := os.ReadFile(errFile)
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		fmt.Fprintf(out, "%s🪵 Belum ada log error tersimpan (%s).%s\n", cDim, errFile, cReset)
		return
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\r\n"), "\n")
	if len(lines) > 20 {
		lines = lines[len(lines)-20:]
	}
	fmt.Fprintf(out, "%s╭─ Log error terbaru (%s)%s\n", cCyan, errFile, cReset)
	for _, l := range lines {
		fmt.Fprintf(out, "  %s\n", l)
	}
}
