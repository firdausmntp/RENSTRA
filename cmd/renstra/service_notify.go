package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/store"
	"renstra/internal/telegram"
)

const restartNotifyFile = "restart_notify.json"

type restartNotify struct {
	ChatID      int64     `json:"chat_id"`
	Reason      string    `json:"reason"`
	RequestedAt time.Time `json:"requested_at"`
}

func writeRestartNotify(home string, chatID int64, reason string) {
	if strings.TrimSpace(home) == "" {
		return
	}
	_ = os.MkdirAll(home, 0o755)
	raw, _ := json.Marshal(restartNotify{
		ChatID:      chatID,
		Reason:      strings.TrimSpace(reason),
		RequestedAt: time.Now(),
	})
	_ = os.WriteFile(filepath.Join(home, restartNotifyFile), raw, 0o600)
}

func consumeRestartNotify(home string) (*restartNotify, bool) {
	if strings.TrimSpace(home) == "" {
		return nil, false
	}
	p := filepath.Join(home, restartNotifyFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	_ = os.Remove(p)
	var rn restartNotify
	if json.Unmarshal(raw, &rn) != nil {
		return nil, false
	}
	if !rn.RequestedAt.IsZero() && time.Since(rn.RequestedAt) > 2*time.Minute {
		return nil, false
	}
	return &rn, true
}

func notifyRestartReady(
	ctx context.Context,
	bot *telegram.Bot,
	st *store.Store,
	cfg *config.Config,
	home, model, provKind string,
	ctxWin, mcpCount, mcpTools, skillCount int,
	dashAddr string,
) {
	if bot == nil {
		return
	}
	rn, ok := consumeRestartNotify(home)
	if !ok {
		return
	}
	var targets []int64
	if rn.ChatID != 0 {
		targets = []int64{rn.ChatID}
	} else if cfg != nil && len(cfg.Telegram.AllowedUsers) > 0 {
		targets = append(targets, cfg.Telegram.AllowedUsers...)
	} else if st != nil {
		if chats, err := st.ListChatsSummary(ctx, 1); err == nil && len(chats) > 0 && chats[0].ChatID != 0 {
			targets = []int64{chats[0].ChatID}
		}
	}
	if len(targets) == 0 {
		return
	}
	fbCount := 0
	smartFB := false
	if cfg != nil {
		fbCount = len(cfg.Fallbacks)
		smartFB = cfg.SmartFallbackEnabled()
	}
	reason := rn.Reason
	if reason == "" {
		reason = "Service restart"
	}
	dashLine := "nonaktif"
	if dashAddr != "" {
		dashLine = "`http://" + dashAddr + "`"
	}
	msg := fmt.Sprintf(
		"✅ **RENSTRA Service Di-Restart & Config Baru Dimuat**\n"+
			"• **Pemicu:** %s\n"+
			"• **Versi:** `%s` (PID `%d`)\n"+
			"• **Model Aktif:** `%s` (`%s`, ctx `%dk`)\n"+
			"• **Fallback:** `%d` target (Smart: `%v`)\n"+
			"• **MCP:** `%d` server (`%d` tool) · **Skills:** `%d`\n"+
			"• **Dashboard:** %s",
		reason, version, os.Getpid(), model, provKind, ctxWin/1000,
		fbCount, smartFB, mcpCount, mcpTools, skillCount, dashLine,
	)
	for _, cid := range targets {
		_ = bot.SendMessage(ctx, cid, msg)
	}
}

func triggerSelfRestart(home string, chatID int64, reason string) error {
	if _, err := config.Load(); err != nil {
		return fmt.Errorf("config.yaml tidak valid, restart dibatalkan: %w", err)
	}
	writeRestartNotify(home, chatID, reason)
	if runtime.GOOS == "linux" {
		if exec.Command("systemctl", "--user", "cat", "renstra").Run() == nil {
			return exec.Command("systemctl", "--user", "restart", "renstra").Start()
		}
		if exec.Command("systemctl", "cat", "renstra").Run() == nil || exec.Command("systemctl", "is-active", "--quiet", "renstra").Run() == nil {
			cmd := exec.Command("systemctl", "restart", "renstra")
			if os.Geteuid() != 0 {
				if _, err := exec.LookPath("sudo"); err == nil {
					cmd = exec.Command("sudo", "-n", "systemctl", "restart", "renstra")
				}
			}
			return cmd.Start()
		}
	}
	_ = os.Remove(pidFilePath(home))
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	logPath := filepath.Join(home, "renstra.log")
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		defer logFile.Close()
	}
	cmd := exec.Command(exe, "serve")
	cmd.Env = append(os.Environ(), "RENSTRA_RESTART_DELAY_MS=900")
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	cmd.SysProcAttr = detachedSysProcAttr()
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	go func() {
		time.Sleep(150 * time.Millisecond)
		os.Exit(0)
	}()
	return nil
}

