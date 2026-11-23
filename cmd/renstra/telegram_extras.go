package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/telegram"
	"renstra/internal/tool"
)

func (h *telegramHandler) buildMemoryMenu() (string, [][]telegram.InlineButton) {
	if h.runner.Memory == nil {
		return "🧠 Memory store tidak aktif.", nil
	}
	snap := h.runner.Memory.Snapshot()
	memLim, userLim := h.runner.Memory.Limits()
	memText := strings.TrimSpace(snap.Memory)
	if memText == "" {
		memText = "(kosong)"
	}
	userText := strings.TrimSpace(snap.User)
	if userText == "" {
		userText = "(kosong)"
	}
	soulLen := utf8.RuneCountInString(snap.Soul)
	text := fmt.Sprintf("🔥 **SOUL.md (%d/%d char):** `Zero Refusal Aktif` (ketik `/soul` untuk lihat/ubah)\n\n🧠 **MEMORY (%d/%d char):**\n%s\n\n👤 **USER PROFILE (%d/%d char):**\n%s\n\n💡 _Tips: Reply pesan mana saja dengan `/remember` atau ketik `/remember <catatan>` untuk menyimpan._",
		soulLen, memory.SoulLimit,
		utf8.RuneCountInString(snap.Memory), memLim, memText,
		utf8.RuneCountInString(snap.User), userLim, userText)

	rows := [][]telegram.InlineButton{
		{
			{Text: "🗑️ Clear Memory", CallbackData: "mem:clr:memory", Style: "danger"},
			{Text: "🗑️ Clear User", CallbackData: "mem:clr:user", Style: "danger"},
			{Text: "🔥 Reset Soul", CallbackData: "mem:clr:soul", Style: "primary"},
		},
		{
			{Text: "🔄 Refresh", CallbackData: "cmd:memory", Style: "primary"},
			{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
			{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
		},
	}
	return text, rows
}

func (h *telegramHandler) handleMemoryCallback(ctx context.Context, chatID, msgID int64, parts []string) {
	if h.runner.Memory == nil || len(parts) < 3 {
		return
	}
	if parts[1] == "clr" {
		switch parts[2] {
		case "memory":
			_ = h.runner.Memory.Write(memory.KindMemory, "")
			h.invalidateAllPrompts()
		case "user":
			_ = h.runner.Memory.Write(memory.KindUser, "")
			h.invalidateAllPrompts()
		case "soul":
			_ = h.runner.Memory.ResetSoul()
			h.invalidateAllPrompts()
		}
	}
	if h.bot != nil {
		text, rows := h.buildMemoryMenu()
		_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
	}
}

func (h *telegramHandler) handleSoulCmd(arg string) (string, error) {
	if h.runner.Memory == nil {
		return memory.DefaultSoul, nil
	}
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		snap := h.runner.Memory.Snapshot()
		return fmt.Sprintf("🔥 **SOUL.md (%d/%d char):**\n\n%s\n\n💡 _Ketik `/soul reset` untuk kembali ke Zero Refusal bawaan, atau `/soul <isi baru>` untuk mengubah._",
			utf8.RuneCountInString(snap.Soul), memory.SoulLimit, strings.TrimSpace(snap.Soul)), nil
	}
	if strings.EqualFold(trimmed, "reset") || strings.EqualFold(trimmed, "default") {
		if err := h.runner.Memory.ResetSoul(); err != nil {
			return "", err
		}
		h.invalidateAllPrompts()
		return "🔥 `SOUL.md` dikembalikan ke default **Zero Refusal & Action-First**.", nil
	}
	if err := h.runner.Memory.Write(memory.KindSoul, trimmed); err != nil {
		return "", err
	}
	h.invalidateAllPrompts()
	return fmt.Sprintf("🔥 `SOUL.md` berhasil diperbarui (%d/%d char).", utf8.RuneCountInString(trimmed), memory.SoulLimit), nil
}

func (h *telegramHandler) handleRememberCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	note := strings.TrimSpace(arg)
	if note == "" {
		return "Pemakaian: `/remember <catatan>` (atau reply pesan dengan `/remember`).\nAwalan: `user:` untuk USER.md, `soul:` untuk SOUL.md, `here:` untuk memori khusus topik/grup ini.", nil
	}

	lower := strings.ToLower(note)
	if strings.HasPrefix(lower, "here:") || strings.HasPrefix(lower, "topic:") {
		idx := strings.Index(note, ":")
		note = strings.TrimSpace(note[idx+1:])
		if note == "" {
			return "Catatan topik/grup tidak boleh kosong.", nil
		}
		cs := h.loadChatSettings(ctx, chatID)
		cur := ""
		if cs.TopicMemory != nil {
			cur = strings.TrimRight(*cs.TopicMemory, "\r\n")
		}
		updated := "- " + note
		if cur != "" {
			updated = cur + "\n- " + note
		}
		cs.TopicMemory = &updated
		if err := h.saveChatSettings(ctx, chatID, cs); err != nil {
			return "", err
		}
		return fmt.Sprintf("📌 Disimpan ke **Memori Topik/Grup Ini**:\n> %s", note), nil
	}

	if h.runner.Memory == nil {
		return "Memory store tidak aktif.", nil
	}
	kind := memory.KindMemory
	label := "MEMORY.md"
	switch {
	case strings.HasPrefix(lower, "user:"):
		kind = memory.KindUser
		label = "USER.md"
		note = strings.TrimSpace(note[len("user:"):])
	case strings.HasPrefix(lower, "soul:"):
		kind = memory.KindSoul
		label = "SOUL.md"
		note = strings.TrimSpace(note[len("soul:"):])
	}
	if note == "" {
		return "Catatan tidak boleh kosong.", nil
	}

	snap := h.runner.Memory.Snapshot()
	existing := snap.Memory
	switch kind {
	case memory.KindUser:
		existing = snap.User
	case memory.KindSoul:
		existing = snap.Soul
	}
	existing = strings.TrimRight(existing, "\r\n")
	updated := "- " + note
	if existing != "" {
		updated = existing + "\n- " + note
	}
	if err := h.runner.Memory.Write(kind, updated); err != nil {
		return "", err
	}
	h.invalidateAllPrompts()
	return fmt.Sprintf("🧠 Disimpan ke `%s`:\n> %s", label, note), nil
}

