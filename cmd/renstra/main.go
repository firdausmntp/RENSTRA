package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/provider/openai"
	"renstra/internal/store"
	"renstra/internal/tool"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
	}
	switch args[0] {
	case "config":
		configCmd(args[1:])
	case "chat":
		if err := chatCmd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "perintah tidak dikenal: %s\n", args[0])
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `pemakaian:
  renstra chat [model]     mulai sesi CLI
  renstra config path      cetak RENSTRA_HOME`)
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
		prov = openai.New(pcfg, apiKey)
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

	r := &agent.Runner{
		Provider:  prov,
		Tools:     tool.NewRegistry(tool.NewTerminal(workspace, 0), tool.NewReadFile(), tool.NewWriteFile(), tool.NewPatch()),
		Store:     st,
		Model:     model,
		Cwd:       workspace,
		MaxIters:  cfg.Agent.MaxIterations,
		MaxTokens: pcfg.ModelConfig(model).MaxOutput,
		Approve:   cli.Approver(os.Stdin, os.Stdout),
		Out:       os.Stdout,
	}
	fmt.Printf("renstra — %s (%s). /help untuk bantuan.\n\n", model, filepath.Base(pcfg.BaseURL))
	return cli.Run(context.Background(), r, 1, os.Stdin, os.Stdout)
}