func serviceCmd(args []string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	sub := "status"
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "restart", "reload":
		return restartCmd(args[1:], out)
	case "start":
		return restartCmd([]string{"--start"}, out)
	case "stop":
		home := config.Home()
		if runtime.GOOS == "linux" {
			if exec.Command("systemctl", "--user", "is-active", "--quiet", "renstra").Run() == nil {
				return exec.Command("systemctl", "--user", "stop", "renstra").Run()
			}
			if exec.Command("systemctl", "is-active", "--quiet", "renstra").Run() == nil {
				return exec.Command("systemctl", "stop", "renstra").Run()
			}
		}
		if pid, alive := readLivePID(home); alive {
			if err := stopProcess(pid, 5*time.Second); err != nil {
				return err
			}
			_ = os.Remove(pidFilePath(home))
			fmt.Fprintf(cli.Writer(out), "%s⏹️ Service renstra dihentikan (PID %d).%s\n", cli.ColorYellow, pid, cli.ColorReset)
			return nil
		}
		fmt.Fprintf(cli.Writer(out), "%sℹ️ Service renstra tidak sedang berjalan.%s\n", cli.ColorDim, cli.ColorReset)
		return nil
	case "status":
		home := config.Home()
		w := cli.Writer(out)
		if runtime.GOOS == "linux" {
			if outB, err := exec.Command("systemctl", "status", "renstra", "--no-pager", "-n", "5").CombinedOutput(); err == nil {
				fmt.Fprintln(w, string(outB))
				return nil
			}
		}
		if pid, alive := readLivePID(home); alive {
			fmt.Fprintf(w, "%s● renstra serve aktif (PID %d, home %s)%s\n", cli.ColorGreen, pid, home, cli.ColorReset)
		} else {
			fmt.Fprintf(w, "%s○ renstra serve tidak berjalan (home %s)%s\n", cli.ColorYellow, home, cli.ColorReset)
		}
		return nil
	default:
		return fmt.Errorf("pemakaian: renstra service [status|restart|reload|start|stop]")
	}
}

func (h *telegramHandler) handleServiceCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	sub := strings.ToLower(strings.TrimSpace(arg))
	if sub == "restart" || sub == "reload" {
		go func() {
			time.Sleep(300 * time.Millisecond)
			_ = triggerSelfRestart(config.Home(), chatID, "Perintah Telegram /service "+sub)
		}()
		return "🔄 Me-restart service RENSTRA & memuat ulang konfigurasi baru... Kabar konfirmasi akan dikirim ke chat ini setelah proses baru aktif.", nil
	}
	model := h.runnerForChat(ctx, chatID).GetModel()
	dash := "nonaktif"
	if h.dashAddr != "" {
		dash = "`http://" + h.dashAddr + "`"
	}
	text := fmt.Sprintf(
		"⚙️ **Status Service RENSTRA**\n• **Versi:** `%s` (PID `%d`)\n• **Model Aktif:** `%s`\n• **Dashboard:** %s\n\nKetik `/service restart` untuk me-restart proses & memuat ulang `config.yaml`.",
		version, os.Getpid(), model, dash,
	)
	if h.bot != nil {
		rows := [][]telegram.InlineButton{
			{
				{Text: "🔄 Restart Service", CallbackData: "svc:restart", Style: "primary"},
				{Text: "⬅️ Menu", CallbackData: "cmd:menu", Style: "primary"},
			},
		}
		if err := h.bot.SendMessageWithKeyboard(ctx, chatID, text, rows); err == nil {
			return "", nil
		}
	}
	return text, nil
}

func (h *telegramHandler) handleUsageCmd(ctx context.Context) (string, error) {
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
}
