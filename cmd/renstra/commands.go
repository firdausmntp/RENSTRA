package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/cron"
	"renstra/internal/mcp"
	"renstra/internal/memory"
	"renstra/internal/migrate"
	"renstra/internal/provider"
	"renstra/internal/provider/anthropic"
	"renstra/internal/provider/google"
	"renstra/internal/provider/groq"
	"renstra/internal/provider/mistral"
	"renstra/internal/provider/ollama"
	"renstra/internal/provider/openai"
	"renstra/internal/provider/openrouter"
	"renstra/internal/provider/xai"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/telegram"
	"renstra/internal/tool"
)

func migrateCmd(args []string) error {
	if len(args) == 0 || args[0] != "hermes" {
		return fmt.Errorf("pemakaian: renstra migrate hermes [--from <dir>] [--dry-run] [--force]")
	}
	var fromDir string
	var dryRun, force bool
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--dry-run":
			dryRun = true
		case "--force":
			force = true
		case "--from":
			if i+1 >= len(rest) {
				return fmt.Errorf("--from butuh path direktori")
			}
			i++
			fromDir = rest[i]
		default:
			if strings.HasPrefix(rest[i], "--from=") {
				fromDir = strings.TrimPrefix(rest[i], "--from=")
			} else {
				return fmt.Errorf("flag tidak dikenal: %s", rest[i])
			}
		}
	}

	rep, err := migrate.FromHermes(context.Background(), migrate.Options{
		FromDir: fromDir,
		ToDir:   config.Home(),
		DryRun:  dryRun,
		Force:   force,
	})
	if err != nil {
		return err
	}

	mode := "SELESAI"
	if rep.DryRun {
		mode = "DRY-RUN (tidak ada perubahan ditulis)"
	}
	fmt.Printf("Migrasi Hermes -> RENSTRA [%s]\n", mode)
	fmt.Printf("  Sumber : %s\n", rep.SourceDir)
	fmt.Printf("  Tujuan : %s\n", rep.DestDir)
	fmt.Printf("  Config : written=%v\n", rep.ConfigWritten)
	fmt.Printf("  MCP    : written=%v (servers=%d)\n", rep.MCPWritten, rep.MCPServers)
	fmt.Printf("  Memory : MEMORY.md=%v, USER.md=%v\n", rep.MemoryCopied, rep.UserCopied)
	fmt.Printf("  Skills : disalin=%d, dilewati=%d\n", rep.SkillsCopied, rep.SkillsSkipped)
	fmt.Printf("  History: sesi=%d (dilewati=%d), pesan=%d\n", rep.SessionsCopied, rep.SessionsSkipped, rep.MessagesCopied)
	if len(rep.EnvVarsFound) > 0 {
		fmt.Printf("  Env API key ditemukan di .env Hermes (pastikan di-export): %s\n", strings.Join(rep.EnvVarsFound, ", "))
	}
	for _, s := range rep.SkippedItems {
		fmt.Printf("  [lewati] %s\n", s)
	}
	for _, w := range rep.Warnings {
		fmt.Printf("  [peringatan] %s\n", w)
	}
	return nil
}

func mcpCmd(args []string) error {
	cfgs := loadMCPConfigs()
	if len(cfgs) == 0 {
		fmt.Println("(belum ada server MCP terdaftar di mcp.yaml)")
		return nil
	}
	fmt.Printf("Server MCP terkonfigurasi (%d):\n", len(cfgs))
	for _, c := range cfgs {
		fmt.Printf("  %-15s cmd=%s args=%v\n", c.Name, c.Command, c.Args)
	}
	return nil
}

func loadMCPConfigs() []mcp.ServerConfig {
	home := config.Home()
	paths := []string{
		filepath.Join(home, "mcp.yaml"),
		filepath.Join(globalBaseDir(), "mcp.yaml"),
	}

	seen := make(map[string]bool)
	var out []mcp.ServerConfig

	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var doc struct {
			Servers map[string]struct {
				Command string            `yaml:"command"`
				Args    []string          `yaml:"args"`
				Env     []string          `yaml:"env"`
				URL     string            `yaml:"url"`
				Headers map[string]string `yaml:"headers"`
			} `yaml:"servers"`
		}
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			continue
		}
		for name, s := range doc.Servers {
			if seen[name] {
				continue // profile layer wins over global
			}
			seen[name] = true
			out = append(out, mcp.ServerConfig{
				Name:    name,
				Command: s.Command,
				Args:    s.Args,
				Env:     s.Env,
				URL:     s.URL,
				Headers: s.Headers,
			})
		}
	}
	return out
}

