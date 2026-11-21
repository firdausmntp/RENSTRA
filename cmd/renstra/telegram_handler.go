package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/store"
	"renstra/internal/telegram"
)

type telegramHandler struct {
	bot             *telegram.Bot
	runner          *agent.Runner
	store           *store.Store
	cfg             *config.Config
	errLog          *errorLogRecorder
	approvalTimeout time.Duration

	mu    sync.Mutex
	chats map[int64]*agent.Runner
}

func (h *telegramHandler) runnerForChat(ctx context.Context, chatID int64) *agent.Runner {
	h.mu.Lock()
	if h.chats == nil {
		h.chats = map[int64]*agent.Runner{}
	}
	cr := h.chats[chatID]
	if cr != nil {
		h.mu.Unlock()
		return cr
	}
	cr = h.runner.Clone()
	h.chats[chatID] = cr
	h.mu.Unlock()

	if h.store != nil {
		if savedRef, err := h.store.GetChatModel(ctx, chatID); err == nil && savedRef != "" {
			if h.cfg != nil && len(h.cfg.Providers) > 0 {
				if pcfg, model, rerr := config.Resolve(savedRef, h.cfg); rerr == nil {
					if prov, perr := buildProvider(pcfg); perr == nil {
						mc := pcfg.ModelConfig(model)
						cr.SetModel(prov, model, mc.MaxOutput, mc.Context, mc.PriceIn, mc.PriceOut)
						cr.SetCapabilities(mc.Vision, mc.Tools != nil && !*mc.Tools)
					}
				}
			} else {
				cr.SetModel(nil, savedRef, 0, 0, 0, 0)
			}
		}
		if savedMode, err := h.store.GetChatApprovalMode(ctx, chatID); err == nil && savedMode != "" {
			cr.SetApprovalMode(savedMode)
		}
		h.applyChatSettings(ctx, chatID, cr)
	}
	return cr
}

func (h *telegramHandler) setApprovalMode(ctx context.Context, chatID int64, mode string) error {
	cr := h.runnerForChat(ctx, chatID)
	cr.SetApprovalMode(mode)
	if h.store != nil {
		return h.store.SetChatApprovalMode(ctx, chatID, mode)
	}
	return nil
}

func (h *telegramHandler) invalidateAllPrompts() {
	h.runner.InvalidatePrompt()
	h.mu.Lock()
	for _, cr := range h.chats {
		cr.InvalidatePrompt()
	}
	h.mu.Unlock()
}

