package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"renstra/internal/telegram"
)

func (h *telegramHandler) buildFallbackMenu(ctx context.Context, chatID int64) (string, [][]telegram.InlineButton) {
	cr := h.runnerForChat(ctx, chatID)
	statuses := cr.FallbackStatus()
	autoOn := h.cfg != nil && h.cfg.SmartFallbackEnabled()

	var b strings.Builder
	b.WriteString("🛡️ **Smart Fallback & Circuit Breaker**\n")
	if autoOn {
		b.WriteString("• **Auto-Discovery:** `ON` (provider lain otomatis siaga)\n")
	} else {
		b.WriteString("• **Auto-Discovery:** `OFF` (pakai daftar `fallbacks` di config)\n")
	}
	b.WriteString("• **Routing:** Cooldown otomatis saat 429/5xx/Auth + pilih context/vision cocok\n\n")

	if len(statuses) == 0 {
		b.WriteString("_(Belum ada target provider aktif)_\n")
	} else {
		for i, st := range statuses {
			role := "Cadangan"
			if i == 0 {
				role = "Utama"
			}
			badge := "🟢 Ready"
			if st.CooldownLeft > 0 {
				sec := int(st.CooldownLeft.Round(time.Second).Seconds())
				if sec < 1 {
					sec = 1
				}
				if st.LastKind == "auth" {
					badge = fmt.Sprintf("🔴 Auth/Quota (%ds)", sec)
				} else {
					badge = fmt.Sprintf("🟡 Cooldown (%ds)", sec)
				}
			}
			caps := ""
			if st.Vision {
				caps += " 🖼️"
			}
			if st.ContextWindow > 0 {
				caps += fmt.Sprintf(" %dk", st.ContextWindow/1000)
			}
			fmt.Fprintf(&b, "%d. **[%s]** `%s:%s`%s — %s\n", i+1, role, st.Provider, st.Model, caps, badge)
			if st.LastError != "" && st.CooldownLeft > 0 {
				fmt.Fprintf(&b, "   _Alasan:_ `%s`\n", st.LastError)
			}
		}
	}

	autoBtn := telegram.InlineButton{Text: "🧠 Auto-Discovery: OFF", CallbackData: "fb:auto:1", Style: "primary"}
	if autoOn {
		autoBtn = telegram.InlineButton{Text: "🧠 Auto-Discovery: ON", CallbackData: "fb:auto:0", Style: "success"}
	}

	rows := [][]telegram.InlineButton{
		{
			{Text: "🔄 Reset Cooldown", CallbackData: "fb:reset", Style: "primary"},
			autoBtn,
		},
		{
			{Text: "🔄 Refresh", CallbackData: "cmd:fallback", Style: "primary"},
			{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
			{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
		},
	}
	return b.String(), rows
}

func (h *telegramHandler) toggleSmartAuto(ctx context.Context, chatID int64, enable bool) {
	if h.cfg == nil {
		return
	}
	h.cfg.SmartFallback = &enable
	targets := buildFallbacks(h.cfg)
	h.runner.Fallbacks = targets
	h.mu.Lock()
	for _, cr := range h.chats {
		cr.Fallbacks = targets
	}
	h.mu.Unlock()
	_ = h.runnerForChat(ctx, chatID)
}

func (h *telegramHandler) handleFallbackCallback(ctx context.Context, chatID, msgID int64, parts []string) {
	if len(parts) < 2 {
		return
	}
	switch parts[1] {
	case "reset":
		h.runnerForChat(ctx, chatID).ResetFallbacks()
	case "auto":
		enable := len(parts) >= 3 && parts[2] == "1"
		h.toggleSmartAuto(ctx, chatID, enable)
	}
	text, rows := h.buildFallbackMenu(ctx, chatID)
	_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
}

func (h *telegramHandler) handleFallbackCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "reset", "clear":
		h.runnerForChat(ctx, chatID).ResetFallbacks()
		return "🔄 Semua cooldown Smart Fallback telah direset ke `Ready`.", nil
	case "auto on", "on":
		h.toggleSmartAuto(ctx, chatID, true)
		return "🧠 Smart Auto-Discovery fallback diaktifkan (`ON`).", nil
	case "auto off", "off":
		h.toggleSmartAuto(ctx, chatID, false)
		return "🧠 Smart Auto-Discovery fallback dinonaktifkan (`OFF`).", nil
	}
	text, _ := h.buildFallbackMenu(ctx, chatID)
	return text, nil
}