func modelListCmd() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gagal memuat config: %v\n", err)
	}

	fmt.Println("Provider aktif:")
	fmt.Println("  openai     (aktif) - endpoint OpenAI-compatible / custom")
	fmt.Println("  anthropic  (aktif) - Claude Messages API")
	for _, name := range []string{
		openrouter.Name(), groq.Name(), google.Name(),
		ollama.Name(), mistral.Name(), xai.Name(),
	} {
		fmt.Printf("  %-10s (aktif)\n", name)
	}

	if cfg != nil && len(cfg.Providers) > 0 {
		fmt.Println("\nProvider terkonfigurasi di config.yaml:")
		for name, p := range cfg.Providers {
			fmt.Printf("  %-12s kind=%-10s base=%s default=%s\n", name, p.Kind, p.BaseURL, p.DefaultModel)
		}
	}
	if cfg != nil && len(cfg.Aliases) > 0 {
		fmt.Println("\nAlias model:")
		for alias, target := range cfg.Aliases {
			fmt.Printf("  %-12s -> %s\n", alias, target)
		}
	}
}

func skillCmd(args []string) error {
	if len(args) == 0 {
		fmt.Println("pemakaian: renstra skill <list|add|rm> [--global] [argumen]")
		return nil
	}

	global := false
	var filtered []string
	for _, a := range args {
		if a == "--global" {
			global = true
		} else {
			filtered = append(filtered, a)
		}
	}

	home := config.Home()
	instanceDir := filepath.Join(home, "skills")
	globalDir := filepath.Join(globalBaseDir(), "skills")

	layers := []skill.Layer{
		{Dir: instanceDir, Writable: !global},
		{Dir: globalDir, Writable: global},
	}
	reg, err := skill.Load(layers...)
	if err != nil {
		return err
	}

	switch filtered[0] {
	case "list":
		idx := reg.Index()
		if idx == "" {
			fmt.Println("(belum ada skill terdaftar)")
			return nil
		}
		fmt.Print(idx)
		if len(reg.Conflicts()) > 0 {
			fmt.Println("\nKonflik nama (lapis atas menang):")
			for _, c := range reg.Conflicts() {
				fmt.Printf("  %s: %s (menutupi %s)\n", c.Name, c.WinnerDir, c.ShadowedDir)
			}
		}
		return nil
	case "add":
		dest := instanceDir
		if global {
			dest = globalDir
		}
		if len(filtered) == 2 {
			installed, err := installSkillFromSource(filtered[1], dest)
			if err != nil {
				return err
			}
			fmt.Printf("skill dipasang ke %s: %s\n", dest, strings.Join(installed, ", "))
			return nil
		}
		if len(filtered) < 4 {
			return fmt.Errorf("pemakaian: renstra skill add <dir|git-url> [--global] ATAU renstra skill add <name> <description> <body> [--global]")
		}
		name, desc, body := filtered[1], filtered[2], filtered[3]
		if err := reg.Create(name, desc, body); err != nil {
			return err
		}
		fmt.Printf("skill %q disimpan ke %s\n", name, dest)
		return nil
	case "rm":
		if len(filtered) < 2 {
			return fmt.Errorf("pemakaian: renstra skill rm <name> [--global]")
		}
		name := filtered[1]
		if err := reg.Delete(name); err != nil {
			return err
		}
		fmt.Printf("skill %q berhasil dihapus\n", name)
		return nil
	default:
		return fmt.Errorf("subcommand skill tidak dikenal: %s", filtered[0])
	}
}

func globalBaseDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(config.Home(), "global")
	}
	return filepath.Join(base, "renstra", "global")
}

