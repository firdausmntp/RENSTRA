package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/tool"
	"renstra/internal/update"
)

var version = "v1.4.5"

func main() {
	if rel, isNewer := update.PeekCachedUpdate(config.Home(), version); isNewer && rel != nil {
		cli.SetUpdateNotice(fmt.Sprintf("Versi baru tersedia: %s (kamu: %s). Jalankan `renstra update` untuk memperbarui.", rel.TagName, version))
	}
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
		if err := serveCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "dashboard":
		if err := dashboardCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "restart":
		if err := restartCmd(args[1:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "service":
		if err := serviceCmd(args[1:], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "doctor":
		if err := doctorCmd(os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "chat":
		if err := chatCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "update":
		if err := updateCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "model":
		if err := modelCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		return
	case "account", "accounts":
		if err := accountCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(2)
		}
		return
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
	case "backup":
		if err := backupCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "restore":
		if err := restoreCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "help", "-h", "--help":
		cli.PrintCLIUsage(os.Stdout, detectLang(args[1:]), version)
	case "version", "-v", "--version":
		out := cli.Writer(os.Stdout)
		fmt.Fprintf(out, "%s⚡ renstra%s %s%s%s\n", cli.ColorCyan, cli.ColorReset, cli.ColorGreen, version, cli.ColorReset)
		if rel, isNewer := update.PeekCachedUpdate(config.Home(), version); isNewer && rel != nil {
			fmt.Fprintf(out, "%s💡 Versi baru tersedia: %s (kamu: %s). Jalankan `renstra update` untuk memperbarui.%s\n", cli.ColorYellow, rel.TagName, version, cli.ColorReset)
		}
	default:
		fmt.Fprintf(os.Stderr, "perintah tidak dikenal: %s\n\n", args[0])
		cli.PrintCLIUsage(os.Stderr, detectLang(args[1:]), version)
		os.Exit(2)
	}
}

func detectLang(args []string) string {
	for i := 0; i < len(args); i++ {
		if (args[i] == "--lang" || args[i] == "-l") && i+1 < len(args) {
			if strings.EqualFold(args[i+1], "en") {
				return "en"
			}
			return "id"
		}
		if strings.HasPrefix(args[i], "--lang=") {
			if strings.EqualFold(strings.TrimPrefix(args[i], "--lang="), "en") {
				return "en"
			}
			return "id"
		}
	}
	if strings.EqualFold(os.Getenv("RENSTRA_LANG"), "en") {
		return "en"
	}
	return "id"
}

func usage() {
	cli.PrintCLIUsage(os.Stderr, detectLang(nil), version)
	os.Exit(2)
}

func configCmd(args []string) {
	if len(args) > 0 && args[0] == "path" {
		out := cli.Writer(os.Stdout)
		fmt.Fprintf(out, "%s%s%s\n", cli.ColorCyan, config.Home(), cli.ColorReset)
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

	ref := cfg.DefaultModel
	if len(args) > 0 {
		ref = args[0]
	} else if saved, err := st.GetChatModel(context.Background(), 1); err == nil && saved != "" {
		ref = saved
	}
	pcfg, model, err := config.Resolve(ref, cfg)
	if err != nil {
		return err
	}

	prov, err := buildProvider(pcfg)
	if err != nil {
		return err
	}

	memStore, _ := memory.Open(filepath.Join(home, "memory"))
	skillReg, _ := skill.Load(
		skill.Layer{Dir: filepath.Join(workspace, ".skills"), Writable: false},
		skill.Layer{Dir: filepath.Join(workspace, "skills"), Writable: false},
		skill.Layer{Dir: filepath.Join(home, "skills"), Writable: true},
		skill.Layer{Dir: filepath.Join(globalBaseDir(), "skills"), Writable: false},
	)

	checkpointsDir := filepath.Join(home, "checkpoints")
	_ = os.MkdirAll(filepath.Join(workspace, "tmp"), 0o755)
	pruneRetention(cfg, home, workspace)
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
		mt := tool.NewMemory(memStore)
		mt.SetFactStore(st)
		tools = append(tools, mt)
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

	var updateMsg string
	if rel, isNewer := update.CheckCachedUpdate(home, version, 12*time.Hour); isNewer && rel != nil {
		updateMsg = fmt.Sprintf("Versi baru tersedia: %s (kamu: %s). Jalankan `renstra update` untuk memperbarui.", rel.TagName, version)
		cli.SetUpdateNotice(updateMsg)
	}

	provKind := pcfg.Kind
	if provKind == "" {
		provKind = "openai"
	}

	cli.PrintBanner(os.Stdout, r, cli.RunOptions{
		Version:    version,
		Provider:   provKind,
		UpdateInfo: updateMsg,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return cli.RunWithOptions(ctx, r, 1, os.Stdin, os.Stdout, cli.RunOptions{
		Version:    version,
		Provider:   provKind,
		UpdateInfo: updateMsg,
		ModelList:  func() string { return modelListText(cfg) },
		BindApprover: func(rd *bufio.Reader) {
			r.Approve = cli.ApproverFrom(rd, os.Stdout)
		},
		SwitchModel: func(ref string) (string, error) {
			cleaned, isGlobal := parseGlobalModelFlag(strings.TrimSpace(ref))
			if isGlobal {
				ref = cleaned
			}
			trimmed := strings.TrimSpace(ref)
			if strings.EqualFold(trimmed, "add") || strings.HasPrefix(strings.ToLower(trimmed), "add ") {
				fields := strings.Fields(strings.TrimSpace(trimmed[len("add"):]))
				spec, err := config.ParseAddModelArgs(fields, cfg)
				if err != nil {
					return "", err
				}
				updatedCfg, err := config.AddModel(spec, cfg)
				if err != nil {
					return "", err
				}
				cfg = updatedCfg
				ref = spec.Provider + ":" + spec.Model
			}
			for _, prefix := range []string{"rm ", "remove ", "delete ", "del "} {
				if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
					target := strings.TrimSpace(trimmed[len(prefix):])
					updatedCfg, removed, err := config.RemoveModel(target, cfg)
					if err != nil {
						return "", err
					}
					cfg = updatedCfg
					return fmt.Sprintf("🗑️ Model/alias dihapus: %s", removed), nil
				}
			}
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
			if isGlobal {
				_, storedRef, _ := config.SetDefaultModel(ref)
				if storedRef != "" {
					cfg.DefaultModel = storedRef
					_ = st.SetGlobalModel(context.Background(), storedRef)
				}
				return fmt.Sprintf("🌐 Model global diubah ke: %s (semua sesi & default)", nextModel), nil
			}
			_ = st.SetChatModel(context.Background(), 1, ref)
			return fmt.Sprintf("Model aktif diganti ke: %s", nextModel), nil
		},
	})
}
