package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gopkg.in/yaml.v3"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/cron"
	"renstra/internal/mcp"
	"renstra/internal/memory"
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
	fmt.Println("  openai     (aktif)   - endpoint OpenAI-compatible / local")
	fmt.Println("  anthropic  (aktif)   - Claude Messages API")
	fmt.Println("\nProvider lain (stub, siap diaktifkan di v1.1):")
	for _, name := range []string{
		openrouter.Name(), groq.Name(), google.Name(),
		ollama.Name(), mistral.Name(), xai.Name(),
	} {
		fmt.Printf("  %-10s (stub)    - belum diimplementasikan\n", name)
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
		if len(filtered) < 4 {
			return fmt.Errorf("pemakaian: renstra skill add <name> <description> <body> [--global]")
		}
		name, desc, body := filtered[1], filtered[2], filtered[3]
		if err := reg.Create(name, desc, body); err != nil {
			return err
		}
		dest := instanceDir
		if global {
			dest = globalDir
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

// setupCmd guides a user through creating their config.yaml.
func setupCmd() error {
	reader := bufio.NewReader(os.Stdin)
	home := config.Home()
	cfgPath := filepath.Join(home, "config.yaml")

	fmt.Println("=== RENSTRA Setup Wizard ===")
	fmt.Printf("Config akan disimpan ke: %s\n\n", cfgPath)

	token := promptString(reader, "Telegram Bot Token", "")
	providerKind := promptString(reader, "Provider kind (openai / anthropic)", "openai")
	baseURL := promptString(reader, "Provider Base URL (kosong = bawaan provider)", "")
	apiKeyEnv := promptString(reader, "Nama env variable untuk API key", strings.ToUpper(providerKind)+"_API_KEY")
	defaultModel := promptString(reader, "Model default", "gpt-4o-mini")
	allowedUserStr := promptString(reader, "Telegram User ID kamu (angka, pisahkan koma)", "")
	approvalMode := promptString(reader, "Mode approval (prompt / auto / deny)", "prompt")
	promptPrefix := promptString(reader, "System prompt prefix (kosong = default)", "")

	if baseURL == "" {
		if providerKind == "anthropic" {
			baseURL = "https://api.anthropic.com"
		} else {
			baseURL = "https://api.openai.com/v1"
		}
	}

	var allowedUsers []int64
	for _, part := range strings.Split(allowedUserStr, ",") {
		part = strings.TrimSpace(part)
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			allowedUsers = append(allowedUsers, id)
		}
	}

	_ = os.MkdirAll(home, 0o700)

	var b strings.Builder
	b.WriteString("# RENSTRA configuration\n")
	b.WriteString("# Edit file ini untuk menyesuaikan perilaku agent.\n\n")

	b.WriteString("default_model: " + yamlQuote("utama:"+defaultModel) + "\n\n")

	b.WriteString("providers:\n")
	b.WriteString("  utama:\n")
	fmt.Fprintf(&b, "    kind: %s\n", yamlQuote(providerKind))
	fmt.Fprintf(&b, "    base_url: %s\n", yamlQuote(baseURL))
	fmt.Fprintf(&b, "    api_key_env: %s\n", yamlQuote(apiKeyEnv))
	fmt.Fprintf(&b, "    default_model: %s\n", yamlQuote(defaultModel))
	b.WriteString("    # path: /chat/completions     # override endpoint bila perlu\n")
	b.WriteString("    # proxy: \"http://127.0.0.1:8080\"\n")
	b.WriteString("    # headers:\n")
	b.WriteString("    #   X-Custom-Header: value\n")
	b.WriteString("    # models:\n")
	b.WriteString("    #   " + defaultModel + ":\n")
	b.WriteString("    #     context: 128000\n")
	b.WriteString("    #     max_output: 4096\n")
	b.WriteString("    #     price_in: 0.15\n")
	b.WriteString("    #     price_out: 0.60\n\n")

	b.WriteString("aliases: {}\n\n")

	b.WriteString("# --- Approval ---\n")
	b.WriteString("# mode: prompt = tanya lewat inline keyboard, auto = izinkan semua,\n")
	b.WriteString("#       deny = tolak semua tool berbahaya\n")
	b.WriteString("approval:\n")
	fmt.Fprintf(&b, "  mode: %s\n", yamlQuote(approvalMode))
	b.WriteString("  timeout: 120s\n")
	b.WriteString("  # always_allowed:            # bypass prompt untuk perintah spesifik\n")
	b.WriteString("  #   - \"terminal:git status\"\n")
	b.WriteString("  #   - \"write_file\"\n")
	b.WriteString("  # always_denied:             # blokir perintah spesifik\n")
	b.WriteString("  #   - \"terminal:rm -rf\"\n\n")

	b.WriteString("# --- Telegram ---\n")
	b.WriteString("telegram:\n")
	fmt.Fprintf(&b, "  token: %s\n", yamlQuote(token))
	b.WriteString("  allowed_users:\n")
	if len(allowedUsers) == 0 {
		b.WriteString("    []   # WARNING: kosong = tolak semua orang\n")
	} else {
		for _, id := range allowedUsers {
			fmt.Fprintf(&b, "    - %d\n", id)
		}
	}
	b.WriteString("  poll_timeout: 30s\n\n")

	b.WriteString("# --- Agent ---\n")
	b.WriteString("agent:\n")
	if promptPrefix != "" {
		fmt.Fprintf(&b, "  system_prompt_prefix: %s\n", yamlQuote(promptPrefix))
	} else {
		b.WriteString("  # system_prompt_prefix: \"kamu asisten pribadi yang ringkas dan langsung\"\n")
	}
	b.WriteString("  max_iterations: 40\n")
	b.WriteString("  max_turn_timeout: 15m\n")
	b.WriteString("  # aux_model: \"utama:gpt-4o-mini\"   # model untuk kompaksi (default = model utama)\n")
	b.WriteString("  auto_compact: true\n")
	b.WriteString("  compact_threshold: 0.7\n\n")

	b.WriteString("# --- Tools ---\n")
	b.WriteString("# Set false untuk mematikan tool tertentu.\n")
	b.WriteString("tools:\n")
	b.WriteString("  terminal: true\n")
	b.WriteString("  read_file: true\n")
	b.WriteString("  write_file: true\n")
	b.WriteString("  patch: true\n")
	b.WriteString("  web_fetch: true\n")
	b.WriteString("  memory: true\n")
	b.WriteString("  skill: true\n")
	b.WriteString("  cron: true\n")
	b.WriteString("  delegate: true\n\n")

	b.WriteString("# --- Limits (0 = nonaktif, hanya peringatan) ---\n")
	b.WriteString("limits:\n")
	b.WriteString("  warn_daily_usd: 1.0\n")
	b.WriteString("  max_daily_usd: 0\n")
	b.WriteString("  max_daily_tokens: 0\n")

	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		return err
	}

	fmt.Printf("\nConfig berhasil dibuat! (%s)\n", cfgPath)
	fmt.Printf("Jangan lupa set env: %s\n", apiKeyEnv)
	fmt.Println("Jalankan `renstra serve` untuk menyalakan bot Telegram.")
	return nil
}

