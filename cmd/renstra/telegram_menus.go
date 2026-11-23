package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"renstra/internal/agent"
	"renstra/internal/telegram"
)

const (
	modelPageSize = 5
	skillPageSize = 6
	cronPageSize  = 4
)

type modelOption struct {
	Label string
	Ref   string
}

func (h *telegramHandler) modelOptions() []modelOption {
	if h.cfg == nil {
		return nil
	}
	var out []modelOption
	seen := map[string]bool{}

	if len(h.cfg.Aliases) > 0 {
		anames := make([]string, 0, len(h.cfg.Aliases))
		for a := range h.cfg.Aliases {
			anames = append(anames, a)
		}
		sort.Strings(anames)
		for _, a := range anames {
			if !seen[a] {
				seen[a] = true
				out = append(out, modelOption{Label: fmt.Sprintf("%s (%s)", a, h.cfg.Aliases[a]), Ref: a})
			}
		}
	}

	pnames := make([]string, 0, len(h.cfg.Providers))
	for n := range h.cfg.Providers {
		pnames = append(pnames, n)
	}
	sort.Strings(pnames)
	for _, pn := range pnames {
		pc := h.cfg.Providers[pn]
		mset := map[string]bool{}
		if pc.DefaultModel != "" {
			mset[pc.DefaultModel] = true
		}
		for mn := range pc.Models {
			mset[mn] = true
		}
		mnames := make([]string, 0, len(mset))
		for mn := range mset {
			mnames = append(mnames, mn)
		}
		sort.Strings(mnames)
		for _, mn := range mnames {
			ref := pn + ":" + mn
			if !seen[ref] {
				seen[ref] = true
				out = append(out, modelOption{Label: ref, Ref: ref})
			}
		}
	}
	return out
}

func (h *telegramHandler) buildModelMenu(ctx context.Context, chatID int64, page int) (string, [][]telegram.InlineButton) {
	cr := h.runnerForChat(ctx, chatID)
	activeModel := cr.GetModel()
	savedRef := ""
	if h.store != nil {
		sessID := telegram.SessionChatID(ctx, chatID)
		savedRef, _ = h.store.GetChatModel(ctx, sessID)
		if savedRef == "" && sessID != chatID {
			savedRef, _ = h.store.GetChatModel(ctx, chatID)
		}
	}
	opts := h.modelOptions()

	total := len(opts)
	totalPages := (total + modelPageSize - 1) / modelPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🤖 **Pemilihan Model**\n• **Model aktif:** `%s`\n", activeModel)
	if total == 0 {
		b.WriteString("\n_(Belum ada model terkonfigurasi di config.yaml)_\n")
	} else {
		fmt.Fprintf(&b, "• **Total opsi:** %d (Halaman %d/%d)\n", total, page+1, totalPages)
	}
	b.WriteString("\nPilih lewat tombol di bawah, ketik `/model <nama>`, atau tambah model baru: `/model add <nama> <provider> <context>`.")

	var rows [][]telegram.InlineButton
	start := page * modelPageSize
	end := start + modelPageSize
	if end > total {
		end = total
	}
	for i := start; i < end; i++ {
		opt := opts[i]
		isActive := opt.Ref == savedRef || strings.HasSuffix(opt.Ref, ":"+activeModel) || opt.Ref == activeModel
		label := "🤖 " + opt.Label
		style := "primary"
		if isActive {
			label = "✅ " + opt.Label
			style = "success"
		}
		rows = append(rows, []telegram.InlineButton{
			{Text: label, CallbackData: fmt.Sprintf("mdl:s:%d:%d", i, page), Style: style},
		})
	}
	if nav := paginationRow("mdl", page, totalPages); len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "🔄 Refresh", CallbackData: fmt.Sprintf("mdl:p:%d", page), Style: "primary"},
		{Text: "🔑 Akun", CallbackData: "cmd:account", Style: "primary"},
		{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
		{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
	})
	return b.String(), rows
}

