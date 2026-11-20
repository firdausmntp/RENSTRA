package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"renstra/internal/agent"
	"renstra/internal/cron"
)

// printHelp renders the grouped, colored command reference.
func printHelp(out io.Writer) {
	section := func(title string) {
		fmt.Fprintf(out, "\n%s%s%s\n", cCyan, title, cReset)
	}
	row := func(cmd, desc string) {
		fmt.Fprintf(out, "  %s%-30s%s %s%s%s\n", cYellow, cmd, cReset, cDim, desc, cReset)
	}
	fmt.Fprintf(out, "%s╭─ Perintah RENSTRA CLI%s\n", cCyan, cReset)
	section("💬 Sesi & Kontrol")
	row("/new, /restart", "mulai sesi baru (potong riwayat)")
	row("/compact", "ringkas riwayat percakapan sekarang")
	row("/status", "info sesi: model, konteks, biaya")
	section("🧠 Model & Kecerdasan")
	row("/model", "model aktif + daftar provider/model")
	row("/model <nama>", "ganti model (nama/alias/provider:model)")
	row("/skills", "daftar skill terdaftar")
	row("/skills <nama>", "lihat isi lengkap satu skill")
	row("/skills reload", "scan ulang skill dari disk")
	row("/memory", "lihat memori & profil tersimpan")
	section("⏰ Otomasi")
	row("/cron list", "lihat jadwal cron")
	row("/cron add <jadwal> | <prompt>", "tambah jadwal (5-field / @daily)")
	row("/cron toggle <id>", "aktif / nonaktifkan jadwal")
	row("/cron rm <id>", "hapus jadwal")
	section("📊 Analitik")
	row("/usage", "token & biaya hari ini")
	row("/insights", "rekap hari ini, 7 & 30 hari")
	section("ℹ️ Lainnya")
	row("/version", "versi binary")
	row("/help", "panduan ini")
	row("/exit", "keluar")
	row("ctrl+c", "batalkan turn yang sedang berjalan")
	fmt.Fprintf(out, "\n%sContoh cron: /cron add 0 9 * * * | ringkas berita pagi%s\n", cDim, cReset)
	fmt.Fprintf(out, "%sSelain perintah di atas, pesan dikirim langsung ke agent.%s\n", cDim, cReset)
}

// RunOptions holds optional hooks and metadata for interactive CLI commands.
type RunOptions struct {
	SwitchModel  func(ref string) (string, error)
	ModelList    func() string // Formatted provider/model/alias listing for /model
	BindApprover func(reader *bufio.Reader)
	Version      string
	Provider     string
	UpdateInfo   string // Notice when a newer version is available
}

// PrintBanner displays a styled welcome box for the interactive terminal session.
func PrintBanner(out io.Writer, r *agent.Runner, opts RunOptions) {
	out = plainIfNotTTY(out)
	ver := opts.Version
	if ver == "" {
		ver = "v1.2.0"
	}
	prov := opts.Provider
	if prov == "" {
		prov = "default"
	}
	model := r.GetModel()
	if model == "" {
		model = "default"
	}
	ctxWin := r.ContextWindow
	if ctxWin <= 0 {
		ctxWin = 32000
	}

	skillsCount := 0
	if r.Skills != nil {
		idx := strings.TrimSpace(r.Skills.Index())
		if idx != "" {
			skillsCount = len(strings.Split(idx, "\n"))
		}
	}

	const inner = 65
	var b strings.Builder
	b.WriteString(cCyan + "╭" + strings.Repeat("─", inner) + "╮" + cReset + "\n")
	bannerLine(&b, inner,
		bannerSeg{"RENSTRA Interactive Terminal", cWhite},
		bannerSeg{"  " + ver, cYellow},
	)
	bannerLine(&b, inner,
		bannerSeg{"Model    : ", cDim},
		bannerSeg{padRight(truncateStr(model, 24), 24), cGreen},
		bannerSeg{"Provider: ", cDim},
		bannerSeg{truncateStr(prov, 16), cWhite},
	)
	bannerLine(&b, inner,
		bannerSeg{"Context  : ", cDim},
		bannerSeg{padRight(fmt.Sprintf("%d tokens", ctxWin), 24), ""},
		bannerSeg{"Skills  : ", cDim},
		bannerSeg{fmt.Sprintf("%d terdaftar", skillsCount), ""},
	)
	if r.Cwd != "" {
		bannerLine(&b, inner,
			bannerSeg{"Workspace: ", cDim},
			bannerSeg{truncateStr(r.Cwd, 52), ""},
		)
	}
	bannerLine(&b, inner)
	bannerLine(&b, inner,
		bannerSeg{"Ketik pesanmu atau gunakan ", ""},
		bannerSeg{"/help", cYellow},
		bannerSeg{" untuk bantuan. Ctrl+C: batal.", ""},
	)
	b.WriteString(cCyan + "╰" + strings.Repeat("─", inner) + "╯" + cReset + "\n")
	fmt.Fprint(out, b.String())
	if opts.UpdateInfo != "" {
		fmt.Fprintf(out, "%s💡 %s%s\n\n", cYellow, opts.UpdateInfo, cReset)
	} else {
		fmt.Fprintln(out)
	}
}

// bannerSeg is one colored run of text inside a banner box line.
type bannerSeg struct{ text, color string }

