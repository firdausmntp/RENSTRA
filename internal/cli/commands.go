package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"renstra/internal/agent"
	"renstra/internal/cron"
)

func handleCommand(ctx context.Context, r *agent.Runner, chatID int64, line string, out io.Writer, opts *RunOptions) bool {
	if !strings.HasPrefix(line, "/") {
		return false
	}
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "/exit", "/quit":
		return true
	case "/new", "/reset", "/restart":
		if err := r.Store.SessionBreak(ctx, chatID); err != nil {
			fmt.Fprintf(out, "%s✖ error: %v%s\n", cRed, err, cReset)
			return true
		}
		r.InvalidatePrompt()
		fmt.Fprintf(out, "%s✨ Sesi baru dimulai (riwayat diputus).%s\n", cGreen, cReset)
		return true
	case "/approval":
		if arg == "" {
			fmt.Fprintf(out, "%s🔐 Mode approval saat ini:%s %s%s%s\n", cCyan, cReset, cYellow, r.GetApprovalMode(), cReset)
			fmt.Fprintf(out, "%sPilihan: /approval prompt | /approval auto | /approval deny%s\n", cDim, cReset)
			return true
		}
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "auto", "allow_all", "on":
			r.SetApprovalMode("auto")
			fmt.Fprintf(out, "%s⚡ Mode approval diubah ke: auto%s (semua tool berisiko disetujui otomatis)\n", cGreen, cReset)
		case "prompt", "ask", "manual":
			r.SetApprovalMode("prompt")
			fmt.Fprintf(out, "%s🔐 Mode approval diubah ke: prompt%s (konfirmasi y/N sebelum eksekusi)\n", cCyan, cReset)
		case "deny", "deny_all", "off":
			r.SetApprovalMode("deny")
			fmt.Fprintf(out, "%s🛑 Mode approval diubah ke: deny%s (semua tool berisiko ditolak)\n", cRed, cReset)
		default:
			fmt.Fprintf(out, "%sMode tidak dikenal. Gunakan: /approval prompt | /approval auto | /approval deny%s\n", cYellow, cReset)
		}
		return true
	case "/help":
		lang := opts.Lang
		if strings.EqualFold(arg, "en") || strings.EqualFold(arg, "id") {
			lang = strings.ToLower(arg)
		}
		printHelp(out, lang)
		return true
	case "/lang":
		switch strings.ToLower(strings.TrimSpace(arg)) {
		case "en", "english":
			opts.Lang = "en"
			fmt.Fprintf(out, "%s🌐 CLI help language set to English (/help).%s\n", cGreen, cReset)
		case "id", "indo", "indonesia":
			opts.Lang = "id"
			fmt.Fprintf(out, "%s🌐 Bahasa bantuan CLI diubah ke Indonesia (/help).%s\n", cGreen, cReset)
		default:
			cur := opts.Lang
			if cur == "" {
				cur = "id"
			}
			fmt.Fprintf(out, "%s🌐 Bahasa saat ini: %s (gunakan /lang id atau /lang en)%s\n", cCyan, cur, cReset)
		}
		return true
	case "/version":
		ver := opts.Version
		if ver == "" {
			ver = "v1.2.6"
		}
		fmt.Fprintf(out, "%s⚡ renstra%s %s%s%s\n", cCyan, cReset, cGreen, ver, cReset)
		return true
	case "/usage":
		today := time.Now().Format("2006-01-02")
		u, err := r.Store.UsageForDay(ctx, today)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return true
		}
		fmt.Fprintf(out, "%s╭─ Penggunaan hari ini (%s)%s\n", cCyan, today, cReset)
		fmt.Fprintf(out, "  ↑ masuk : %s%d tok%s\n", cWhite, u.TokensIn, cReset)
		fmt.Fprintf(out, "  ↓ keluar: %s%d tok%s\n", cWhite, u.TokensOut, cReset)
		fmt.Fprintf(out, "  💵 biaya: %s$%.4f%s\n", cGreen, u.CostUSD, cReset)
		if r.Limits.WarnDailyUSD > 0 {
			fmt.Fprintf(out, "  ⚠️ batas warn : $%.2f USD/hari\n", r.Limits.WarnDailyUSD)
		}
		if r.Limits.MaxDailyUSD > 0 {
			fmt.Fprintf(out, "  🛑 batas biaya: $%.2f USD/hari\n", r.Limits.MaxDailyUSD)
		}
		if r.Limits.MaxDailyTokens > 0 {
			fmt.Fprintf(out, "  🛑 batas token: %d tok/hari\n", r.Limits.MaxDailyTokens)
		}
		return true
	case "/insights":
		now := time.Now()
		today := now.Format("2006-01-02")
		dayU, err := r.Store.UsageForDay(ctx, today)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return true
		}
		weekU, err := r.Store.UsageSince(ctx, now.AddDate(0, 0, -7))
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return true
		}
		monthU, err := r.Store.UsageSince(ctx, now.AddDate(0, 0, -30))
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return true
		}
		fmt.Fprintf(out, "%s╭─ Insights pemakaian%s\n", cCyan, cReset)
		fmt.Fprintf(out, "  %s%-16s%s in=%-9d out=%-9d %s$%.4f%s\n", cYellow, "Hari ini:", cReset, dayU.TokensIn, dayU.TokensOut, cGreen, dayU.CostUSD, cReset)
		fmt.Fprintf(out, "  %s%-16s%s in=%-9d out=%-9d %s$%.4f%s\n", cYellow, "7 hari:", cReset, weekU.TokensIn, weekU.TokensOut, cGreen, weekU.CostUSD, cReset)
		fmt.Fprintf(out, "  %s%-16s%s in=%-9d out=%-9d %s$%.4f%s\n", cYellow, "30 hari:", cReset, monthU.TokensIn, monthU.TokensOut, cGreen, monthU.CostUSD, cReset)
		return true
	case "/status":
		printStatus(ctx, r, chatID, out, opts)
		return true
	case "/cron":
		handleCronCLI(ctx, r, chatID, arg, out)
		return true
	case "/compact":
		summary, err := r.ForceCompact(ctx, chatID)
		if err != nil {
			fmt.Fprintf(out, "gagal kompaksi: %v\n", err)
			return true
		}
		if summary == "pesan belum cukup banyak untuk dikompaksi" {
			fmt.Fprintln(out, "Pesan belum cukup banyak untuk dikompaksi (minimal riwayat melebihi 6 pesan).")
			return true
		}
		fmt.Fprintf(out, "Kompaksi selesai:\n%s\n", summary)
		return true
	case "/skills":
		if r.Skills == nil {
			fmt.Fprintln(out, "Skill registry tidak aktif.")
			return true
		}
		if strings.EqualFold(arg, "reload") {
			r.InvalidatePrompt()
			fmt.Fprintln(out, "Skill dimuat ulang dari disk.")
		} else if arg != "" {
			sk, ok := r.Skills.Get(arg)
			if !ok {
				fmt.Fprintf(out, "skill %q tidak ditemukan; ketik /skills untuk daftar\n", arg)
				return true
			}
			fmt.Fprintf(out, "%s╭─ Skill: %s%s\n", cCyan, sk.Name, cReset)
			fmt.Fprintf(out, "%s%s%s\n", cDim, sk.Description, cReset)
			fmt.Fprintf(out, "%sPath: %s%s\n\n", cDim, sk.Path, cReset)
			fmt.Fprintln(out, strings.TrimSpace(sk.Body))
			return true
		}
		idx := strings.TrimSpace(r.Skills.Index())
		if idx == "" {
			fmt.Fprintln(out, "(belum ada skill terdaftar)")
		} else {
			fmt.Fprintf(out, "%s╭─ Skills terdaftar%s\n", cCyan, cReset)
			fmt.Fprintln(out, idx)
			fmt.Fprintf(out, "%sLihat isi: /skills <nama> • Muat ulang: /skills reload%s\n", cDim, cReset)
		}
		return true
	case "/memory":
		if r.Memory == nil {
			fmt.Fprintln(out, "Memory store tidak aktif.")
			return true
		}
		snap := r.Memory.Snapshot()
		memLim, userLim := r.Memory.Limits()
		memBody := strings.TrimSpace(snap.Memory)
		if memBody == "" {
			memBody = cDim + "(kosong)" + cReset
		}
		userBody := strings.TrimSpace(snap.User)
		if userBody == "" {
			userBody = cDim + "(kosong)" + cReset
		}
		fmt.Fprintf(out, "%s╭─ MEMORY (%d/%d)%s\n%s\n\n%s╭─ USER (%d/%d)%s\n%s\n",
			cCyan, utf8.RuneCountInString(snap.Memory), memLim, cReset, memBody,
			cCyan, utf8.RuneCountInString(snap.User), userLim, cReset, userBody)
		return true
	case "/remember":
		handleRememberCLI(r, arg, out)
		return true
	case "/export":
		handleExportCLI(ctx, r, chatID, arg, out)
		return true
	case "/fallback":
		handleFallbackCLI(r, arg, out)
		return true
	case "/logs":
		handleLogsCLI(r, arg, out)
		return true
	case "/model":
		if arg == "" {
			fmt.Fprintf(out, "%sModel aktif:%s %s%s%s\n", cDim, cReset, cGreen, r.GetModel(), cReset)
			if opts.ModelList != nil {
				if listing := opts.ModelList(); listing != "" {
					fmt.Fprintln(out, listing)
				}
			}
			fmt.Fprintf(out, "%sGanti: /model <nama> atau /model <provider>:<model>%s\n", cDim, cReset)
			return true
		}
		if opts.SwitchModel != nil {
			msg, err := opts.SwitchModel(arg)
			if err != nil {
				fmt.Fprintf(out, "gagal ganti model: %v\n", err)
				return true
			}
			fmt.Fprintln(out, msg)
			return true
		}
		r.SetModel(nil, arg, 0, 0, 0, 0)
		fmt.Fprintf(out, "Model aktif diganti ke: %s\n", arg)
		return true
	default:
		return false
	}
}