func usageCmd() error {
	home := config.Home()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctx := context.Background()
	today := time.Now().Format("2006-01-02")
	dayUsage, err := st.UsageForDay(ctx, today)
	if err != nil {
		dayUsage = store.Usage{}
	}

	monthUsage, err := st.UsageSince(ctx, time.Now().AddDate(0, 0, -30))
	if err != nil {
		monthUsage = store.Usage{}
	}

	fmt.Printf("Pemakaian RENSTRA (%s):\n", home)
	fmt.Printf("  Hari ini (%s):\n", today)
	fmt.Printf("    Tokens in  : %d\n", dayUsage.TokensIn)
	fmt.Printf("    Tokens out : %d\n", dayUsage.TokensOut)
	fmt.Printf("    Biaya est  : $%.4f\n\n", dayUsage.CostUSD)
	fmt.Println("  30 hari terakhir:")
	fmt.Printf("    Tokens in  : %d\n", monthUsage.TokensIn)
	fmt.Printf("    Tokens out : %d\n", monthUsage.TokensOut)
	fmt.Printf("    Biaya est  : $%.4f\n", monthUsage.CostUSD)
	return nil
}

// serveCmd runs the Telegram gateway + cron runner concurrently.
func serveCmd() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	home := config.Home()
	cfgPath := filepath.Join(home, "config.yaml")

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Read Telegram token and allowed_users from config
	token := cfg.Telegram.Token
	allowedUsers := cfg.Telegram.AllowedUsers

	if token == "" {
		return fmt.Errorf("telegram.token belum diset di %s; jalankan `renstra setup`", cfgPath)
	}
	if len(allowedUsers) == 0 {
		logger.Warn("telegram_allowed_users_empty", "hint", "bot akan menolak semua pesan masuk hingga telegram.allowed_users diisi")
	}

	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pollTimeout := time.Duration(cfg.Telegram.PollTimeout)
	if pollTimeout == 0 {
		pollTimeout = 30 * time.Second
	}

	workspace := filepath.Join(home, "workspace")
	_ = os.MkdirAll(workspace, 0o755)
	pruneRetention(cfg, home, workspace)

	// Seed bot with recently seen updates so a restart never answers twice.
	_ = st.PruneUpdatesSeen(ctx)
	recentIDs, _ := st.RecentUpdateIDs(ctx, 500)
	bot, err := telegram.New(telegram.Config{
		Token:        token,
		AllowedUsers: allowedUsers,
		PollTimeout:  pollTimeout,
		WorkspaceDir: workspace,
		OnSeen: func(id int64) {
			sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer scancel()
			_ = st.MarkUpdateSeen(sctx, id)
		},
		Transcribe: buildTranscriber(cfg),
	})
	if err != nil {
		return err
	}
	for _, id := range recentIDs {
		bot.MarkSeen(id)
	}

	// Prepare provider, tools, memory, skills
	pcfg, model, err := config.Resolve(cfg.DefaultModel, cfg)
	if err != nil {
		return err
	}
	prov, err := buildProvider(pcfg)
	if err != nil {
		return err
	}

	memStore, err := memory.Open(filepath.Join(home, "memory"))
	if err != nil {
		return err
	}
	skillReg, err := skill.Load(
		skill.Layer{Dir: filepath.Join(workspace, ".skills"), Writable: false},
		skill.Layer{Dir: filepath.Join(workspace, "skills"), Writable: false},
		skill.Layer{Dir: filepath.Join(home, "skills"), Writable: true},
		skill.Layer{Dir: filepath.Join(globalBaseDir(), "skills"), Writable: false},
	)
	if err != nil {
		return err
	}

	mc := pcfg.ModelConfig(model)
	auxProv, auxModel, auxIn, auxOut, auxErr := resolveAuxTarget(cfg, prov, model, mc)
	if auxErr != nil {
		logger.Warn("aux_model_invalid", "aux_model", cfg.Agent.AuxModel, "error", auxErr.Error())
	}

	runner := &agent.Runner{
		Provider:           prov,
		Tools:              tool.NewRegistry(),
		Store:              st,
		Model:              model,
		AuxModel:           auxModel,
		AuxProvider:        auxProv,
		AuxPriceIn:         auxIn,
		AuxPriceOut:        auxOut,
		Cwd:                workspace,
		MaxIters:           cfg.Agent.MaxIterations,
		MaxTokens:          mc.MaxOutput,
		PriceIn:            mc.PriceIn,
		PriceOut:           mc.PriceOut,
		ContextWindow:      mc.Context,
		AutoCompact:        cfg.Agent.AutoCompaction(),
		DisableAutoCompact: !cfg.Agent.AutoCompaction(),
		Threshold:          cfg.Agent.Threshold(),
		Vision:             mc.Vision,
		ToolsDisabled:      mc.Tools != nil && !*mc.Tools,
		TurnTimeout:        time.Duration(cfg.Agent.MaxTurnTimeout),
		Limits:             cfg.Limits,
		PromptPrefix:       cfg.Agent.SystemPromptPrefix,
		ApprovalCfg:        cfg.Approval,
		Fallbacks:          buildFallbacks(cfg),
		Memory:             memStore,
		Skills:             skillReg,
		Logger:             logger,
	}

	checkpointsDir := filepath.Join(home, "checkpoints")
	wfTool := tool.NewWriteFile(workspace)
	wfTool.BackupDir = checkpointsDir
	patchTool := tool.NewPatch(workspace)
	patchTool.BackupDir = checkpointsDir
	webFetchTool := tool.NewWebFetch()
	webFetchTool.AllowPrivate = cfg.Tools.AllowPrivateFetch

	candidates := []tool.Tool{
		tool.NewTerminal(workspace, 0),
		tool.NewReadFile(workspace),
		wfTool,
		patchTool,
		webFetchTool,
		tool.NewWebSearch(),
		tool.NewMemory(memStore),
		tool.NewSkill(skillReg),
		tool.NewCron(st, 0),
		tool.NewDelegate(runner, 0),
		tool.NewSessionSearch(st),
		tool.NewSendFile(workspace, bot),
	}
	for _, t := range candidates {
		if cfg.Tools.IsEnabled(t.Name()) {
			runner.Tools.Add(t)
		}
	}

	// Connect MCP servers with startup timeout so an unreachable server never stalls boot.
	mcpCtx, mcpCancel := context.WithTimeout(ctx, 15*time.Second)
	mcpServers, mcpErrors := mcp.ConnectAll(mcpCtx, loadMCPConfigs())
	mcpCancel()
	for _, s := range mcpServers {
		defer s.Client.Close()
		for _, t := range s.Tools {
			runner.Tools.Add(t)
		}
	}
	for _, merr := range mcpErrors {
		fmt.Fprintf(os.Stderr, "peringatan MCP: %v\n", merr)
	}

	approvalWait := time.Duration(cfg.Approval.Timeout)
	if approvalWait == 0 {
		approvalWait = 120 * time.Second
	}

	handler := &telegramHandler{
		bot:             bot,
		runner:          runner,
		store:           st,
		cfg:             cfg,
		approvalTimeout: approvalWait,
	}
	runner.Approve = func(actx context.Context, name string, args json.RawMessage) (bool, error) {
		cid := tool.ChatIDFrom(actx)
		if cid <= 0 {
			return false, nil
		}
		return handler.Approve(actx, cid, name, string(args))
	}

	if interruptedChats, err := st.RecoverInterruptedTurns(ctx); err == nil {
		for _, cid := range interruptedChats {
			_ = bot.SendMessage(ctx, cid, "ℹ️ Bot baru saja dimulai ulang. Proses yang sedang berjalan sebelumnya terhenti.")
		}
	}
	if err := bot.SetMyCommands(ctx, telegram.DefaultCommands()); err != nil {
		logger.Warn("telegram_set_my_commands_failed", "error", err.Error())
	}

	// Cron runner in background with panic recovery and isolated Runner clone.
	cronRunner := runner.Clone()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "panic di cron: %v\n", r)
			}
		}()
		_ = cron.Run(ctx, st, time.Local, func(cctx context.Context, job cron.Job) {
			defer func() {
				if r := recover(); r != nil {
					_ = bot.SendMessage(cctx, job.ChatID, fmt.Sprintf("[cron %d panic]: %v", job.ID, r))
				}
			}()
			ans, err := cronRunner.Run(agent.WithUnattended(cctx), job.ChatID, job.Prompt)
			if err != nil {
				_ = bot.SendMessage(cctx, job.ChatID, fmt.Sprintf("[cron %d error]: %v", job.ID, err))
				return
			}
			_ = bot.SendMessage(cctx, job.ChatID, ans)
		}, 30*time.Second)
	}()

	fmt.Printf("renstra gateway berjalan (model: %s). Tekan Ctrl+C untuk berhenti.\n", model)
	return bot.Run(ctx, handler)
}