// bannerLine writes "│ <segs…>…pad… │" padded by visible rune count, so ANSI
// codes and multi-byte runes never break the box alignment.
func bannerLine(b *strings.Builder, inner int, segs ...bannerSeg) {
	visible := 0
	b.WriteString(cCyan + "│" + cReset + " ")
	for _, s := range segs {
		visible += utf8.RuneCountInString(s.text)
		if s.color != "" {
			b.WriteString(s.color + s.text + cReset)
		} else {
			b.WriteString(s.text)
		}
	}
	pad := inner - 2 - visible
	if pad < 0 {
		pad = 0
	}
	b.WriteString(strings.Repeat(" ", pad) + " " + cCyan + "│" + cReset + "\n")
}

func truncateStr(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max-1]) + "…"
}

// padRight pads s with spaces to width runes (ANSI-free text only).
func padRight(s string, width int) string {
	n := utf8.RuneCountInString(s)
	if n >= width {
		return s
	}
	return s + strings.Repeat(" ", width-n)
}

// Run reads lines from in until EOF or /exit. Ctrl+C cancels the running turn
// instead of killing the process.
func Run(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer) error {
	return RunWithOptions(ctx, r, chatID, in, out, RunOptions{})
}

// RunWithOptions reads lines from in until EOF or /exit with optional command hooks.
func RunWithOptions(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer, opts RunOptions) error {
	// Streamed text is rendered by the per-turn cliStreamWriter (with live
	// status line); the runner's plain Out must stay silent to avoid doubles.
	r.Out = io.Discard
	interactive := isTTY(out)
	out = plainIfNotTTY(out)
	reader := bufio.NewReader(in)
	// Share the REPL's reader with the approval prompt so a piped or pasted
	// "y" is not eaten by the line scanner's read-ahead.
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
			if handleCommand(ctx, r, chatID, line, out, opts) {
				if line == "/exit" || line == "/quit" {
					return nil
				}
			} else if !runTurn(ctx, r, chatID, line, sigCh, out, interactive) {
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

func handleCommand(ctx context.Context, r *agent.Runner, chatID int64, line string, out io.Writer, opts RunOptions) bool {
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
			fmt.Fprintf(out, "error: %v\n", err)
			return true
		}
		r.InvalidatePrompt()
		fmt.Fprintln(out, "sesi baru")
		return true
	case "/help":
		printHelp(out)
		return true
	case "/version":
		ver := opts.Version
		if ver == "" {
			ver = "v1.2.0"
		}
		fmt.Fprintf(out, "renstra %s\n", ver)
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
		fmt.Fprintf(out, "  %-16s in=%-9d out=%-9d $%.4f\n", "Hari ini:", dayU.TokensIn, dayU.TokensOut, dayU.CostUSD)
		fmt.Fprintf(out, "  %-16s in=%-9d out=%-9d $%.4f\n", "7 hari:", weekU.TokensIn, weekU.TokensOut, weekU.CostUSD)
		fmt.Fprintf(out, "  %-16s in=%-9d out=%-9d $%.4f\n", "30 hari:", monthU.TokensIn, monthU.TokensOut, monthU.CostUSD)
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
		fmt.Fprintf(out, "MEMORY (%d/%d):\n%s\n\nUSER (%d/%d):\n%s\n",
			utf8.RuneCountInString(snap.Memory), memLim, strings.TrimSpace(snap.Memory),
			utf8.RuneCountInString(snap.User), userLim, strings.TrimSpace(snap.User))
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

// runTurn runs one turn in the background so Ctrl+C can cancel it. It returns
// false when the caller should stop the REPL.
func runTurn(ctx context.Context, r *agent.Runner, chatID int64, line string, sigCh <-chan os.Signal, out io.Writer, interactive bool) bool {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctxWin := r.ContextWindow
	if ctxWin <= 0 {
		ctxWin = 32000
	}
	sw := newCLIStreamWriter(out, ctxWin, interactive)
	// Pre-estimate context occupancy so the live status line shows ctx%
	// before the provider reports real usage at the end of the turn.
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

	fmt.Fprintf(out, "\n%s╭─ RENSTRA %s(%s)%s\n", cMagenta, cDim, r.GetModel(), cReset)

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
				fmt.Fprintf(out, "%s╰─%s %s\n\n", cMagenta, cReset, summary)
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

// buildStatsFooter renders the per-turn metrics line: elapsed time, real
// token usage, generation speed (tok/s), tool count, context occupancy, cost.
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

// humanTokens abbreviates token counts: 1234 -> "1.2k".
func humanTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// printStatus shows a session dashboard: model, context occupancy, message
// count, skills, today's cost, and workspace.
func printStatus(ctx context.Context, r *agent.Runner, chatID int64, out io.Writer, opts RunOptions) {
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
	fmt.Fprintf(out, "  Context  : %s/%s tok (%.0f%%)\n", humanTokens(used), humanTokens(win), pct)
	fmt.Fprintf(out, "  Riwayat  : %d pesan dalam sesi aktif\n", len(msgs))
	fmt.Fprintf(out, "  Skills   : %d terdaftar\n", skillsCount)
	fmt.Fprintf(out, "  Biaya    : $%.4f hari ini\n", u.CostUSD)
	if r.Cwd != "" {
		fmt.Fprintf(out, "  Workspace: %s%s%s\n", cDim, r.Cwd, cReset)
	}
}

// handleCronCLI mirrors the Telegram /cron subcommands against the same store.
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
