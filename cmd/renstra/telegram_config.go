package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"renstra/internal/agent"
	"renstra/internal/telegram"
)

type chatSettings struct {
	AutoCompact      *bool    `json:"auto_compact,omitempty"`
	CompactThreshold *float64 `json:"compact_threshold,omitempty"`
	MaxIterations    *int     `json:"max_iterations,omitempty"`
	TurnTimeoutSec   *int     `json:"turn_timeout_sec,omitempty"`
	WarnDailyUSD     *float64 `json:"warn_daily_usd,omitempty"`
	MaxDailyUSD      *float64 `json:"max_daily_usd,omitempty"`
	MaxDailyTokens   *int64   `json:"max_daily_tokens,omitempty"`
	PromptPrefix     *string  `json:"prompt_prefix,omitempty"`
}

func (h *telegramHandler) loadChatSettings(ctx context.Context, chatID int64) chatSettings {
	var cs chatSettings
	if h.store == nil {
		return cs
	}
	raw, err := h.store.GetChatSettings(ctx, chatID)
	if err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &cs)
	}
	return cs
}

func (h *telegramHandler) applyChatSettings(ctx context.Context, chatID int64, cr *agent.Runner) {
	cs := h.loadChatSettings(ctx, chatID)
	if cs.AutoCompact != nil || cs.CompactThreshold != nil {
		ac := !cr.DisableAutoCompact
		if cs.AutoCompact != nil {
			ac = *cs.AutoCompact
		}
		th := cr.Threshold
		if cs.CompactThreshold != nil {
			th = *cs.CompactThreshold
		}
		cr.SetAutoCompact(ac, th)
	}
	if cs.MaxIterations != nil {
		cr.SetMaxIters(*cs.MaxIterations)
	}
	if cs.TurnTimeoutSec != nil {
		cr.SetTurnTimeout(time.Duration(*cs.TurnTimeoutSec) * time.Second)
	}
	if cs.WarnDailyUSD != nil || cs.MaxDailyUSD != nil || cs.MaxDailyTokens != nil {
		lim := cr.Limits
		if cs.WarnDailyUSD != nil {
			lim.WarnDailyUSD = *cs.WarnDailyUSD
		}
		if cs.MaxDailyUSD != nil {
			lim.MaxDailyUSD = *cs.MaxDailyUSD
		}
		if cs.MaxDailyTokens != nil {
			lim.MaxDailyTokens = *cs.MaxDailyTokens
		}
		cr.SetLimits(lim)
	}
	if cs.PromptPrefix != nil {
		cr.SetPromptPrefix(*cs.PromptPrefix)
	}
}

func (h *telegramHandler) saveChatSettings(ctx context.Context, chatID int64, cs chatSettings) error {
	raw, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	if h.store != nil {
		if err := h.store.SetChatSettings(ctx, chatID, string(raw)); err != nil {
			return err
		}
	}
	cr := h.runnerForChat(ctx, chatID)
	h.applyChatSettings(ctx, chatID, cr)
	return nil
}

func (h *telegramHandler) resetChatSettings(ctx context.Context, chatID int64) error {
	if h.store != nil {
		if err := h.store.SetChatSettings(ctx, chatID, ""); err != nil {
			return err
		}
	}
	cr := h.runnerForChat(ctx, chatID)
	cr.SetAutoCompact(!h.runner.DisableAutoCompact, h.runner.Threshold)
	cr.SetMaxIters(h.runner.MaxIters)
	cr.SetTurnTimeout(h.runner.TurnTimeout)
	cr.SetLimits(h.runner.Limits)
	cr.SetPromptPrefix(h.runner.PromptPrefix)
	return nil
}

