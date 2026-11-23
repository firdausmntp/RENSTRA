package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/telegram"
)

func (h *telegramHandler) buildAccountMenu(ctx context.Context, chatID int64) (string, [][]telegram.InlineButton) {
	cr := h.runnerForChat(ctx, chatID)
	activeModel := cr.GetModel()
	accounts := config.ListAccounts(h.cfg)

	var b strings.Builder
	b.WriteString("🔑 **Manajemen Akun & Provider**\n")
	fmt.Fprintf(&b, "• **Model Aktif:** `%s`\n", activeModel)
	fmt.Fprintf(&b, "• **Total Akun:** `%d`\n\n", len(accounts))

	var rows [][]telegram.InlineButton
	if len(accounts) == 0 {
		b.WriteString("_(Belum ada akun provider terdaftar)_\n")
	} else {
		for _, acc := range accounts {
			status := "🟢"
			if !acc.HasKey {
				status = "🔴"
			}
			defBadge := ""
			if acc.IsDefault {
				defBadge = " ⭐"
			}
			fmt.Fprintf(&b, "• %s **%s** (`%s`)%s — model: `%s` | key: `%s`\n",
				status, acc.Name, acc.Kind, defBadge, acc.DefaultModel, acc.MaskedKey)

			btnLabel := fmt.Sprintf("🔑 Pakai %s (%s)", acc.Name, acc.DefaultModel)
			btnStyle := "primary"
			if acc.DefaultModel == activeModel {
				btnLabel = fmt.Sprintf("✅ %s (%s)", acc.Name, acc.DefaultModel)
				btnStyle = "success"
			}
			rows = append(rows, []telegram.InlineButton{
				{Text: btnLabel, CallbackData: "acc:use:" + acc.Name, Style: btnStyle},
			})
		}
	}

	b.WriteString("\n💡 **Perintah Akun:**\n")
	b.WriteString("• `/account add <nama> <kind> <api_key> [model] [base_url]`\n")
	b.WriteString("• `/account key <nama> <api_key>`\n")
	b.WriteString("• `/account use <nama>` • `/account rm <nama>`")

	rows = append(rows, []telegram.InlineButton{
		{Text: "🤖 Pilih Model", CallbackData: "cmd:model", Style: "primary"},
		{Text: "🛡️ Fallback", CallbackData: "cmd:fallback", Style: "primary"},
	})
	rows = append(rows, []telegram.InlineButton{
		{Text: "🔄 Refresh", CallbackData: "cmd:account", Style: "primary"},
		{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
		{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
	})
	return b.String(), rows
}

func (h *telegramHandler) handleAccountCallback(ctx context.Context, chatID, msgID int64, parts []string) {
	if len(parts) >= 3 && parts[1] == "use" {
		_, _ = h.switchChatModel(ctx, chatID, parts[2])
	}
	text, rows := h.buildAccountMenu(ctx, chatID)
	_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
}

func (h *telegramHandler) refreshFallbacks() {
	if h.cfg == nil {
		return
	}
	targets := buildFallbacks(h.cfg)
	h.runner.Fallbacks = targets
	h.mu.Lock()
	for _, cr := range h.chats {
		cr.Fallbacks = targets
	}
	h.mu.Unlock()
}

func (h *telegramHandler) handleAccountCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(arg))
	if len(fields) == 0 || strings.EqualFold(fields[0], "list") {
		text, _ := h.buildAccountMenu(ctx, chatID)
		return text, nil
	}
	sub := strings.ToLower(fields[0])
	switch sub {
	case "add":
		if len(fields) < 4 {
			return "Pemakaian: `/account add <nama_akun> <kind> <api_key> [default_model] [base_url]`\nContoh: `/account add or2 openrouter sk-or-v1-xxx anthropic/claude-3.7-sonnet`", nil
		}
		name, kind, apiKey := fields[1], fields[2], fields[3]
		defModel, baseURL := "", ""
		if len(fields) >= 5 {
			defModel = fields[4]
		}
		if len(fields) >= 6 {
			baseURL = fields[5]
		}
		updatedCfg, info, err := config.UpsertAccount(name, kind, apiKey, defModel, baseURL, h.cfg)
		if err != nil {
			return "", err
		}
		h.cfg = updatedCfg
		h.refreshFallbacks()
		_, _ = h.switchChatModel(ctx, chatID, info.Name+":"+info.DefaultModel)
		return fmt.Sprintf("✅ **Akun `%s` (%s) disimpan & diaktifkan!**\n• **Model:** `%s`\n• **Key (`%s`):** `%s`",
			info.Name, info.Kind, info.DefaultModel, info.APIKeyEnv, info.MaskedKey), nil

	case "key":
		if len(fields) < 3 {
			return "Pemakaian: `/account key <nama_akun> <api_key>`", nil
		}
		updatedCfg, info, err := config.UpsertAccount(fields[1], "", fields[2], "", "", h.cfg)
		if err != nil {
			return "", err
		}
		h.cfg = updatedCfg
		h.refreshFallbacks()
		return fmt.Sprintf("🔑 API Key akun `%s` (`%s`) diperbarui: `%s`", info.Name, info.APIKeyEnv, info.MaskedKey), nil

	case "use", "switch":
		if len(fields) < 2 {
			return "Pemakaian: `/account use <nama_akun>`", nil
		}
		return h.switchChatModel(ctx, chatID, fields[1])

	case "rm", "remove", "delete", "del":
		if len(fields) < 2 {
			return "Pemakaian: `/account rm <nama_akun>`", nil
		}
		updatedCfg, err := config.RemoveAccount(fields[1], h.cfg)
		if err != nil {
			return "", err
		}
		h.cfg = updatedCfg
		h.refreshFallbacks()
		return fmt.Sprintf("🗑️ Akun `%s` dihapus dari `config.yaml`.", fields[1]), nil

	default:
		return "Subcommand `/account`: `list`, `add <nama> <kind> <key> [model]`, `key <nama> <key>`, `use <nama>`, `rm <nama>`.", nil
	}
}