func (h *telegramHandler) Handle(ctx context.Context, chatID int64, text string) (reply string, err error) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic saat menangani chat %d: %v\n", chatID, r)
			reply, err = "", fmt.Errorf("panic internal: %v", r)
		}
	}()

	trimmed := strings.TrimSpace(text)
	cmd := ""
	arg := ""
	if strings.HasPrefix(trimmed, "/") {
		parts := strings.SplitN(trimmed, " ", 2)
		cmd = strings.ToLower(parts[0])
		if at := strings.Index(cmd, "@"); at != -1 {
			cmd = cmd[:at]
		}
		if len(parts) > 1 {
			arg = strings.TrimSpace(parts[1])
		}
	}

	switch cmd {
	case "/new", "/reset", "/restart":
		if err := h.store.SessionBreak(ctx, chatID); err != nil {
			return "", fmt.Errorf("gagal reset sesi: %w", err)
		}
		h.invalidateAllPrompts()
		return "Sesi baru dimulai (restart). Catatan memori di-freeze ulang dan riwayat diputus.", nil

	case "/usage":
		today := time.Now().Format("2006-01-02")
		u, err := h.store.UsageForDay(ctx, today)
		if err != nil {
			return "", fmt.Errorf("gagal membaca kuota: %w", err)
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Penggunaan Hari Ini (%s):\n- Token Masuk: %d\n- Token Keluar: %d\n- Estimasi Biaya: $%.4f USD",
			today, u.TokensIn, u.TokensOut, u.CostUSD)
		if h.cfg != nil {
			if h.cfg.Limits.WarnDailyUSD > 0 {
				fmt.Fprintf(&b, "\n- Batas Peringatan: $%.2f USD/hari", h.cfg.Limits.WarnDailyUSD)
			}
			if h.cfg.Limits.MaxDailyUSD > 0 {
				fmt.Fprintf(&b, "\n- Batas Maks Biaya: $%.2f USD/hari", h.cfg.Limits.MaxDailyUSD)
			}
			if h.cfg.Limits.MaxDailyTokens > 0 {
				fmt.Fprintf(&b, "\n- Batas Maks Token: %d token/hari", h.cfg.Limits.MaxDailyTokens)
			}
		}
		return b.String(), nil

	case "/insights":
		now := time.Now()
		today := now.Format("2006-01-02")
		dayU, err := h.store.UsageForDay(ctx, today)
		if err != nil {
			return "", fmt.Errorf("gagal membaca insights harian: %w", err)
		}
		weekU, err := h.store.UsageSince(ctx, now.AddDate(0, 0, -7))
		if err != nil {
			return "", fmt.Errorf("gagal membaca insights mingguan: %w", err)
		}
		monthU, err := h.store.UsageSince(ctx, now.AddDate(0, 0, -30))
		if err != nil {
			return "", fmt.Errorf("gagal membaca insights bulanan: %w", err)
		}
		return fmt.Sprintf("Insights Pemakaian:\n\nHari ini (%s):\n- In: %d | Out: %d | Biaya: $%.4f\n\n7 hari terakhir:\n- In: %d | Out: %d | Biaya: $%.4f\n\n30 hari terakhir:\n- In: %d | Out: %d | Biaya: $%.4f",
			today,
			dayU.TokensIn, dayU.TokensOut, dayU.CostUSD,
			weekU.TokensIn, weekU.TokensOut, weekU.CostUSD,
			monthU.TokensIn, monthU.TokensOut, monthU.CostUSD), nil

	case "/compact":
		summary, err := h.runner.ForceCompact(ctx, chatID)
		if err != nil {
			return "", fmt.Errorf("gagal kompaksi: %w", err)
		}
		if summary == "pesan belum cukup banyak untuk dikompaksi" {
			return "Pesan belum cukup banyak untuk dikompaksi (minimal riwayat melebihi 6 pesan).", nil
		}
		return fmt.Sprintf("Kompaksi selesai.\nRingkasan konteks:\n%s", summary), nil

	case "/model":
		if arg == "" && h.bot != nil {
			mText, rows := h.buildModelMenu(ctx, chatID, 0)
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, mText, rows); err == nil {
				return "", nil
			}
		}
		return h.handleModelCmd(ctx, chatID, arg)

	case "/skills":
		if (arg == "" || strings.EqualFold(arg, "reload")) && h.bot != nil {
			if strings.EqualFold(arg, "reload") {
				h.invalidateAllPrompts()
			}
			sText, rows := h.buildSkillsMenu(0)
			if len(rows) > 0 {
				if err := h.bot.SendMessageWithKeyboard(ctx, chatID, sText, rows); err == nil {
					return "", nil
				}
			}
		}
		return h.handleSkillsCmd(arg)

	case "/status":
		stText, rows := h.buildStatusMenu(ctx, chatID)
		if h.bot != nil {
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, stText, rows); err == nil {
				return "", nil
			}
		}
		return stText, nil

	case "/approval":
		if arg == "" && h.bot != nil {
			aText, rows := h.buildApprovalMenu(ctx, chatID)
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, aText, rows); err == nil {
				return "", nil
			}
		}
		return h.handleApprovalCmd(ctx, chatID, arg)

	case "/config", "/settings":
		if arg == "" {
			cText, rows := h.buildConfigMenu(ctx, chatID, 0)
			if h.bot != nil {
				if err := h.bot.SendMessageWithKeyboard(ctx, chatID, cText, rows); err == nil {
					return "", nil
				}
			}
			return cText, nil
		}
		return h.handleConfigCmd(ctx, chatID, arg)

	case "/version":
		return fmt.Sprintf("⚡ **RENSTRA** `%s`\nSelf-hosted • Telegram-first • Single-binary Go", version), nil

	case "/memory":
		mText, rows := h.buildMemoryMenu()
		if h.bot != nil && len(rows) > 0 {
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, mText, rows); err == nil {
				return "", nil
			}
		}
		if h.runner.Memory == nil {
			return "Memory store tidak aktif.", nil
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
		return fmt.Sprintf("MEMORY (%d/%d char):\n%s\n\nUSER PROFILE (%d/%d char):\n%s",
			utf8.RuneCountInString(snap.Memory), memLim, memText,
			utf8.RuneCountInString(snap.User), userLim, userText), nil

	case "/remember":
		return h.handleRememberCmd(arg)

	case "/export":
		return h.handleExportCmd(ctx, chatID)

	case "/logs":
		return h.handleLogsCmd(ctx, chatID, arg)

	case "/fallback":
		if arg == "" && h.bot != nil {
			fText, rows := h.buildFallbackMenu(ctx, chatID)
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, fText, rows); err == nil {
				return "", nil
			}
		}
		return h.handleFallbackCmd(ctx, chatID, arg)

	case "/cron":
		if (arg == "" || strings.EqualFold(arg, "list")) && h.bot != nil {
			cText, rows := h.buildCronMenu(ctx, chatID, 0)
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, cText, rows); err == nil {
				return "", nil
			}
		}
		return h.handleCronCmd(ctx, chatID, arg)

	default:
		if cmd != "" {
			return fmt.Sprintf("Perintah %q tidak dikenal. Ketik /help untuk melihat panduan semua perintah.", cmd), nil
		}
	}

	cr := h.runnerForChat(ctx, chatID)

	if h.bot != nil {
		sw := telegram.NewStreamWriterWithContext(ctx, h.bot, chatID)
		defer sw.Cancel()
		ctx = agent.WithStreamWriter(ctx, sw)
		ctx = agent.WithToolHook(ctx, sw.OnTool)
		reply, err = cr.Run(ctx, chatID, text)
		if err == nil && sw.Finalize(ctx, reply) {
			return "", nil
		}
		return reply, err
	}

	return cr.Run(ctx, chatID, text)
}