func (h *telegramHandler) buildConfigMenu(ctx context.Context, chatID int64, page int) (string, [][]telegram.InlineButton) {
	cr := h.runnerForChat(ctx, chatID)
	const totalPages = 2
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}

	ac := !cr.DisableAutoCompact
	th := cr.Threshold
	if th <= 0 {
		th = 0.7
	}
	mi := cr.MaxIters
	if mi <= 0 {
		mi = 40
	}
	to := cr.TurnTimeout
	if to <= 0 {
		to = 15 * time.Minute
	}
	promptStatus := "(default)"
	if strings.TrimSpace(cr.PromptPrefix) != "" {
		promptStatus = fmt.Sprintf("kustom (%d char)", len([]rune(cr.PromptPrefix)))
	}

	text := fmt.Sprintf("⚙️ **Pengaturan Default Chat (Halaman %d/%d)**\n"+
		"• **Auto-Compact:** `%v` (threshold `%.0f%%`)\n"+
		"• **Max Iterations:** `%d` iterasi/turn\n"+
		"• **Turn Timeout:** `%s`\n"+
		"• **Warn / Max Daily USD:** `$%.2f` / `$%.2f`\n"+
		"• **Max Daily Tokens:** `%d`\n"+
		"• **System Prompt Prefix:** `%s`\n\n"+
		"Ubah via tombol di bawah atau ketik `/config <key> <val>`.",
		page+1, totalPages, ac, th*100, mi, to, cr.Limits.WarnDailyUSD, cr.Limits.MaxDailyUSD, cr.Limits.MaxDailyTokens, promptStatus)

	var rows [][]telegram.InlineButton
	if page == 0 {
		acBtn := telegram.InlineButton{Text: "🗜 Auto-Compact: ON", CallbackData: "cfg:ac:0:0", Style: "success"}
		if !ac {
			acBtn = telegram.InlineButton{Text: "🗜 Auto-Compact: OFF", CallbackData: "cfg:ac:1:0", Style: "danger"}
		}
		rows = append(rows, []telegram.InlineButton{acBtn})
		rows = append(rows, []telegram.InlineButton{
			pickBtn("Thresh 50%", fmt.Sprintf("%.2f", th) == "0.50", "cfg:th:0.50:0"),
			pickBtn("Thresh 70%", fmt.Sprintf("%.2f", th) == "0.70", "cfg:th:0.70:0"),
			pickBtn("Thresh 85%", fmt.Sprintf("%.2f", th) == "0.85", "cfg:th:0.85:0"),
		})
		rows = append(rows, []telegram.InlineButton{
			pickBtn("Iter 20", mi == 20, "cfg:mi:20:0"),
			pickBtn("Iter 40", mi == 40, "cfg:mi:40:0"),
			pickBtn("Iter 80", mi == 80, "cfg:mi:80:0"),
		})
	} else {
		rows = append(rows, []telegram.InlineButton{
			pickBtn("Timeout 5m", to == 5*time.Minute, "cfg:to:300:1"),
			pickBtn("Timeout 15m", to == 15*time.Minute, "cfg:to:900:1"),
			pickBtn("Timeout 30m", to == 30*time.Minute, "cfg:to:1800:1"),
		})
		rows = append(rows, []telegram.InlineButton{
			pickBtn("Warn $1", cr.Limits.WarnDailyUSD == 1, "cfg:wu:1:1"),
			pickBtn("Warn $5", cr.Limits.WarnDailyUSD == 5, "cfg:wu:5:1"),
			pickBtn("Warn Off", cr.Limits.WarnDailyUSD == 0, "cfg:wu:0:1"),
		})
		rows = append(rows, []telegram.InlineButton{
			pickBtn("Max $5", cr.Limits.MaxDailyUSD == 5, "cfg:mu:5:1"),
			pickBtn("Max $20", cr.Limits.MaxDailyUSD == 20, "cfg:mu:20:1"),
			pickBtn("Max Unlim", cr.Limits.MaxDailyUSD == 0, "cfg:mu:0:1"),
		})
	}

	if nav := paginationRow("cfg", page, totalPages); len(nav) > 0 {
		rows = append(rows, nav)
	}
	rows = append(rows, []telegram.InlineButton{
		{Text: "🔄 Reset Default", CallbackData: fmt.Sprintf("cfg:reset:%d", page), Style: "danger"},
		{Text: "📊 Status", CallbackData: "cmd:status", Style: "primary"},
	})
	return text, rows
}

func pickBtn(label string, active bool, cb string) telegram.InlineButton {
	if active {
		return telegram.InlineButton{Text: "✅ " + label, CallbackData: cb, Style: "success"}
	}
	return telegram.InlineButton{Text: label, CallbackData: cb, Style: "primary"}
}