func accountCmd(args []string) error {
	out := cli.Writer(os.Stdout)
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(args) == 0 || args[0] == "list" {
		accounts := config.ListAccounts(cfg)
		fmt.Fprintf(out, "%s╭─ Akun & Provider Terdaftar (%d)%s\n", cli.ColorCyan, len(accounts), cli.ColorReset)
		if len(accounts) == 0 {
			fmt.Fprintf(out, "  %s(belum ada akun terdaftar)%s\n", cli.ColorDim, cli.ColorReset)
			return nil
		}
		for _, a := range accounts {
			badge := cli.ColorGreen + "aktif" + cli.ColorReset
			if !a.HasKey {
				badge = cli.ColorRed + "tanpa key" + cli.ColorReset
			}
			def := ""
			if a.IsDefault {
				def = " " + cli.ColorYellow + "[default]" + cli.ColorReset
			}
			fmt.Fprintf(out, "  %s%-12s%s (%s) model=%s%s%s key=%s (%s)%s\n",
				cli.ColorYellow, a.Name, cli.ColorReset, a.Kind,
				cli.ColorCyan, a.DefaultModel, cli.ColorReset,
				a.MaskedKey, badge, def)
		}
		fmt.Fprintf(out, "%sTambah akun: renstra account add <nama> <kind> <api_key> [model] [base_url]%s\n", cli.ColorDim, cli.ColorReset)
		return nil
	}
	switch args[0] {
	case "add":
		if len(args) < 4 {
			return fmt.Errorf("pemakaian: renstra account add <nama> <kind> <api_key> [default_model] [base_url]")
		}
		defModel, baseURL := "", ""
		if len(args) >= 5 {
			defModel = args[4]
		}
		if len(args) >= 6 {
			baseURL = args[5]
		}
		_, info, err := config.UpsertAccount(args[1], args[2], args[3], defModel, baseURL, cfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s✅ Akun %s (%s) disimpan:%s model=%s key=%s (%s)\n",
			cli.ColorGreen, info.Name, info.Kind, cli.ColorReset, info.DefaultModel, info.MaskedKey, info.APIKeyEnv)
		return nil
	case "key":
		if len(args) < 3 {
			return fmt.Errorf("pemakaian: renstra account key <nama_akun> <api_key>")
		}
		_, info, err := config.UpsertAccount(args[1], "", args[2], "", "", cfg)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "%s🔑 Key akun %s (%s) diperbarui:%s %s\n",
			cli.ColorGreen, info.Name, info.APIKeyEnv, cli.ColorReset, info.MaskedKey)
		return nil
	case "use", "switch":
		if len(args) < 2 {
			return fmt.Errorf("pemakaian: renstra account use <nama_akun>")
		}
		return modelSetCmd(args[1])
	case "rm", "remove", "delete":
		if len(args) < 2 {
			return fmt.Errorf("pemakaian: renstra account rm <nama_akun>")
		}
		if _, err := config.RemoveAccount(args[1], cfg); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s🗑️ Akun %s dihapus.%s\n", cli.ColorGreen, args[1], cli.ColorReset)
		return nil
	default:
		return fmt.Errorf("subcommand account tidak dikenal: %s", args[0])
	}
}