func (h *telegramHandler) buildApprovalMenu(ctx context.Context, chatID int64) (string, [][]telegram.InlineButton) {
	mode := h.runnerForChat(ctx, chatID).GetApprovalMode()
	text := fmt.Sprintf("🔐 **Pengaturan Mode Approval**\n• **Mode aktif:** `%s`\n\n• `prompt` — Minta konfirmasi tombol untuk tool berisiko\n• `auto` — Setujui semua tool secara otomatis\n• `deny` — Tolak semua tool berisiko secara otomatis\n\nPilih mode lewat tombol atau ketik `/approval <mode>`.", mode)

	promptLabel, promptStyle := "🔐 Prompt", "primary"
	if mode == "prompt" {
		promptLabel, promptStyle = "✅ Prompt", "success"
	}
	autoLabel, autoStyle := "⚡ Auto-Approve", "primary"
	if mode == "auto" {
		autoLabel, autoStyle = "✅ Auto-Approve", "success"
	}
	denyLabel, denyStyle := "🛑 Deny", "danger"
	if mode == "deny" {
		denyLabel = "✅ Deny"
	}

	rows := [][]telegram.InlineButton{
		{
			{Text: promptLabel, CallbackData: "apr:prompt", Style: promptStyle},
			{Text: autoLabel, CallbackData: "apr:auto", Style: autoStyle},
			{Text: denyLabel, CallbackData: "apr:deny", Style: denyStyle},
		},
		{
			{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
			{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
		},
	}
	return text, rows
}

func (h *telegramHandler) buildSkillsMenu(page int) (string, [][]telegram.InlineButton) {
	if h.runner.Skills == nil {
		return "🛠 Skill registry tidak aktif.", nil
	}
	names := h.runner.Skills.Names()
	total := len(names)
	totalPages := (total + skillPageSize - 1) / skillPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "🛠 **Katalog Skills (%d)**\n", total)
	if total == 0 {
		b.WriteString("_(Belum ada skill terdaftar)_\n")
	} else {
		fmt.Fprintf(&b, "Halaman %d/%d — klik tombol untuk melihat detail atau ketik `/skills <nama>`.\n", page+1, totalPages)
	}

	var rows [][]telegram.InlineButton
	start := page * skillPageSize
	end := start + skillPageSize
	if end > total {
		end = total
	}
	for i := start; i < end; i += 2 {
		row := []telegram.InlineButton{
			{Text: "🛠 " + names[i], CallbackData: fmt.Sprintf("skl:v:%d:%d", i, page), Style: "primary"},
		}
		if i+1 < end {
			row = append(row, telegram.InlineButton{
				Text: "🛠 " + names[i+1], CallbackData: fmt.Sprintf("skl:v:%d:%d", i+1, page), Style: "primary",
			})
		}
		rows = append(rows, row)
	}
	if nav := paginationRow("skl", page, totalPages); len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "🔄 Reload Disk", CallbackData: fmt.Sprintf("skl:r:%d", page), Style: "success"},
		{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
		{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
	})
	return b.String(), rows
}

func (h *telegramHandler) buildCronMenu(ctx context.Context, chatID int64, page int) (string, [][]telegram.InlineButton) {
	jobs, err := h.store.ListJobs(ctx, chatID)
	if err != nil {
		return fmt.Sprintf("⏰ Gagal membaca jadwal cron: %v", err), nil
	}
	total := len(jobs)
	totalPages := (total + cronPageSize - 1) / cronPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "⏰ **Jadwal Cron (%d)**\n", total)
	if total == 0 {
		b.WriteString("_(Tidak ada jadwal cron aktif)_\n\nTambah baru dengan:\n`/cron add 0 9 * * * | ringkas berita`")
	} else {
		fmt.Fprintf(&b, "Halaman %d/%d — klik jadwal untuk toggle aktif/nonaktif:\n\n", page+1, totalPages)
	}

	var rows [][]telegram.InlineButton
	start := page * cronPageSize
	end := start + cronPageSize
	if end > total {
		end = total
	}
	for i := start; i < end; i++ {
		j := jobs[i]
		icon, status, style := "🟢", "aktif", "success"
		if !j.Enabled {
			icon, status, style = "⚪", "nonaktif", "primary"
		}
		fmt.Fprintf(&b, "• %s **#%d** `%s` (%s) — %q\n", icon, j.ID, j.Schedule, status, j.Prompt)
		rows = append(rows, []telegram.InlineButton{
			{Text: fmt.Sprintf("%s #%d (%s)", icon, j.ID, j.Schedule), CallbackData: fmt.Sprintf("crn:t:%d:%d", j.ID, page), Style: style},
			{Text: "🗑️ Hapus", CallbackData: fmt.Sprintf("crn:d:%d:%d", j.ID, page), Style: "danger"},
		})
	}
	if nav := paginationRow("crn", page, totalPages); len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "🔄 Refresh", CallbackData: fmt.Sprintf("crn:p:%d", page), Style: "primary"},
		{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
		{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
	})
	return b.String(), rows
}