func (h *telegramHandler) handleConfigCallback(ctx context.Context, chatID, msgID int64, parts []string) {
	if len(parts) < 2 {
		return
	}
	page := 0
	if len(parts) >= 4 {
		page, _ = strconv.Atoi(parts[3])
	} else if len(parts) == 3 {
		page, _ = strconv.Atoi(parts[2])
	}

	cs := h.loadChatSettings(ctx, chatID)
	switch parts[1] {
	case "p":
		page, _ = strconv.Atoi(parts[2])
	case "reset":
		_ = h.resetChatSettings(ctx, chatID)
	case "ac":
		v := parts[2] == "1"
		cs.AutoCompact = &v
		_ = h.saveChatSettings(ctx, chatID, cs)
	case "th":
		if v, err := strconv.ParseFloat(parts[2], 64); err == nil {
			cs.CompactThreshold = &v
			_ = h.saveChatSettings(ctx, chatID, cs)
		}
	case "mi":
		if v, err := strconv.Atoi(parts[2]); err == nil {
			cs.MaxIterations = &v
			_ = h.saveChatSettings(ctx, chatID, cs)
		}
	case "to":
		if v, err := strconv.Atoi(parts[2]); err == nil {
			cs.TurnTimeoutSec = &v
			_ = h.saveChatSettings(ctx, chatID, cs)
		}
	case "wu":
		if v, err := strconv.ParseFloat(parts[2], 64); err == nil {
			cs.WarnDailyUSD = &v
			_ = h.saveChatSettings(ctx, chatID, cs)
		}
	case "mu":
		if v, err := strconv.ParseFloat(parts[2], 64); err == nil {
			cs.MaxDailyUSD = &v
			_ = h.saveChatSettings(ctx, chatID, cs)
		}
	}
	text, rows := h.buildConfigMenu(ctx, chatID, page)
	_ = h.bot.EditMessageWithKeyboard(ctx, chatID, msgID, text, rows)
}

func (h *telegramHandler) handleConfigCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(arg), "reset") {
		if err := h.resetChatSettings(ctx, chatID); err != nil {
			return "", err
		}
		return "🔄 Pengaturan chat dikembalikan ke default `config.yaml`.", nil
	}
	parts := strings.SplitN(strings.TrimSpace(arg), " ", 2)
	if len(parts) < 2 {
		return "Pemakaian: `/config <key> <value>`\nKey: `auto_compact`, `compact_threshold`, `max_iterations`, `turn_timeout`, `warn_usd`, `max_usd`, `max_tokens`, `prompt_prefix`, `reset`", nil
	}
	key := strings.ToLower(strings.TrimSpace(parts[0]))
	val := strings.TrimSpace(parts[1])
	cs := h.loadChatSettings(ctx, chatID)

	switch key {
	case "auto_compact":
		v := val == "on" || val == "true" || val == "1"
		cs.AutoCompact = &v
	case "compact_threshold":
		v, err := strconv.ParseFloat(val, 64)
		if err != nil || v < 0.3 || v > 0.95 {
			return "Nilai `compact_threshold` harus antara `0.3` dan `0.95`.", nil
		}
		cs.CompactThreshold = &v
	case "max_iterations":
		v, err := strconv.Atoi(val)
		if err != nil || v <= 0 {
			return "Nilai `max_iterations` harus angka positif.", nil
		}
		cs.MaxIterations = &v
	case "turn_timeout":
		d, err := time.ParseDuration(val)
		if err != nil || d <= 0 {
			return "Format `turn_timeout` tidak valid (contoh: `5m`, `15m`, `1h`).", nil
		}
		sec := int(d.Seconds())
		cs.TurnTimeoutSec = &sec
	case "warn_usd":
		v, err := strconv.ParseFloat(val, 64)
		if err != nil || v < 0 {
			return "Nilai `warn_usd` tidak valid.", nil
		}
		cs.WarnDailyUSD = &v
	case "max_usd":
		v, err := strconv.ParseFloat(val, 64)
		if err != nil || v < 0 {
			return "Nilai `max_usd` tidak valid.", nil
		}
		cs.MaxDailyUSD = &v
	case "max_tokens":
		v, err := strconv.ParseInt(val, 10, 64)
		if err != nil || v < 0 {
			return "Nilai `max_tokens` tidak valid.", nil
		}
		cs.MaxDailyTokens = &v
	case "prompt_prefix":
		if strings.EqualFold(val, "clear") || val == "-" {
			empty := ""
			cs.PromptPrefix = &empty
		} else {
			cs.PromptPrefix = &val
		}
	default:
		return fmt.Sprintf("Key %q tidak dikenal. Key tersedia: `auto_compact`, `compact_threshold`, `max_iterations`, `turn_timeout`, `warn_usd`, `max_usd`, `max_tokens`, `prompt_prefix`, `reset`.", key), nil
	}

	if err := h.saveChatSettings(ctx, chatID, cs); err != nil {
		return "", err
	}
	return fmt.Sprintf("✅ Pengaturan `%s` diperbarui menjadi `%s`.", key, val), nil
}
