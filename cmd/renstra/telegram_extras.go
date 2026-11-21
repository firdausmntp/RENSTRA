package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"renstra/internal/memory"
	"renstra/internal/telegram"
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
	text := fmt.Sprintf("🧠 **MEMORY (%d/%d char):**\n%s\n\n👤 **USER PROFILE (%d/%d char):**\n%s\n\n💡 _Tips: Reply pesan mana saja dengan `/remember` atau ketik `/remember <catatan>` untuk menyimpan._",
		utf8.RuneCountInString(snap.Memory), memLim, memText,
		utf8.RuneCountInString(snap.User), userLim, userText)

	rows := [][]telegram.InlineButton{
		{
			{Text: "🗑️ Clear Memory", CallbackData: "mem:clr:memory", Style: "danger"},
			{Text: "🗑️ Clear User", CallbackData: "mem:clr:user", Style: "danger"},
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
		}
	}
	if h.bot != nil {
		text, rows := h.buildMemoryMenu()
		_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
	}
}

func (h *telegramHandler) handleRememberCmd(arg string) (string, error) {
	if h.runner.Memory == nil {
		return "Memory store tidak aktif.", nil
	}
	note := strings.TrimSpace(arg)
	if note == "" {
		return "Pemakaian: `/remember <catatan>` (atau reply pesan apa pun dengan `/remember`).\nTambahkan awalan `user:` untuk menyimpan ke profil user.", nil
	}

	kind := memory.KindMemory
	label := "MEMORY.md"
	if strings.HasPrefix(strings.ToLower(note), "user:") {
		kind = memory.KindUser
		label = "USER.md"
		note = strings.TrimSpace(note[len("user:"):])
	}
	if note == "" {
		return "Catatan tidak boleh kosong.", nil
	}

	snap := h.runner.Memory.Snapshot()
	existing := snap.Memory
	if kind == memory.KindUser {
		existing = snap.User
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

func (h *telegramHandler) handleExportCmd(ctx context.Context, chatID int64) (string, error) {
	msgs, err := h.store.Messages(ctx, chatID)
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