type telegramHandler struct {
	bot             *telegram.Bot
	runner          *agent.Runner
	store           *store.Store
	cfg             *config.Config
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
	}
	return cr
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
	// A panic inside a tool must not take the gateway down with it.
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
	case "/new", "/reset":
		if err := h.store.SessionBreak(ctx, chatID); err != nil {
			return "", fmt.Errorf("gagal reset sesi: %w", err)
		}
		h.invalidateAllPrompts()
		return "Sesi baru dimulai. Catatan memori di-freeze ulang dan riwayat diputus.", nil

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
		return fmt.Sprintf("Kompaksi selesai.\nRingkasan konteks:\n%s", summary), nil

	case "/model":
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

	case "/skills":
		if h.runner.Skills == nil {
			return "Skill registry tidak aktif.", nil
		}
		reloaded := false
		if strings.EqualFold(arg, "reload") {
			h.invalidateAllPrompts()
			reloaded = true
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
		}
		if conflicts := h.runner.Skills.Conflicts(); len(conflicts) > 0 {
			b.WriteString("\n\nKonflik nama (lapis atas menang):\n")
			for _, c := range conflicts {
				fmt.Fprintf(&b, "- %s: %s (menutupi %s)\n", c.Name, c.WinnerDir, c.ShadowedDir)
			}
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "/memory":
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

	case "/cron":
		return h.handleCronCmd(ctx, chatID, arg)
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

func (h *telegramHandler) handleCronCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	sub := "list"
	rest := ""
	if arg != "" {
		parts := strings.SplitN(arg, " ", 2)
		sub = strings.ToLower(parts[0])
		if len(parts) > 1 {
			rest = strings.TrimSpace(parts[1])
		}
	}

	switch sub {
	case "list":
		jobs, err := h.store.ListJobs(ctx, chatID)
		if err != nil {
			return "", err
		}
		if len(jobs) == 0 {
			return "(tidak ada jadwal cron aktif)", nil
		}
		var b strings.Builder
		b.WriteString("Daftar jadwal cron:\n")
		for _, j := range jobs {
			status := "aktif"
			if !j.Enabled {
				status = "nonaktif"
			}
			fmt.Fprintf(&b, "- ID %d [%s]: %s -> %q (next: %s)\n", j.ID, status, j.Schedule, j.Prompt, j.NextRunAt)
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "add":
		parts := strings.SplitN(rest, "|", 2)
		if len(parts) != 2 {
			return "Format: `/cron add <schedule> | <prompt>`\nContoh: `/cron add 0 9 * * * | ringkas berita`", nil
		}
		sched := strings.TrimSpace(parts[0])
		prompt := strings.TrimSpace(parts[1])
		if sched == "" || prompt == "" {
			return "Format: `/cron add <schedule> | <prompt>`\nSchedule dan prompt tidak boleh kosong.", nil
		}
		next, err := cron.NextAfter(sched, time.Now(), time.Local)
		if err != nil {
			return "", fmt.Errorf("jadwal cron tidak valid: %w", err)
		}
		id, err := h.store.AddJob(ctx, chatID, sched, prompt)
		if err != nil {
			return "", err
		}
		_ = h.store.UpdateJobRun(ctx, id, next.Format(time.RFC3339), "")
		return fmt.Sprintf("Jadwal cron ditambahkan (ID %d): `%s`\nBerikutnya: %s", id, sched, next.Format(time.RFC3339)), nil

	case "rm", "remove":
		id, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil || id <= 0 {
			return "Pemakaian: `/cron rm <id>`", nil
		}
		if err := h.store.RemoveJob(ctx, id); err != nil {
			return "", err
		}
		return fmt.Sprintf("Jadwal ID %d berhasil dihapus.", id), nil

	case "toggle", "pause":
		id, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil || id <= 0 {
			return "Pemakaian: `/cron toggle <id>`", nil
		}
		jobs, err := h.store.ListJobs(ctx, chatID)
		if err != nil {
			return "", err
		}
		var target *store.Job
		for i := range jobs {
			if jobs[i].ID == id {
				target = &jobs[i]
				break
			}
		}
		if target == nil {
			return "", fmt.Errorf("jadwal ID %d tidak ditemukan", id)
		}
		newState := !target.Enabled
		if err := h.store.SetJobEnabled(ctx, id, newState); err != nil {
			return "", err
		}
		if newState {
			return fmt.Sprintf("Jadwal ID %d diaktifkan.", id), nil
		}
		return fmt.Sprintf("Jadwal ID %d dinonaktifkan.", id), nil

	default:
		return "Subcommand cron: `/cron list`, `/cron add <schedule> | <prompt>`, `/cron toggle <id>`, `/cron rm <id>`", nil
	}
}

func (h *telegramHandler) Approve(ctx context.Context, chatID int64, toolName, args string) (bool, error) {
	timeout := h.approvalTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return h.bot.AskApproval(ctx, chatID, toolName, args, timeout)
}

func buildProvider(pcfg config.ProviderConfig) (provider.Provider, error) {
	apiKey := ""
	if pcfg.APIKeyEnv != "" {
		apiKey = os.Getenv(pcfg.APIKeyEnv)
	}
	switch pcfg.Kind {
	case "", "openai", "custom":
		return openai.New(pcfg, apiKey)
	case "anthropic":
		return anthropic.New(pcfg, apiKey)
	case "openrouter":
		return openrouter.New(pcfg, apiKey)
	case "groq":
		return groq.New(pcfg, apiKey)
	case "ollama":
		return ollama.New(pcfg, apiKey)
	case "mistral":
		return mistral.New(pcfg, apiKey)
	case "xai":
		return xai.New(pcfg, apiKey)
	case "google":
		return google.New(pcfg, apiKey)
	default:
		return nil, fmt.Errorf("provider kind %q tidak dikenal", pcfg.Kind)
	}
}

func buildFallbacks(cfg *config.Config) []agent.FallbackTarget {
	var targets []agent.FallbackTarget
	for _, ref := range cfg.Fallbacks {
		pcfg, model, err := config.Resolve(ref, cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "peringatan fallback %q tidak valid: %v\n", ref, err)
			continue
		}
		prov, err := buildProvider(pcfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "peringatan build provider fallback %q: %v\n", ref, err)
			continue
		}
		mc := pcfg.ModelConfig(model)
		targets = append(targets, agent.FallbackTarget{
			Provider:      prov,
			Model:         model,
			MaxTokens:     mc.MaxOutput,
			PriceIn:       mc.PriceIn,
			PriceOut:      mc.PriceOut,
			ContextWindow: mc.Context,
			Vision:        mc.Vision,
			ToolsDisabled: mc.Tools != nil && !*mc.Tools,
		})
	}
	return targets
}

