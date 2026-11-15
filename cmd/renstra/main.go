package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/provider/openai"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/tool"
)

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
	case "usage":
		if err := usageCmd(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "config":
		configCmd(args[1:])
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
  renstra model list                 daftar provider aktif & stub
  renstra skill list [--global]      lihat skill terdaftar
  renstra skill add <n> <d> <b>      tambah skill baru
  renstra skill rm <n> [--global]    hapus skill
  renstra usage                      laporan pemakaian token & biaya
  renstra config path                lokasi direktori data (~/.renstra)`)
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

	apiKey := ""
	if pcfg.APIKeyEnv != "" {
		apiKey = os.Getenv(pcfg.APIKeyEnv)
	}

	var prov provider.Provider
	switch pcfg.Kind {
	case "", "openai", "custom":
		c, err := openai.New(pcfg, apiKey)
		if err != nil {
			return err
		}
		prov = c
	case "anthropic":
		return fmt.Errorf("provider anthropic belum diimplementasikan (Plan 4)")
	default:
		return fmt.Errorf("provider kind %q belum didukung", pcfg.Kind)
	}

	home := config.Home()
	workspace := filepath.Join(home, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		return err
	}

	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	memStore, _ := memory.Open(filepath.Join(home, "memory"))
	skillReg, _ := skill.Load(
		skill.Layer{Dir: filepath.Join(home, "skills"), Writable: true},
		skill.Layer{Dir: filepath.Join(globalBaseDir(), "skills"), Writable: false},
	)

	tools := []tool.Tool{
		tool.NewTerminal(workspace, 0),
		tool.NewReadFile(workspace),
		tool.NewWriteFile(workspace),
		tool.NewPatch(workspace),
		tool.NewWebFetch(),
	}
	if memStore != nil {
		tools = append(tools, tool.NewMemory(memStore))
	}
	if skillReg != nil {
		tools = append(tools, tool.NewSkill(skillReg))
	}

	mc := pcfg.ModelConfig(model)
	r := &agent.Runner{
		Provider:  prov,
		Tools:     tool.NewRegistry(),
		Store:     st,
		Model:     model,
		Cwd:       workspace,
		MaxIters:  cfg.Agent.MaxIterations,
		MaxTokens: mc.MaxOutput,
		PriceIn:   mc.PriceIn,
		PriceOut:  mc.PriceOut,
		Memory:    memStore,
		Skills:    skillReg,
		Approve:   cli.Approver(os.Stdin, os.Stdout),
		Out:       os.Stdout,
	}
	for _, t := range tools {
		r.Tools.Add(t)
	}
	r.Tools.Add(tool.NewDelegate(r, 0))
	fmt.Printf("renstra — %s (%s). /help untuk bantuan.\n\n", model, filepath.Base(pcfg.BaseURL))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return cli.Run(ctx, r, 1, os.Stdin, os.Stdout)
}