func (h *telegramHandler) buildStatusMenu(ctx context.Context, chatID int64) (string, [][]telegram.InlineButton) {
	cr := h.runnerForChat(ctx, chatID)
	win := cr.ContextWindow
	if win <= 0 {
		win = 32000
	}
	msgs, _ := h.store.Messages(ctx, telegram.SessionChatID(ctx, chatID))
	used := agent.EstimateTokens(msgs)
	if cr.Tools != nil {
		used += agent.EstimateRequestTokens("", cr.Tools.Defs(), nil)
	}
	pct := float64(used) / float64(win) * 100
	today := time.Now().Format("2006-01-02")
	u, _ := h.store.UsageForDay(ctx, today)
	skillsCount := 0
	if cr.Skills != nil {
		skillsCount = len(cr.Skills.Names())
	}
	text := fmt.Sprintf("📊 **Status Sesi**\n• **Model:** `%s`\n• **Approval:** `%s`\n• **Konteks:** %d / %d tok (%.0f%%)\n• **Riwayat:** %d pesan aktif\n• **Skills:** %d terdaftar\n• **Biaya Hari Ini:** $%.4f USD",
		cr.GetModel(), cr.GetApprovalMode(), used, win, pct, len(msgs), skillsCount, u.CostUSD) + h.updateFooter()

	rows := [][]telegram.InlineButton{
		{
			{Text: "🤖 Ganti Model", CallbackData: "cmd:model", Style: "primary"},
			{Text: "🛡️ Fallback", CallbackData: "cmd:fallback", Style: "primary"},
			{Text: "🔐 Approval", CallbackData: "cmd:approval", Style: "primary"},
		},
		{
			{Text: "🛠 Skills", CallbackData: "cmd:skills", Style: "success"},
			{Text: "🧠 Memory", CallbackData: "cmd:memory", Style: "primary"},
			{Text: "⏰ Cron", CallbackData: "cmd:cron", Style: "primary"},
			{Text: "⚙️ Config", CallbackData: "cmd:config", Style: "primary"},
		},
		{
			{Text: "🔄 Refresh", CallbackData: "cmd:status", Style: "primary"},
			{Text: "👥 Group & Topik", CallbackData: "cmd:group", Style: "primary"},
			{Text: "🏠 Menu Utama", CallbackData: "cmd:menu", Style: "primary"},
		},
	}
	return text, rows
}

func paginationRow(prefix string, page, totalPages int) []telegram.InlineButton {
	if totalPages <= 1 {
		return nil
	}
	var row []telegram.InlineButton
	if page > 0 {
		row = append(row, telegram.InlineButton{
			Text: "⬅️ Prev", CallbackData: fmt.Sprintf("%s:p:%d", prefix, page-1), Style: "primary",
		})
	}
	row = append(row, telegram.InlineButton{
		Text: fmt.Sprintf("📄 %d/%d", page+1, totalPages), CallbackData: "noop",
	})
	if page+1 < totalPages {
		row = append(row, telegram.InlineButton{
			Text: "Next ➡️", CallbackData: fmt.Sprintf("%s:p:%d", prefix, page+1), Style: "primary",
		})
	}
	return row
}

func (h *telegramHandler) handleApprovalCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	cr := h.runnerForChat(ctx, chatID)
	if arg == "" {
		return fmt.Sprintf("🔐 **Mode Approval Saat Ini:** `%s`\n\nPilihan mode:\n• `/approval prompt` — Minta konfirmasi tombol (default)\n• `/approval auto` — Setujui semua tool secara otomatis\n• `/approval deny` — Tolak semua tool berisiko", cr.GetApprovalMode()), nil
	}
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "auto", "allow_all", "on":
		if err := h.setApprovalMode(ctx, chatID, "auto"); err != nil {
			return "", fmt.Errorf("gagal menyimpan mode approval: %w", err)
		}
		return "⚡ **Mode approval diubah ke:** `auto`\nSemua perintah berisiko pada chat ini akan disetujui otomatis tanpa konfirmasi.", nil
	case "prompt", "ask", "manual":
		if err := h.setApprovalMode(ctx, chatID, "prompt"); err != nil {
			return "", fmt.Errorf("gagal menyimpan mode approval: %w", err)
		}
		return "🔐 **Mode approval diubah ke:** `prompt`\nBot akan meminta persetujuan tombol sebelum menjalankan perintah berisiko.", nil
	case "deny", "deny_all", "off":
		if err := h.setApprovalMode(ctx, chatID, "deny"); err != nil {
			return "", fmt.Errorf("gagal menyimpan mode approval: %w", err)
		}
		return "🛑 **Mode approval diubah ke:** `deny`\nSemua perintah berisiko akan ditolak otomatis.", nil
	default:
		return "Mode tidak dikenal. Gunakan: `/approval prompt`, `/approval auto`, atau `/approval deny`.", nil
	}
}
