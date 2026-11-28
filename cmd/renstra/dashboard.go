package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/dashboard"
	"renstra/internal/mcp"
	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/store"

	"os/signal"
	"path/filepath"
	"syscall"
)

type serveFlags struct {
	DashPort int
	DashAddr string
	NoDash   bool
}

func parseServeFlags(args []string) (serveFlags, error) {
	var f serveFlags
	if p := strings.TrimSpace(os.Getenv("RENSTRA_DASHBOARD_PORT")); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			f.DashPort = n
		}
	}
	if a := strings.TrimSpace(os.Getenv("RENSTRA_DASHBOARD_ADDR")); a != "" {
		f.DashAddr = a
	}
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--no-dashboard":
			f.NoDash = true
		case args[i] == "--dashboard-port" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 || n > 65535 {
				return f, fmt.Errorf("--dashboard-port tidak valid: %q", args[i])
			}
			f.DashPort = n
		case strings.HasPrefix(args[i], "--dashboard-port="):
			val := strings.TrimPrefix(args[i], "--dashboard-port=")
			n, err := strconv.Atoi(val)
			if err != nil || n <= 0 || n > 65535 {
				return f, fmt.Errorf("--dashboard-port tidak valid: %q", val)
			}
			f.DashPort = n
		case args[i] == "--dashboard-addr" && i+1 < len(args):
			i++
			f.DashAddr = args[i]
		case strings.HasPrefix(args[i], "--dashboard-addr="):
			f.DashAddr = strings.TrimPrefix(args[i], "--dashboard-addr=")
		default:
			return f, fmt.Errorf("flag tidak dikenal pada serve: %s", args[i])
		}
	}
	return f, nil
}

func startDashboardServer(ctx context.Context, cfg *config.Config, flags serveFlags, home string, st *store.Store, mem *memory.Store, skills *skill.Registry, runner *agent.Runner, handler *telegramHandler, logHub *dashboard.LogHub, logger *slog.Logger) string {
	if flags.NoDash {
		return ""
	}
	addr, enabled := cfg.DashboardListenAddr(flags.DashPort, flags.DashAddr)
	if !enabled || addr == "" {
		return ""
	}
	token := strings.TrimSpace(cfg.Dashboard.Token)
	if envTok := strings.TrimSpace(os.Getenv("RENSTRA_DASHBOARD_TOKEN")); envTok != "" {
		token = envTok
	}
	if token == "" && !strings.HasPrefix(addr, "127.0.0.1:") && !strings.HasPrefix(addr, "localhost:") && !strings.HasPrefix(addr, "[::1]:") {
		logger.Warn("dashboard_public_without_token", "addr", addr, "hint", "set dashboard.token di config.yaml atau RENSTRA_DASHBOARD_TOKEN agar akses VPS aman")
	}

	srv, err := dashboard.New(dashboard.Options{
		Addr:      addr,
		Token:     token,
		Version:   version,
		HomeDir:   home,
		Store:     st,
		Memory:    mem,
		Skills:    skills,
		Config:    cfg,
		LogHub:    logHub,
		StartedAt: time.Now(),
		GetActiveModel: func() string {
			return runner.GetModel()
		},
		GetProviderKind: func() string {
			if pcfg, _, err := config.Resolve(cfg.DefaultModel, cfg); err == nil && pcfg.Kind != "" {
				return pcfg.Kind
			}
			return "openai"
		},
		GetContextWindow: func() int {
			return runner.ContextWindow
		},
		GetFallbackStatus: func() []dashboard.FallbackStatusItem {
			items := runner.FallbackStatus()
			out := make([]dashboard.FallbackStatusItem, 0, len(items))
			for _, it := range items {
				cd := ""
				if it.CooldownLeft > 0 {
					cd = it.CooldownLeft.Truncate(time.Second).String()
				}
				out = append(out, dashboard.FallbackStatusItem{
					Provider:      it.Provider,
					Model:         it.Model,
					Failures:      it.Failures,
					CooldownLeft:  cd,
					LastError:     it.LastError,
					LastKind:      string(it.LastKind),
					ContextWindow: it.ContextWindow,
					Vision:        it.Vision,
				})
			}
			return out
		},
		ResetFallbacks: func() {
			runner.ResetFallbacks()
		},
		InvalidatePrompts: func() {
			handler.invalidateAllPrompts()
		},
		SetDefaultModelRun: func(ref string) error {
			if _, err := handler.switchGlobalModel(context.Background(), ref); err != nil {
				return err
			}
			return nil
		},
		ReloadConfig: func(updated *config.Config) {
			handler.cfg = updated
			runner.Fallbacks = buildFallbacks(updated)
		},
		ReloadMCP: func() {
			mctx, mcancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer mcancel()
			servers, _ := mcp.ConnectAll(mctx, loadMCPConfigs())
			runner.Tools.RemovePrefix("mcp__")
			for _, s := range servers {
				for _, t := range s.Tools {
					runner.Tools.Add(t)
				}
			}
			handler.mu.Lock()
			for _, cr := range handler.chats {
				cr.Tools.RemovePrefix("mcp__")
				for _, s := range servers {
					for _, t := range s.Tools {
						cr.Tools.Add(t)
					}
				}
			}
			handler.mu.Unlock()
			handler.invalidateAllPrompts()
		},
		RestartService: func(reason string) error {
			return triggerSelfRestart(home, 0, reason)
		},
		SwitchChatModel: func(mctx context.Context, chatID int64, ref string) error {
			_, err := handler.handleModelCmd(mctx, chatID, ref)
			return err
		},
		RunChat: func(rctx context.Context, chatID int64, prompt string) (string, error) {
			return handler.Handle(agent.WithUnattended(rctx), chatID, prompt)
		},
	})
	if err != nil {
		logger.Error("dashboard_init_failed", "addr", addr, "error", err.Error())
		return ""
	}
	go func() {
		if err := srv.Run(ctx); err != nil {
			logger.Error("dashboard_stopped", "addr", addr, "error", err.Error())
		}
	}()
	logger.Info("dashboard_started", "addr", addr, "auth", token != "")
	return addr
}

