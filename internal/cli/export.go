package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/tool"
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

func handleSoulCLI(r *agent.Runner, arg string, out io.Writer) {
	if r.Memory == nil {
		fmt.Fprintln(out, strings.TrimSpace(memory.DefaultSoul))
		return
	}
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		snap := r.Memory.Snapshot()
		fmt.Fprintf(out, "%s╭─ SOUL.md (%d/%d)%s\n%s\n",
			cCyan, len([]rune(snap.Soul)), memory.SoulLimit, cReset, strings.TrimSpace(snap.Soul))
		return
	}
	if strings.EqualFold(trimmed, "reset") || strings.EqualFold(trimmed, "default") {
		if err := r.Memory.ResetSoul(); err != nil {
			fmt.Fprintf(out, "%s✖ gagal reset SOUL.md: %v%s\n", cRed, err, cReset)
			return
		}
		r.InvalidatePrompt()
		fmt.Fprintf(out, "%s🔥 SOUL.md dikembalikan ke default Zero Refusal.%s\n", cGreen, cReset)
		return
	}
	if err := r.Memory.Write(memory.KindSoul, trimmed); err != nil {
		fmt.Fprintf(out, "%s✖ gagal menyimpan SOUL.md: %v%s\n", cRed, err, cReset)
		return
	}
	r.InvalidatePrompt()
	fmt.Fprintf(out, "%s🔥 SOUL.md diperbarui (%d/%d char).%s\n", cGreen, len([]rune(trimmed)), memory.SoulLimit, cReset)
}

func handleContextCLI(ctx context.Context, r *agent.Runner, chatID int64, arg string, out io.Writer) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		win := r.ContextWindow
		if win <= 0 {
			win = 32000
		}
		used := 0
		if r.Store != nil {
			msgs, _ := r.Store.Messages(ctx, chatID)
			used = agent.EstimateTokens(msgs)
		}
		if r.Tools != nil {
			used += agent.EstimateRequestTokens("", r.Tools.Defs(), nil)
		}
		pct := float64(used) / float64(win) * 100
		fmt.Fprintf(out, "%s📏 Context Window:%s %s%d tok%s (terpakai ~%d tok / %.0f%%)\n",
			cCyan, cReset, cGreen, win, cReset, used, pct)
		fmt.Fprintf(out, "%sUbah: /context <ukuran> (contoh: /context 128k, /context 200k, /context 1m)%s\n", cDim, cReset)
		return
	}
	n, err := config.ParseTokenCount(trimmed)
	if err != nil || n < 4096 {
		fmt.Fprintf(out, "%s✖ ukuran context minimal 4096 (contoh: 32k, 128k, 200k, 1m)%s\n", cRed, cReset)
		return
	}
	r.SetContextWindow(n)
	fmt.Fprintf(out, "%s✅ Context window diubah ke:%s %s%d tokens%s\n", cGreen, cReset, cCyan, n, cReset)
}

func handlePersonaCLI(r *agent.Runner, arg string, out io.Writer) {
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		cur := strings.TrimSpace(r.PromptPrefix)
		if cur == "" {
			cur = "(default)"
		}
		fmt.Fprintf(out, "%s🎭 Persona aktif:%s %s\n", cCyan, cReset, cur)
		fmt.Fprintf(out, "%sUbah: /persona <instruksi> | Reset: /persona reset%s\n", cDim, cReset)
		return
	}
	if strings.EqualFold(trimmed, "reset") || strings.EqualFold(trimmed, "clear") || trimmed == "-" {
		r.SetPromptPrefix("")
		fmt.Fprintf(out, "%s🎭 Persona dikembalikan ke default.%s\n", cGreen, cReset)
		return
	}
	r.SetPromptPrefix(trimmed)
	fmt.Fprintf(out, "%s🎭 Persona diperbarui:%s %s\n", cGreen, cReset, trimmed)
}