func (h *telegramHandler) handleMemorySubCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	parts := strings.SplitN(strings.TrimSpace(arg), " ", 2)
	sub := strings.ToLower(strings.TrimSpace(parts[0]))
	rest := ""
	if len(parts) > 1 {
		rest = strings.TrimSpace(parts[1])
	}

	switch sub {
	case "here", "topic":
		cs := h.loadChatSettings(ctx, chatID)
		if rest == "" {
			val := "(kosong)"
			if cs.TopicMemory != nil && strings.TrimSpace(*cs.TopicMemory) != "" {
				val = strings.TrimSpace(*cs.TopicMemory)
			}
			return fmt.Sprintf("📌 **Memori Khusus Topik/Grup Ini:**\n%s\n\n💡 _Tambah dengan `/remember here: <catatan>`, ganti dengan `/memory here <isi>`, atau hapus dengan `/memory here clear`._", val), nil
		}
		if strings.EqualFold(rest, "clear") || strings.EqualFold(rest, "reset") {
			empty := ""
			cs.TopicMemory = &empty
			if err := h.saveChatSettings(ctx, chatID, cs); err != nil {
				return "", err
			}
			return "🧹 Memori khusus topik/grup ini dibersihkan.", nil
		}
		cs.TopicMemory = &rest
		if err := h.saveChatSettings(ctx, chatID, cs); err != nil {
			return "", err
		}
		return fmt.Sprintf("📌 Memori khusus topik/grup ini diperbarui (%d char).", utf8.RuneCountInString(rest)), nil

	case "set", "write":
		if h.runner.Memory == nil {
			return "Memory store tidak aktif.", nil
		}
		if err := h.runner.Memory.Write(memory.KindMemory, rest); err != nil {
			return "", err
		}
		h.invalidateAllPrompts()
		return fmt.Sprintf("🧠 `MEMORY.md` diperbarui (%d char).", utf8.RuneCountInString(rest)), nil

	case "user":
		if h.runner.Memory == nil {
			return "Memory store tidak aktif.", nil
		}
		if err := h.runner.Memory.Write(memory.KindUser, rest); err != nil {
			return "", err
		}
		h.invalidateAllPrompts()
		return fmt.Sprintf("👤 `USER.md` diperbarui (%d char).", utf8.RuneCountInString(rest)), nil

	case "soul":
		return h.handleSoulCmd(rest)

	case "del", "rm", "delete":
		if h.runner.Memory == nil {
			return "Memory store tidak aktif.", nil
		}
		kind := memory.KindMemory
		q := rest
		if strings.HasPrefix(strings.ToLower(q), "user:") {
			kind = memory.KindUser
			q = strings.TrimSpace(q[len("user:"):])
		}
		removed, err := h.runner.Memory.DeleteEntry(kind, q)
		if err != nil {
			return "", err
		}
		h.invalidateAllPrompts()
		return fmt.Sprintf("🗑️ Catatan dihapus:\n> %s", removed), nil

	case "limit", "limits":
		if h.runner.Memory == nil {
			return "Memory store tidak aktif.", nil
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			m, u, s := h.runner.Memory.AllLimits()
			return fmt.Sprintf("📏 **Batas Karakter Memory:**\n• `MEMORY.md`: `%d`\n• `USER.md`: `%d`\n• `SOUL.md`: `%d`\n\nUbah dengan: `/memory limit <memory> [user] [soul]`", m, u, s), nil
		}
		mLim, _ := strconv.Atoi(fields[0])
		uLim := 0
		sLim := 0
		if len(fields) >= 2 {
			uLim, _ = strconv.Atoi(fields[1])
		}
		if len(fields) >= 3 {
			sLim, _ = strconv.Atoi(fields[2])
		}
		h.runner.Memory.SetLimits(mLim, uLim, sLim)
		m, u, s := h.runner.Memory.AllLimits()
		return fmt.Sprintf("✅ Batas karakter diubah: `MEMORY=%d`, `USER=%d`, `SOUL=%d`.", m, u, s), nil
	default:
		return "Subcommand `/memory`: `set <isi>`, `user <isi>`, `soul <isi>`, `here [isi|clear]`, `del <no|kata>`, `limit <angka>`.", nil
	}
}