// yamlQuote renders a string as a YAML double-quoted scalar so a token that
// contains ':' or '#' cannot break the file.
func yamlQuote(s string) string {
	raw, err := yaml.Marshal(s)
	if err != nil {
		return `""`
	}
	return strings.TrimSpace(string(raw))
}

func promptString(r *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, _ := r.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

// serveCmd runs the Telegram gateway + cron runner concurrently.
func serveCmd() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	home := config.Home()
	cfgPath := filepath.Join(home, "config.yaml")

	// Read Telegram token and allowed_users from config
	token := cfg.Telegram.Token
	allowedUsers := cfg.Telegram.AllowedUsers

	if token == "" {
		return fmt.Errorf("telegram.token belum diset di %s; jalankan `renstra setup`", cfgPath)
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

	// Seed bot with recently seen updates so a restart never answers twice.
	_ = st.PruneUpdatesSeen(ctx)
	recentIDs, _ := st.RecentUpdateIDs(ctx, 500)
	bot, err := telegram.New(telegram.Config{
		Token:        token,
		AllowedUsers: allowedUsers,
		PollTimeout:  pollTimeout,
		OnSeen: func(id int64) {
			sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer scancel()
			_ = st.MarkUpdateSeen(sctx, id)
		},
	})
	if err != nil {
		return err
	}
	for _, id := range recentIDs {
		bot.MarkSeen(id)
	}

	// Prepare provider, tools, memory, skills
	workspace := filepath.Join(home, "workspace")
	_ = os.MkdirAll(workspace, 0o755)

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
		skill.Layer{Dir: filepath.Join(home, "skills"), Writable: true},
		skill.Layer{Dir: filepath.Join(globalBaseDir(), "skills"), Writable: false},
	)
	if err != nil {
		return err
	}

	mc := pcfg.ModelConfig(model)
	auxModel := cfg.Agent.AuxModel
	if auxModel == "" {
		auxModel = model
	}

	runner := &agent.Runner{
		Provider:      prov,
		Tools:         tool.NewRegistry(),
		Store:         st,
		Model:         model,
		AuxModel:      auxModel,
		Cwd:           workspace,
		MaxIters:      cfg.Agent.MaxIterations,
		MaxTokens:     mc.MaxOutput,
		PriceIn:       mc.PriceIn,
		PriceOut:      mc.PriceOut,
		ContextWindow: mc.Context,
		AutoCompact:   cfg.Agent.AutoCompaction(),
		Threshold:     cfg.Agent.Threshold(),
		PromptPrefix:  cfg.Agent.SystemPromptPrefix,
		ApprovalCfg:   cfg.Approval,
		Memory:        memStore,
		Skills:        skillReg,
	}

	candidates := []tool.Tool{
		tool.NewTerminal(workspace, 0),
		tool.NewReadFile(workspace),
		tool.NewWriteFile(workspace),
		tool.NewPatch(workspace),
		tool.NewWebFetch(),
		tool.NewMemory(memStore),
		tool.NewSkill(skillReg),
		tool.NewCron(st, 0),
		tool.NewDelegate(runner, 0),
	}
	for _, t := range candidates {
		if cfg.Tools.IsEnabled(t.Name()) {
			runner.Tools.Add(t)
		}
	}

	// Connect MCP servers
	mcpServers, mcpErrors := mcp.ConnectAll(ctx, loadMCPConfigs())
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
		approvalTimeout: approvalWait,
	}

	// Cron runner in background with panic recovery
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
			ans, err := runner.Run(cctx, job.ChatID, job.Prompt)
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
	approvalTimeout time.Duration
}

func (h *telegramHandler) Handle(ctx context.Context, chatID int64, text string) (reply string, err error) {
	// A panic inside a tool must not take the gateway down with it.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "panic saat menangani chat %d: %v\n", chatID, r)
			reply, err = "", fmt.Errorf("panic internal: %v", r)
		}
	}()
	return h.runner.Run(ctx, chatID, text)
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
	default:
		return nil, fmt.Errorf("provider kind %q belum diaktifkan", pcfg.Kind)
	}
}