func printStatus(ctx context.Context, r *agent.Runner, chatID int64, out io.Writer, opts *RunOptions) {
	win := r.ContextWindow
	if win <= 0 {
		win = 32000
	}
	msgs, _ := r.Store.Messages(ctx, chatID)
	used := agent.EstimateTokens(msgs)
	if r.Tools != nil {
		used += agent.EstimateRequestTokens("", r.Tools.Defs(), nil)
	}
	pct := float64(used) / float64(win) * 100

	skillsCount := 0
	if r.Skills != nil {
		if idx := strings.TrimSpace(r.Skills.Index()); idx != "" {
			skillsCount = len(strings.Split(idx, "\n"))
		}
	}
	today := time.Now().Format("2006-01-02")
	u, _ := r.Store.UsageForDay(ctx, today)

	prov := opts.Provider
	if prov == "" {
		prov = "default"
	}
	fmt.Fprintf(out, "%s╭─ Status sesi%s\n", cCyan, cReset)
	fmt.Fprintf(out, "  Model    : %s%s%s (%s)\n", cGreen, r.GetModel(), cReset, prov)
	fmt.Fprintf(out, "  Approval : %s%s%s\n", cYellow, r.GetApprovalMode(), cReset)
	fmt.Fprintf(out, "  Context  : %s/%s tok (%.0f%%)\n", humanTokens(used), humanTokens(win), pct)
	fmt.Fprintf(out, "  Riwayat  : %d pesan dalam sesi aktif\n", len(msgs))
	fmt.Fprintf(out, "  Skills   : %d terdaftar\n", skillsCount)
	fmt.Fprintf(out, "  Biaya    : $%.4f hari ini\n", u.CostUSD)
	if r.Cwd != "" {
		fmt.Fprintf(out, "  Workspace: %s%s%s\n", cDim, r.Cwd, cReset)
	}
}

