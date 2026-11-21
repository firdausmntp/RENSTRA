package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/cron"
	"renstra/internal/mcp"
	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/telegram"
	"renstra/internal/tool"
	"renstra/internal/update"
)

// serveCmd runs the Telegram gateway + cron runner concurrently.
func serveCmd() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	home := config.Home()
	cfgPath := filepath.Join(home, "config.yaml")

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

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
	_ = os.MkdirAll(filepath.Join(workspace, "tmp"), 0o755)
	pruneRetention(cfg, home, workspace)

	_ = st.PruneUpdatesSeen(ctx)
	recentIDs, _ := st.RecentUpdateIDs(ctx, 500)
	var handler *telegramHandler
	errLog := newErrorLogRecorder(home, version)
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
		OnAutoApprove: func(cid int64) {
			if handler != nil {
				sctx, scancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer scancel()
				_ = handler.setApprovalMode(sctx, cid, "auto")
			}
		},
		OnCallback: func(cctx context.Context, cid, mid int64, data string) {
			if handler != nil {
				handler.HandleCallback(cctx, cid, mid, data)
			}
		},
		OnErrorLog: func(ectx context.Context, cid int64, e error) string {
			modelName := ""
			if handler != nil {
				modelName = handler.runnerForChat(ectx, cid).GetModel()
			}
			return errLog.recordAndWriteSnapshot(cid, modelName, e)
		},
		Transcribe: buildTranscriber(cfg),
	})
	if err != nil {
		return err
	}
	for _, id := range recentIDs {
		bot.MarkSeen(id)
	}

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

	handler = &telegramHandler{
		bot:             bot,
		runner:          runner,
		store:           st,
		cfg:             cfg,
		errLog:          errLog,
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

	go func() {
		if rel, isNewer := update.CheckCachedUpdate(home, version, 12*time.Hour); isNewer && rel != nil {
			logger.Info("renstra_update_available", "current", version, "latest", rel.TagName, "url", rel.HTMLURL)
		}
	}()

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

	if cleanupPID, err := writePIDFile(home); err == nil {
		defer cleanupPID()
	}

	fmt.Printf("renstra gateway berjalan (model: %s). Tekan Ctrl+C untuk berhenti.\n", model)
	return bot.Run(ctx, handler)
}