func handleMemorySubCLI(r *agent.Runner, arg string, out io.Writer) {
	if r.Memory == nil {
		fmt.Fprintln(out, "Memory store tidak aktif.")
		return
	}
	parts := strings.SplitN(strings.TrimSpace(arg), " ", 2)
	sub := strings.ToLower(strings.TrimSpace(parts[0]))
	rest := ""
	if len(parts) > 1 {
		rest = strings.TrimSpace(parts[1])
	}
	switch sub {
	case "set", "write":
		if err := r.Memory.Write(memory.KindMemory, rest); err != nil {
			fmt.Fprintf(out, "%s✖ %v%s\n", cRed, err, cReset)
			return
		}
		r.InvalidatePrompt()
		fmt.Fprintf(out, "%s🧠 MEMORY.md diperbarui (%d char).%s\n", cGreen, len([]rune(rest)), cReset)
	case "user":
		if err := r.Memory.Write(memory.KindUser, rest); err != nil {
			fmt.Fprintf(out, "%s✖ %v%s\n", cRed, err, cReset)
			return
		}
		r.InvalidatePrompt()
		fmt.Fprintf(out, "%s👤 USER.md diperbarui (%d char).%s\n", cGreen, len([]rune(rest)), cReset)
	case "soul":
		handleSoulCLI(r, rest, out)
	case "del", "rm", "delete":
		kind := memory.KindMemory
		q := rest
		if strings.HasPrefix(strings.ToLower(q), "user:") {
			kind = memory.KindUser
			q = strings.TrimSpace(q[len("user:"):])
		}
		removed, err := r.Memory.DeleteEntry(kind, q)
		if err != nil {
			fmt.Fprintf(out, "%s✖ %v%s\n", cRed, err, cReset)
			return
		}
		r.InvalidatePrompt()
		fmt.Fprintf(out, "%s🗑️ Catatan dihapus:%s %s\n", cGreen, cReset, removed)
	case "limit", "limits":
		fields := strings.Fields(rest)
		if len(fields) > 0 {
			mLim, _ := strconv.Atoi(fields[0])
			uLim, sLim := 0, 0
			if len(fields) >= 2 {
				uLim, _ = strconv.Atoi(fields[1])
			}
			if len(fields) >= 3 {
				sLim, _ = strconv.Atoi(fields[2])
			}
			r.Memory.SetLimits(mLim, uLim, sLim)
		}
		m, u, s := r.Memory.AllLimits()
		fmt.Fprintf(out, "%s📏 Batas karakter:%s MEMORY=%d, USER=%d, SOUL=%d\n", cCyan, cReset, m, u, s)
	default:
		fmt.Fprintln(out, "Subcommand /memory: set <isi>, user <isi>, soul <isi>, del <no|kata>, limit <angka>")
	}
}

func handleUndoCLI(out io.Writer) {
	restored, err := tool.RestoreLastCheckpoint(filepath.Join(config.Home(), "checkpoints"))
	if err != nil {
		fmt.Fprintf(out, "%s⏪ Tidak dapat undo: %v%s\n", cYellow, err, cReset)
		return
	}
	fmt.Fprintf(out, "%s⏪ Undo berhasil! File dikembalikan:%s %s\n", cGreen, cReset, restored)
}

func handleAccountCLI(arg string, out io.Writer, opts *RunOptions) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(out, "%s✖ %v%s\n", cRed, err, cReset)
		return
	}
	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) == 0 || strings.EqualFold(fields[0], "list") {
		accounts := config.ListAccounts(cfg)
		fmt.Fprintf(out, "%s╭─ Akun & Provider (%d)%s\n", cCyan, len(accounts), cReset)
		for _, a := range accounts {
			st := cGreen + "aktif" + cReset
			if !a.HasKey {
				st = cRed + "tanpa key" + cReset
			}
			fmt.Fprintf(out, "  %s%-12s%s (%s) model=%s%s%s key=%s (%s)\n",
				cYellow, a.Name, cReset, a.Kind, cGreen, a.DefaultModel, cReset, a.MaskedKey, st)
		}
		fmt.Fprintf(out, "%sPerintah: /account add <nama> <kind> <key> [model] | /account key <nama> <key> | /account use <nama> | /account rm <nama>%s\n", cDim, cReset)
		return
	}
	switch strings.ToLower(fields[0]) {
	case "add":
		if len(fields) < 4 {
			fmt.Fprintln(out, "Pemakaian: /account add <nama> <kind> <api_key> [default_model] [base_url]")
			return
		}
		var defM, bURL string
		if len(fields) >= 5 {
			defM = fields[4]
		}
		if len(fields) >= 6 {
			bURL = fields[5]
		}
		if _, info, err := config.UpsertAccount(fields[1], fields[2], fields[3], defM, bURL, cfg); err != nil {
			fmt.Fprintf(out, "%s✖ %v%s\n", cRed, err, cReset)
		} else {
			fmt.Fprintf(out, "%s✅ Akun %s (%s) disimpan:%s model=%s key=%s\n", cGreen, info.Name, info.Kind, cReset, info.DefaultModel, info.MaskedKey)
		}
	case "key":
		if len(fields) < 3 {
			fmt.Fprintln(out, "Pemakaian: /account key <nama> <api_key>")
			return
		}
		if _, info, err := config.UpsertAccount(fields[1], "", fields[2], "", "", cfg); err != nil {
			fmt.Fprintf(out, "%s✖ %v%s\n", cRed, err, cReset)
		} else {
			fmt.Fprintf(out, "%s🔑 Key akun %s diperbarui:%s %s\n", cGreen, info.Name, cReset, info.MaskedKey)
		}
	case "use", "switch":
		if len(fields) >= 2 && opts != nil && opts.SwitchModel != nil {
			if msg, err := opts.SwitchModel(fields[1]); err == nil {
				fmt.Fprintln(out, msg)
				return
			}
		}
		if len(fields) >= 2 {
			fmt.Fprintf(out, "%s✅ Akun aktif: %s%s\n", cGreen, fields[1], cReset)
		}
	case "rm", "remove", "delete":
		if len(fields) >= 2 {
			if _, err := config.RemoveAccount(fields[1], cfg); err != nil {
				fmt.Fprintf(out, "%s✖ %v%s\n", cRed, err, cReset)
			} else {
				fmt.Fprintf(out, "%s🗑️ Akun %s dihapus.%s\n", cGreen, fields[1], cReset)
			}
		}
	}
}