func resolveAuxTarget(cfg *config.Config, primaryProv provider.Provider, primaryModel string, primaryMC config.ModelConfig) (provider.Provider, string, float64, float64, error) {
	if cfg == nil || strings.TrimSpace(cfg.Agent.AuxModel) == "" {
		return primaryProv, primaryModel, primaryMC.PriceIn, primaryMC.PriceOut, nil
	}
	auxPCfg, auxModel, err := config.Resolve(cfg.Agent.AuxModel, cfg)
	if err != nil {
		return primaryProv, primaryModel, primaryMC.PriceIn, primaryMC.PriceOut, err
	}
	auxProv, err := buildProvider(auxPCfg)
	if err != nil {
		return primaryProv, primaryModel, primaryMC.PriceIn, primaryMC.PriceOut, err
	}
	auxMC := auxPCfg.ModelConfig(auxModel)
	return auxProv, auxModel, auxMC.PriceIn, auxMC.PriceOut, nil
}

func pruneRetention(cfg *config.Config, home, workspace string) {
	days := 14
	if cfg != nil && cfg.Agent.FileRetentionDays > 0 {
		days = cfg.Agent.FileRetentionDays
	} else if cfg != nil && cfg.Agent.FileRetentionDays < 0 {
		return
	}
	maxAge := time.Duration(days) * 24 * time.Hour
	for _, dir := range []string{
		filepath.Join(workspace, "inbox"),
		filepath.Join(workspace, ".tool-output"),
		filepath.Join(home, "checkpoints"),
	} {
		_, _ = tool.PruneOldFiles(dir, maxAge)
	}
}