func (h *telegramHandler) switchChatModel(ctx context.Context, chatID int64, arg string) (string, error) {
	cr := h.runnerForChat(ctx, chatID)
	if h.cfg != nil && len(h.cfg.Providers) > 0 {
		pcfg, model, err := config.Resolve(arg, h.cfg)
		if err != nil {
			return "", fmt.Errorf("gagal memilih model %q: %w", arg, err)
		}
		prov, err := buildProvider(pcfg)
		if err != nil {
			return "", fmt.Errorf("gagal inisialisasi provider untuk %q: %w", arg, err)
		}
		mc := pcfg.ModelConfig(model)
		cr.SetModel(prov, model, mc.MaxOutput, mc.Context, mc.PriceIn, mc.PriceOut)
		cr.SetCapabilities(mc.Vision, mc.Tools != nil && !*mc.Tools)
		_ = h.store.SetChatModel(ctx, chatID, arg)
		if arg != model {
			return fmt.Sprintf("Model aktif diganti ke: %s (%s)", model, arg), nil
		}
		return fmt.Sprintf("Model aktif diganti ke: %s", model), nil
	}
	cr.SetModel(nil, arg, 0, 0, 0, 0)
	_ = h.store.SetChatModel(ctx, chatID, arg)
	return fmt.Sprintf("Model aktif diganti ke: %s", arg), nil
}

func (h *telegramHandler) handleModelCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	cr := h.runnerForChat(ctx, chatID)
	if arg == "" {
		var b strings.Builder
		fmt.Fprintf(&b, "Model aktif saat ini: %s\n", cr.GetModel())
		if h.cfg != nil && len(h.cfg.Providers) > 0 {
			b.WriteString("\nProvider & model terkonfigurasi:\n")
			pnames := make([]string, 0, len(h.cfg.Providers))
			for n := range h.cfg.Providers {
				pnames = append(pnames, n)
			}
			sort.Strings(pnames)
			for _, pn := range pnames {
				pc := h.cfg.Providers[pn]
				fmt.Fprintf(&b, "- %s (%s): default=%s", pn, pc.Kind, pc.DefaultModel)
				if len(pc.Models) > 0 {
					mnames := make([]string, 0, len(pc.Models))
					for mn := range pc.Models {
						mnames = append(mnames, mn)
					}
					sort.Strings(mnames)
					fmt.Fprintf(&b, " [%s]", strings.Join(mnames, ", "))
				}
				b.WriteString("\n")
			}
		}
		if h.cfg != nil && len(h.cfg.Aliases) > 0 {
			b.WriteString("\nAlias:\n")
			anames := make([]string, 0, len(h.cfg.Aliases))
			for a := range h.cfg.Aliases {
				anames = append(anames, a)
			}
			sort.Strings(anames)
			for _, a := range anames {
				fmt.Fprintf(&b, "- %s -> %s\n", a, h.cfg.Aliases[a])
			}
		}
		b.WriteString("\nGunakan `/model <nama>` atau `/model <provider>:<model>` untuk mengganti model.")
		return strings.TrimRight(b.String(), "\n"), nil
	}
	return h.switchChatModel(ctx, chatID, arg)
}
