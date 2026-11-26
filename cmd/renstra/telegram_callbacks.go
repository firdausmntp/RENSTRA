package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"renstra/internal/config"
	"renstra/internal/telegram"
)

// HandleCallback routes non-approval inline keyboard button clicks.
func (h *telegramHandler) HandleCallback(ctx context.Context, chatID, msgID int64, data string) {
	if h.bot == nil || data == "noop" {
		return
	}
	parts := strings.Split(data, ":")
	switch parts[0] {
	case "cmd":
		if len(parts) < 2 {
			return
		}
		var text string
		var rows [][]telegram.InlineButton
		switch parts[1] {
		case "menu", "start":
			text, rows = telegram.StartText, telegram.DefaultQuickMenu()
		case "help":
			text, rows = telegram.HelpText, telegram.HelpQuickMenu()
		case "model":
			text, rows = h.buildModelMenu(ctx, chatID, 0)
		case "account":
			text, rows = h.buildAccountMenu(ctx, chatID)
		case "approval":
			text, rows = h.buildApprovalMenu(ctx, chatID)
		case "skills":
			text, rows = h.buildSkillsMenu(0)
		case "cron":
			text, rows = h.buildCronMenu(ctx, chatID, 0)
		case "config":
			text, rows = h.buildConfigMenu(ctx, chatID, 0)
		case "memory":
			text, rows = h.buildMemoryMenu()
		case "fallback":
			text, rows = h.buildFallbackMenu(ctx, chatID)
		case "group":
			text, rows = h.buildGroupMenu(ctx, chatID)
		case "status":
			text, rows = h.buildStatusMenu(ctx, chatID)
		default:
			return
		}
		_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)

	case "cfg":
		h.handleConfigCallback(ctx, chatID, msgID, parts)

	case "mem":
		h.handleMemoryCallback(ctx, chatID, msgID, parts)

	case "fb":
		h.handleFallbackCallback(ctx, chatID, msgID, parts)

	case "grp":
		h.handleGroupCallback(ctx, chatID, msgID, parts)

	case "acc":
		h.handleAccountCallback(ctx, chatID, msgID, parts)

	case "svc":
		if len(parts) >= 2 && parts[1] == "restart" {
			_ = h.bot.EditMessage(ctx, chatID, msgID, "🔄 Me-restart service RENSTRA & memuat ulang konfigurasi...")
			go func() {
				time.Sleep(300 * time.Millisecond)
				_ = triggerSelfRestart(config.Home(), chatID, "Inline button /service restart")
			}()
		}

	case "mdl":
		if len(parts) < 3 {
			return
		}
		switch parts[1] {
		case "p":
			page, _ := strconv.Atoi(parts[2])
			text, rows := h.buildModelMenu(ctx, chatID, page)
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
		case "s":
			idx, err := strconv.Atoi(parts[2])
			if err != nil {
				return
			}
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			opts := h.modelOptions()
			if idx >= 0 && idx < len(opts) {
				_, _ = h.switchChatModel(ctx, chatID, opts[idx].Ref)
			}
			text, rows := h.buildModelMenu(ctx, chatID, page)
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
		}

	case "apr":
		if len(parts) < 2 {
			return
		}
		mode := parts[1]
		if mode == "prompt" || mode == "auto" || mode == "deny" {
			_ = h.setApprovalMode(ctx, chatID, mode)
		}
		text, rows := h.buildApprovalMenu(ctx, chatID)
		_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)

	case "skl":
		if len(parts) < 3 {
			return
		}
		switch parts[1] {
		case "p":
			page, _ := strconv.Atoi(parts[2])
			text, rows := h.buildSkillsMenu(page)
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
		case "r":
			h.invalidateAllPrompts()
			page, _ := strconv.Atoi(parts[2])
			text, rows := h.buildSkillsMenu(page)
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
		case "v":
			idx, err := strconv.Atoi(parts[2])
			if err != nil || h.runner.Skills == nil {
				return
			}
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			names := h.runner.Skills.Names()
			if idx < 0 || idx >= len(names) {
				return
			}
			sk, ok := h.runner.Skills.Get(names[idx])
			if !ok {
				return
			}
			var b strings.Builder
			fmt.Fprintf(&b, "🛠 **Skill: %s**\n", sk.Name)
			if sk.Description != "" {
				fmt.Fprintf(&b, "*%s*\n", sk.Description)
			}
			fmt.Fprintf(&b, "Path: `%s`\n\n%s", sk.Path, strings.TrimSpace(sk.Body))
			rows := [][]telegram.InlineButton{
				{
					{Text: "⬅️ Kembali ke Daftar", CallbackData: fmt.Sprintf("skl:p:%d", page), Style: "primary"},
					{Text: "🏠 Menu", CallbackData: "cmd:menu", Style: "primary"},
				},
			}
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, b.String(), rows)
		}

	case "crn":
		if len(parts) < 3 {
			return
		}
		switch parts[1] {
		case "p":
			page, _ := strconv.Atoi(parts[2])
			text, rows := h.buildCronMenu(ctx, chatID, page)
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
		case "t":
			id, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil && id > 0 {
				if jobs, jerr := h.store.ListJobs(ctx, chatID); jerr == nil {
					for i := range jobs {
						if jobs[i].ID == id {
							_ = h.store.SetJobEnabled(ctx, id, !jobs[i].Enabled)
							break
						}
					}
				}
			}
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			text, rows := h.buildCronMenu(ctx, chatID, page)
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
		case "d":
			id, err := strconv.ParseInt(parts[2], 10, 64)
			if err == nil && id > 0 {
				_ = h.store.RemoveJob(ctx, id)
			}
			page := 0
			if len(parts) >= 4 {
				page, _ = strconv.Atoi(parts[3])
			}
			text, rows := h.buildCronMenu(ctx, chatID, page)
			_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
		}
	}
}