func buildTranscriber(cfg *config.Config) func(context.Context, string) (string, error) {
	if cfg == nil {
		return nil
	}
	var baseURL, apiKey, model string
	for _, p := range cfg.Providers {
		kind := strings.ToLower(strings.TrimSpace(p.Kind))
		if kind != "openai" && kind != "groq" && kind != "" {
			continue
		}
		key := ""
		if p.APIKeyEnv != "" {
			key = os.Getenv(p.APIKeyEnv)
		}
		if key == "" && kind == "openai" {
			key = os.Getenv("OPENAI_API_KEY")
		}
		if key == "" && kind == "groq" {
			key = os.Getenv("GROQ_API_KEY")
		}
		if key == "" {
			continue
		}
		baseURL = strings.TrimRight(p.BaseURL, "/")
		if baseURL == "" {
			if kind == "groq" {
				baseURL = "https://api.groq.com/openai/v1"
			} else {
				baseURL = "https://api.openai.com/v1"
			}
		}
		apiKey = key
		if kind == "groq" {
			model = "whisper-large-v3-turbo"
		} else {
			model = "whisper-1"
		}
		break
	}
	if apiKey == "" {
		return nil
	}
	return func(ctx context.Context, audioPath string) (string, error) {
		return transcribeAudioFile(ctx, baseURL, apiKey, model, audioPath)
	}
}