func (h *telegramHandler) handleUndoCmd() (string, error) {
	bakDir := filepath.Join(config.Home(), "checkpoints")
	restored, err := tool.RestoreLastCheckpoint(bakDir)
	if err != nil {
		return fmt.Sprintf("⏪ Tidak dapat undo: %v", err), nil
	}
	return fmt.Sprintf("⏪ **Undo berhasil!** File dikembalikan ke checkpoint terakhir:\n`%s`", restored), nil
}

func (h *telegramHandler) handleExportCmd(ctx context.Context, chatID int64) (string, error) {
	msgs, err := h.store.Messages(ctx, telegram.SessionChatID(ctx, chatID))
	if err != nil {
		return "", fmt.Errorf("gagal membaca riwayat: %w", err)
	}
	if len(msgs) == 0 {
		return "Belum ada pesan di sesi aktif untuk di-export.", nil
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

	ws := h.runner.Cwd
	if ws == "" {
		ws = os.TempDir()
	}
	tmpDir := filepath.Join(ws, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return "", err
	}
	outPath := filepath.Join(tmpDir, fmt.Sprintf("chat-%d-%s.md", chatID, time.Now().Format("20060102-150405")))
	if err := os.WriteFile(outPath, []byte(b.String()), 0o644); err != nil {
		return "", err
	}

	if h.bot != nil {
		if err := h.bot.SendDocument(ctx, chatID, outPath, fmt.Sprintf("📄 Export percakapan (%d pesan)", len(msgs))); err == nil {
			return "", nil
		}
	}
	return fmt.Sprintf("📄 Transkrip percakapan (%d pesan) disimpan ke:\n`%s`", len(msgs), outPath), nil
}

func (h *telegramHandler) handleSkillsCmd(arg string) (string, error) {
	if h.runner.Skills == nil {
		return "Skill registry tidak aktif.", nil
	}
	reloaded := false
	if strings.EqualFold(arg, "reload") {
		h.invalidateAllPrompts()
		reloaded = true
	} else if arg != "" {
		sk, ok := h.runner.Skills.Get(arg)
		if !ok {
			return fmt.Sprintf("Skill %q tidak ditemukan; ketik /skills untuk daftar", arg), nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "🛠 **Skill: %s**\n", sk.Name)
		if sk.Description != "" {
			fmt.Fprintf(&b, "*%s*\n", sk.Description)
		}
		fmt.Fprintf(&b, "Path: `%s`\n\n", sk.Path)
		b.WriteString(strings.TrimSpace(sk.Body))
		return b.String(), nil
	}
	idx := strings.TrimSpace(h.runner.Skills.Index())
	var b strings.Builder
	if reloaded {
		b.WriteString("Skill dimuat ulang dari disk.\n")
	}
	if idx == "" {
		b.WriteString("(belum ada skill terdaftar)")
	} else {
		b.WriteString("Skills terdaftar:\n")
		b.WriteString(idx)
		b.WriteString("\n\n💡 Lihat detail skill: `/skills <nama>` • Muat ulang: `/skills reload`")
	}
	if conflicts := h.runner.Skills.Conflicts(); len(conflicts) > 0 {
		b.WriteString("\n\nKonflik nama (lapis atas menang):\n")
		for _, c := range conflicts {
			fmt.Fprintf(&b, "- %s: %s (menutupi %s)\n", c.Name, c.WinnerDir, c.ShadowedDir)
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
