package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/dashboard"
	"renstra/internal/store"
	"renstra/internal/telegram"
	"renstra/internal/tool"
)

type telegramHandler struct {
	bot             *telegram.Bot
	runner          *agent.Runner
	store           *store.Store
	cfg             *config.Config
	errLog          *errorLogRecorder
	approvalTimeout time.Duration

	mu            sync.Mutex
	chats         map[int64]*agent.Runner
	latestVersion string
	dashAddr      string
	logHub        *dashboard.LogHub
}

func (h *telegramHandler) SetLatestVersion(tag string) {
	t := strings.TrimSpace(tag)
	h.mu.Lock()
	h.latestVersion = t
	h.mu.Unlock()
	if h.bot != nil && t != "" {
		h.bot.SetUpdateFooter(fmt.Sprintf("💡 *Update tersedia:* `%s` (saat ini `%s`) • Jalankan `renstra update` di server.", t, version))
	}
}

func (h *telegramHandler) updateFooter() string {
	h.mu.Lock()
	t := h.latestVersion
	h.mu.Unlock()
	if t == "" || strings.EqualFold(t, version) {
		return ""
	}
	return fmt.Sprintf("\n\n---\n💡 *Update tersedia:* `%s` (saat ini `%s`) • Jalankan `renstra update` di server.", t, version)
}

func (h *telegramHandler) runnerForChat(ctx context.Context, chatID int64) *agent.Runner {
	sessID := telegram.SessionChatID(ctx, chatID)
	h.mu.Lock()
	if h.chats == nil {
		h.chats = map[int64]*agent.Runner{}
	}
	cr := h.chats[sessID]
	if cr != nil {
		h.mu.Unlock()
		return cr
	}
	cr = h.runner.Clone()
	h.chats[sessID] = cr
	h.mu.Unlock()

	if h.store != nil {
		savedRef, err := h.store.GetChatModel(ctx, sessID)
		if (err != nil || savedRef == "") && sessID != chatID {
			savedRef, err = h.store.GetChatModel(ctx, chatID)
		}
		if err == nil && savedRef != "" {
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
		savedMode, err := h.store.GetChatApprovalMode(ctx, sessID)
		if (err != nil || savedMode == "") && sessID != chatID {
			savedMode, err = h.store.GetChatApprovalMode(ctx, chatID)
		}
		if err == nil && savedMode != "" {
			cr.SetApprovalMode(savedMode)
		}
		h.applyChatSettings(ctx, chatID, cr)
	}
	return cr
}

func (h *telegramHandler) setApprovalMode(ctx context.Context, chatID int64, mode string) error {
	sessID := telegram.SessionChatID(ctx, chatID)
	cr := h.runnerForChat(ctx, chatID)
	cr.SetApprovalMode(mode)
	if h.store != nil {
		return h.store.SetChatApprovalMode(ctx, sessID, mode)
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

	sessID := telegram.SessionChatID(ctx, chatID)

	switch cmd {
	case "/new", "/reset", "/restart":
		if err := h.store.SessionBreak(ctx, sessID); err != nil {
			return "", fmt.Errorf("gagal reset sesi: %w", err)
		}
		h.invalidateAllPrompts()
		return "Sesi baru dimulai (restart). Catatan memori di-freeze ulang dan riwayat diputus.", nil

	case "/usage":
		return h.handleUsageCmd(ctx)

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
		summary, err := h.runner.ForceCompact(ctx, sessID)
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

	case "/account", "/accounts", "/provider":
		if (arg == "" || strings.EqualFold(arg, "list")) && h.bot != nil {
			aText, rows := h.buildAccountMenu(ctx, chatID)
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, aText, rows); err == nil {
				return "", nil
			}
		}
		return h.handleAccountCmd(ctx, chatID, arg)

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

	case "/context":
		if arg == "" {
			cr := h.runnerForChat(ctx, chatID)
			win := cr.ContextWindow
			if win <= 0 {
				win = 32000
			}
			msgs, _ := h.store.Messages(ctx, sessID)
			used := agent.EstimateTokens(msgs)
			return fmt.Sprintf("📏 **Context Window Aktif:** `%d` tok (terpakai ~`%d` tok)\nUbah dengan: `/context 128k` atau `/config context 200k`", win, used), nil
		}
		return h.handleConfigCmd(ctx, chatID, "context "+arg)

	case "/group":
		if arg == "" && h.bot != nil {
			gText, rows := h.buildGroupMenu(ctx, chatID)
			if err := h.bot.SendMessageWithKeyboard(ctx, chatID, gText, rows); err == nil {
				return "", nil
			}
		}
		return h.handleGroupCmd(ctx, chatID, arg)

	case "/version":
		return fmt.Sprintf("⚡ **RENSTRA** `%s`\nSelf-hosted • Telegram-first • Single-binary Go", version) + h.updateFooter(), nil

	case "/backup":
		return h.handleBackupCmd(ctx, chatID)

	case "/service":
		return h.handleServiceCmd(ctx, chatID, arg)

	case "/doctor":
		return h.handleDoctorCmd(ctx, chatID)

	case "/dashboard":
		if h.dashAddr == "" {
			return "📊 Web Dashboard tidak aktif. Jalankan `renstra serve --dashboard-port 8080` untuk mengaktifkannya.", nil
		}
		var errCnt, warnCnt int64
		var buf int
		if h.logHub != nil {
			errCnt, warnCnt, buf = h.logHub.Counts()
		}
		return fmt.Sprintf("📊 **RENSTRA Web Dashboard**\n• **Listen:** `http://%s`\n• **Log Buffered:** %d entri (`ERROR`: %d, `WARN`: %d)\n• **Kelola:** Live Logs SSE, Skills, Memory/SOUL, Multi-Akun, Cron & Turns.", h.dashAddr, buf, errCnt, warnCnt), nil

	case "/memory":
		if arg != "" {
			return h.handleMemorySubCmd(ctx, chatID, arg)
		}
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
		return h.handleRememberCmd(ctx, chatID, arg)

	case "/undo":
		return h.handleUndoCmd()

	case "/soul":
		return h.handleSoulCmd(arg)

	case "/persona":
		return h.handlePersonaCmd(ctx, chatID, arg)

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
	ctx = tool.WithChatID(ctx, chatID)

	if h.bot != nil {
		sw := telegram.NewStreamWriterWithContext(ctx, h.bot, chatID)
		defer sw.Cancel()
		ctx = agent.WithStreamWriter(ctx, sw)
		ctx = agent.WithToolHook(ctx, sw.OnTool)
		reply, err = cr.Run(ctx, sessID, text)
		if err == nil && strings.TrimSpace(reply) != "" {
			reply += h.updateFooter()
		}
		if err == nil && sw.Finalize(ctx, reply) {
			return "", nil
		}
		if err != nil {
			sw.CleanupOnError(context.Background())
		}
		return reply, err
	}

	reply, err = cr.Run(ctx, sessID, text)
	if err == nil && strings.TrimSpace(reply) != "" {
		reply += h.updateFooter()
	}
	return reply, err
}