func transcribeAudioFile(ctx context.Context, baseURL, apiKey, model, audioPath string) (string, error) {
	data, err := os.ReadFile(audioPath)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(audioPath))
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(data); err != nil {
		return "", err
	}
	if err := mw.WriteField("model", model); err != nil {
		return "", err
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("transcribe HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}

func installSkillFromSource(src, destDir string) ([]string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return nil, fmt.Errorf("sumber skill kosong")
	}
	isGit := strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") ||
		strings.HasPrefix(src, "git@") || strings.HasSuffix(src, ".git")
	sourceRoot := src
	if isGit {
		tmp, err := os.MkdirTemp("", "renstra-skill-*")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(tmp)
		cmd := exec.Command("git", "clone", "--depth", "1", src, tmp)
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git clone %s gagal: %w (%s)", src, err, strings.TrimSpace(string(out)))
		}
		sourceRoot = tmp
	}

	info, err := os.Stat(sourceRoot)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("direktori skill tidak ditemukan: %s", src)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}

	var candidates []string
	if _, err := os.Stat(filepath.Join(sourceRoot, "SKILL.md")); err == nil {
		candidates = append(candidates, sourceRoot)
	} else {
		for _, searchDir := range []string{sourceRoot, filepath.Join(sourceRoot, "skills")} {
			entries, rerr := os.ReadDir(searchDir)
			if rerr != nil {
				continue
			}
			for _, e := range entries {
				if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				sub := filepath.Join(searchDir, e.Name())
				if _, serr := os.Stat(filepath.Join(sub, "SKILL.md")); serr == nil {
					candidates = append(candidates, sub)
				}
			}
			if len(candidates) > 0 {
				break
			}
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("tidak ditemukan SKILL.md di %s", src)
	}

	var installed []string
	for _, cand := range candidates {
		name := filepath.Base(cand)
		target := filepath.Join(destDir, name)
		if err := copySkillTree(cand, target); err != nil {
			return nil, err
		}
		installed = append(installed, name)
	}
	sort.Strings(installed)
	return installed, nil
}

func copySkillTree(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		target := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