// dashboardCmd runs the Web Dashboard standalone without requiring a Telegram bot token.
func dashboardCmd(args []string) error {
	var flags serveFlags
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--port" && i+1 < len(args), args[i] == "--dashboard-port" && i+1 < len(args):
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 || n > 65535 {
				return fmt.Errorf("port tidak valid: %q", args[i])
			}
			flags.DashPort = n
		case strings.HasPrefix(args[i], "--port="):
			n, err := strconv.Atoi(strings.TrimPrefix(args[i], "--port="))
			if err != nil || n <= 0 || n > 65535 {
				return fmt.Errorf("port tidak valid")
			}
			flags.DashPort = n
		case args[i] == "--addr" && i+1 < len(args), args[i] == "--dashboard-addr" && i+1 < len(args):
			i++
			flags.DashAddr = args[i]
		default:
			return fmt.Errorf("flag tidak dikenal: %s", args[i])
		}
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	home := config.Home()
	workspace := filepath.Join(home, "workspace")
	_ = os.MkdirAll(workspace, 0o755)
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	memStore, _ := memory.Open(filepath.Join(home, "memory"))
	skillReg, _ := skill.Load(
		skill.Layer{Dir: filepath.Join(workspace, ".skills"), Writable: false},
		skill.Layer{Dir: filepath.Join(workspace, "skills"), Writable: false},
		skill.Layer{Dir: filepath.Join(home, "skills"), Writable: true},
		skill.Layer{Dir: filepath.Join(globalBaseDir(), "skills"), Writable: false},
	)

	logHub := dashboard.NewLogHub(filepath.Join(home, "logs"), 1000, os.Stderr)
	logger := slog.New(logHub)
	logger.Info("dashboard_standalone_ready", "version", version, "home", home)

	addr, _ := cfg.DashboardListenAddr(flags.DashPort, flags.DashAddr)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	srv, err := dashboard.New(dashboard.Options{
		Addr:             addr,
		Token:            strings.TrimSpace(cfg.Dashboard.Token),
		Version:          version,
		HomeDir:          home,
		Store:            st,
		Memory:           memStore,
		Skills:           skillReg,
		Config:           cfg,
		LogHub:           logHub,
		StartedAt:        time.Now(),
		GetActiveModel:  func() string { return cfg.DefaultModel },
		GetProviderKind: func() string { return "standalone" },
		GetContextWindow: func() int {
			if pcfg, m, err := config.Resolve(cfg.DefaultModel, cfg); err == nil {
				return pcfg.ModelConfig(m).Context
			}
			return 128000
		},
		SetDefaultModelRun: func(ref string) error {
			_, storedRef, err := config.SetDefaultModel(ref)
			if err != nil {
				return err
			}
			if st != nil {
				_ = st.SetGlobalModel(context.Background(), storedRef)
			}
			if fresh, err := config.Load(); err == nil {
				cfg = fresh
			}
			return nil
		},
	})
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Printf("RENSTRA Web Dashboard berjalan di http://%s (Ctrl+C untuk berhenti)\n", addr)
	return srv.Run(ctx)
}

func backupCmd(args []string) error {
	home := config.Home()
	outPath := fmt.Sprintf("renstra-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		outPath = strings.TrimSpace(args[0])
	}
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()
	f, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := st.BackupArchive(context.Background(), home, f); err != nil {
		return err
	}
	fmt.Printf("✅ Backup tersimpan di %s\n", outPath)
	return nil
}

func restoreCmd(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
		return fmt.Errorf("pemakaian: renstra restore <file-backup.tar.gz>")
	}
	inPath := strings.TrimSpace(args[0])
	f, err := os.Open(inPath)
	if err != nil {
		return err
	}
	defer f.Close()
	home := config.Home()
	files, err := store.RestoreArchive(home, f)
	if err != nil {
		return err
	}
	fmt.Printf("✅ Restore selesai ke %s (%d berkas dipulihkan)\n", home, len(files))
	return nil
}

func (h *telegramHandler) handleBackupCmd(ctx context.Context, chatID int64) (string, error) {
	if h.store == nil {
		return "❌ Store tidak tersedia.", nil
	}
	fname := fmt.Sprintf("renstra-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	tmpPath := filepath.Join(os.TempDir(), fname)
	defer os.Remove(tmpPath)
	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	err = h.store.BackupArchive(ctx, config.Home(), f)
	_ = f.Close()
	if err != nil {
		return "", err
	}
	if h.bot != nil {
		caption := fmt.Sprintf("📦 **RENSTRA Backup** (`%s`)\nBerisi `state.db`, `config.yaml`, `mcp.yaml`, `memory/`, dan `skills/`.", version)
		if err := h.bot.SendDocument(ctx, chatID, tmpPath, caption); err != nil {
			return "", err
		}
		return "", nil
	}
	return "✅ Backup berhasil dibuat: `" + fname + "`", nil
}