func handleCronCLI(ctx context.Context, r *agent.Runner, chatID int64, arg string, out io.Writer) {
	sub := "list"
	rest := ""
	if arg != "" {
		parts := strings.SplitN(arg, " ", 2)
		sub = strings.ToLower(parts[0])
		if len(parts) > 1 {
			rest = strings.TrimSpace(parts[1])
		}
	}

	switch sub {
	case "list":
		jobs, err := r.Store.ListJobs(ctx, chatID)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return
		}
		if len(jobs) == 0 {
			fmt.Fprintln(out, "(tidak ada jadwal cron aktif)")
			return
		}
		fmt.Fprintf(out, "%s╭─ Jadwal cron%s\n", cCyan, cReset)
		for _, j := range jobs {
			status := cGreen + "aktif" + cReset
			if !j.Enabled {
				status = cDim + "nonaktif" + cReset
			}
			fmt.Fprintf(out, "  ID %d [%s]: %s%s%s -> %q (next: %s)\n", j.ID, status, cYellow, j.Schedule, cReset, j.Prompt, j.NextRunAt)
		}
	case "add":
		parts := strings.SplitN(rest, "|", 2)
		if len(parts) != 2 {
			fmt.Fprintln(out, "Format: /cron add <schedule> | <prompt>")
			fmt.Fprintln(out, "Contoh: /cron add 0 9 * * * | ringkas berita")
			return
		}
		sched := strings.TrimSpace(parts[0])
		prompt := strings.TrimSpace(parts[1])
		if sched == "" || prompt == "" {
			fmt.Fprintln(out, "Format: /cron add <schedule> | <prompt> (schedule dan prompt tidak boleh kosong)")
			return
		}
		next, err := cron.NextAfter(sched, time.Now(), time.Local)
		if err != nil {
			fmt.Fprintf(out, "jadwal cron tidak valid: %v\n", err)
			return
		}
		id, err := r.Store.AddJob(ctx, chatID, sched, prompt)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return
		}
		_ = r.Store.UpdateJobRun(ctx, id, next.Format(time.RFC3339), "")
		fmt.Fprintf(out, "Jadwal cron ditambahkan (ID %d): %s%s%s\nBerikutnya: %s\n", id, cYellow, sched, cReset, next.Format(time.RFC3339))
	case "rm", "remove":
		id, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil || id <= 0 {
			fmt.Fprintln(out, "Pemakaian: /cron rm <id>")
			return
		}
		if err := r.Store.RemoveJob(ctx, id); err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return
		}
		fmt.Fprintf(out, "Jadwal ID %d berhasil dihapus.\n", id)
	case "toggle", "pause":
		id, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil || id <= 0 {
			fmt.Fprintln(out, "Pemakaian: /cron toggle <id>")
			return
		}
		jobs, err := r.Store.ListJobs(ctx, chatID)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return
		}
		var enabled *bool
		for i := range jobs {
			if jobs[i].ID == id {
				v := !jobs[i].Enabled
				enabled = &v
				break
			}
		}
		if enabled == nil {
			fmt.Fprintf(out, "jadwal ID %d tidak ditemukan\n", id)
			return
		}
		if err := r.Store.SetJobEnabled(ctx, id, *enabled); err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return
		}
		if *enabled {
			fmt.Fprintf(out, "Jadwal ID %d diaktifkan.\n", id)
		} else {
			fmt.Fprintf(out, "Jadwal ID %d dinonaktifkan.\n", id)
		}
	default:
		fmt.Fprintln(out, "Subcommand cron: /cron list, /cron add <schedule> | <prompt>, /cron toggle <id>, /cron rm <id>")
	}
}
