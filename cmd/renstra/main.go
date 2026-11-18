package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/tool"
)

var version = "v1.1.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}
	switch args[0] {
	case "setup":
		if err := setupCmd(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "serve":
		if err := serveCmd(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "chat":
		if err := chatCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "model":
		if len(args) > 1 && args[1] == "list" {
			modelListCmd()
			return
		}
		fmt.Fprintln(os.Stderr, "pemakaian: renstra model list")
		os.Exit(2)
	case "skill":
		if err := skillCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "mcp":
		if err := mcpCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "usage":
		if err := usageCmd(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "config":
		configCmd(args[1:])
	case "migrate":
		if err := migrateCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "version", "-v", "--version":
		fmt.Println("renstra", version)
	default:
		fmt.Fprintf(os.Stderr, "perintah tidak dikenal: %s\n", args[0])
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `pemakaian renstra:
  renstra setup                      wizard konfigurasi awal (token bot, key, dll)
  renstra serve                      jalankan gateway Telegram + cron
  renstra chat [model]               sesi chat interaktif di CLI
  renstra migrate hermes [flags]     impor memori, skill, config & riwayat dari ~/.hermes
  renstra model list                 daftar provider aktif
  renstra skill list [--global]      lihat skill terdaftar
  renstra skill add <dir|url>        impor skill dari direktori atau git URL
  renstra skill add <n> <d> <b>      tambah skill baru secara langsung
  renstra skill rm <n> [--global]    hapus skill
  renstra mcp list                   daftar server MCP terdaftar
  renstra usage                      laporan pemakaian token & biaya
  renstra config path                lokasi direktori data (~/.renstra)
  renstra version                    tampilkan versi binary`)
	os.Exit(2)
}

func configCmd(args []string) {
	if len(args) > 0 && args[0] == "path" {
		fmt.Println(config.Home())
		return
	}
	fmt.Fprintln(os.Stderr, "pemakaian: renstra config path")
	os.Exit(2)
}

func chatCmd(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ref := cfg.DefaultModel
	if len(args) > 0 {
		ref = args[0]
	}
	pcfg, model, err := config.Resolve(ref, cfg)
	if err != nil {
		return err
	}

	prov, err := buildProvider(pcfg)
	if err != nil {
		return err
	}

	home := config.Home()
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return err
	}
	pruneRetention(cfg, home, workspace)

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

	checkpointsDir := filepath.Join(home, "checkpoints")
	wfTool := tool.NewWriteFile(workspace)
	wfTool.BackupDir = checkpointsDir
	patchTool := tool.NewPatch(workspace)
	patchTool.BackupDir = checkpointsDir
	webFetchTool := tool.NewWebFetch()
	webFetchTool.AllowPrivate = cfg.Tools.AllowPrivateFetch

	tools := []tool.Tool{
		tool.NewTerminal(workspace, 0),
		tool.NewReadFile(workspace),
		wfTool,
		patchTool,
		webFetchTool,
		tool.NewWebSearch(),
		tool.NewSessionSearch(st),
	}
	if memStore != nil {
		tools = append(tools, tool.NewMemory(memStore))
	}
	if skillReg != nil {
		tools = append(tools, tool.NewSkill(skillReg))
	}
	tools = append(tools, tool.NewCron(st, 1))

	mc := pcfg.ModelConfig(model)
	auxProv, auxModel, auxIn, auxOut, auxErr := resolveAuxTarget(cfg, prov, model, mc)
	if auxErr != nil {
		fmt.Fprintf(os.Stderr, "peringatan aux_model tidak valid: %v\n", auxErr)
	}

	r := &agent.Runner{
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
		Approve:            cli.Approver(os.Stdin, os.Stdout),
		Out:                os.Stdout,
	}
	for _, t := range tools {
		if cfg.Tools.IsEnabled(t.Name()) {
			r.Tools.Add(t)
		}
	}
	if cfg.Tools.IsEnabled("delegate") {
		r.Tools.Add(tool.NewDelegate(r, 0))
	}
	fmt.Printf("renstra — %s (%s). /help untuk bantuan.\n\n", model, filepath.Base(pcfg.BaseURL))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return cli.RunWithOptions(ctx, r, 1, os.Stdin, os.Stdout, cli.RunOptions{
		SwitchModel: func(ref string) (string, error) {
			nextPCfg, nextModel, err := config.Resolve(ref, cfg)
			if err != nil {
				return "", err
			}
			nextProv, err := buildProvider(nextPCfg)
			if err != nil {
				return "", err
			}
			nextMC := nextPCfg.ModelConfig(nextModel)
			r.SetModel(nextProv, nextModel, nextMC.MaxOutput, nextMC.Context, nextMC.PriceIn, nextMC.PriceOut)
			r.SetCapabilities(nextMC.Vision, nextMC.Tools != nil && !*nextMC.Tools)
			return fmt.Sprintf("Model aktif diganti ke: %s", nextModel), nil
		},
	})
}
