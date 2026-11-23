package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"renstra/internal/cli"
	"renstra/internal/config"
	"renstra/internal/provider/google"
	"renstra/internal/provider/groq"
	"renstra/internal/provider/mistral"
	"renstra/internal/provider/ollama"
	"renstra/internal/provider/openrouter"
	"renstra/internal/provider/xai"
	"renstra/internal/store"
)

func modelCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("pemakaian: renstra model <list|set <ref>|add <nama> [provider] [context]>")
	}
	switch args[0] {
	case "list":
		modelListCmd()
		return nil
	case "set":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("pemakaian: renstra model set <model|provider:model|alias>")
		}
		return modelSetCmd(args[1])
	case "add":
		return modelAddCmd(args[1:])
	case "rm", "remove", "delete":
		if len(args) < 2 {
			return fmt.Errorf("pemakaian: renstra model rm <nama_model|alias|provider:model>")
		}
		return modelRmCmd(args[1])
	default:
		// Allow shorthand: `renstra model <ref>`
		return modelSetCmd(args[0])
	}
}

func modelAddCmd(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	spec, err := config.ParseAddModelArgs(args, cfg)
	if err != nil {
		return err
	}
	if _, err := config.AddModel(spec, cfg); err != nil {
		return err
	}
	ref := spec.Provider + ":" + spec.Model
	out := cli.Writer(os.Stdout)
	fmt.Fprintf(out, "%s✅ Model ditambahkan:%s %s%s%s (context=%s%d%s, max_output=%s%d%s, vision=%v)\n",
		cli.ColorGreen, cli.ColorReset,
		cli.ColorCyan, ref, cli.ColorReset,
		cli.ColorYellow, spec.Context, cli.ColorReset,
		cli.ColorYellow, spec.MaxOutput, cli.ColorReset,
		spec.Vision)
	return nil
}

func modelRmCmd(ref string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	_, removed, err := config.RemoveModel(ref, cfg)
	if err != nil {
		return err
	}
	out := cli.Writer(os.Stdout)
	fmt.Fprintf(out, "%s🗑️ Model/alias dihapus:%s %s%s%s\n",
		cli.ColorGreen, cli.ColorReset, cli.ColorYellow, removed, cli.ColorReset)
	return nil
}

func modelSetCmd(ref string) error {
	model, storedRef, err := config.SetDefaultModel(ref)
	if err != nil {
		return err
	}
	// Sync chat 1 CLI session override if state.db already exists.
	dbPath := filepath.Join(config.Home(), "state.db")
	if _, statErr := os.Stat(dbPath); statErr == nil {
		if st, err := store.Open(dbPath); err == nil {
			_ = st.SetChatModel(context.Background(), 1, storedRef)
			_ = st.Close()
		}
	}
	out := cli.Writer(os.Stdout)
	if storedRef != model {
		fmt.Fprintf(out, "%s✅ Model default diubah ke:%s %s%s%s (%s%s%s)\n",
			cli.ColorGreen, cli.ColorReset, cli.ColorCyan, model, cli.ColorReset, cli.ColorDim, storedRef, cli.ColorReset)
	} else {
		fmt.Fprintf(out, "%s✅ Model default diubah ke:%s %s%s%s\n",
			cli.ColorGreen, cli.ColorReset, cli.ColorCyan, model, cli.ColorReset)
	}
	return nil
}

func modelListCmd() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gagal memuat config: %v\n", err)
	}

	out := cli.Writer(os.Stdout)
	fmt.Fprintf(out, "%s╭─ Provider aktif%s\n", cli.ColorCyan, cli.ColorReset)
	fmt.Fprintf(out, "  %s%-11s%s %s(aktif)%s - endpoint OpenAI-compatible / custom\n", cli.ColorYellow, "openai", cli.ColorReset, cli.ColorGreen, cli.ColorReset)
	fmt.Fprintf(out, "  %s%-11s%s %s(aktif)%s - Claude Messages API\n", cli.ColorYellow, "anthropic", cli.ColorReset, cli.ColorGreen, cli.ColorReset)
	for _, name := range []string{
		openrouter.Name(), groq.Name(), google.Name(),
		ollama.Name(), mistral.Name(), xai.Name(),
	} {
		fmt.Fprintf(out, "  %s%-11s%s %s(aktif)%s\n", cli.ColorYellow, name, cli.ColorReset, cli.ColorGreen, cli.ColorReset)
	}

	if cfg != nil && cfg.DefaultModel != "" {
		fmt.Fprintf(out, "\n%sModel default:%s %s%s%s\n", cli.ColorDim, cli.ColorReset, cli.ColorGreen, cfg.DefaultModel, cli.ColorReset)
	}
	if txt := modelListText(cfg); txt != "" {
		fmt.Fprintln(out, txt)
	}
	fmt.Fprintf(out, "%sGanti model default: renstra model set <provider:model|alias|model>%s\n", cli.ColorDim, cli.ColorReset)
}

// modelListText renders configured providers, their models, and aliases for
// both `renstra model list` and the CLI `/model` listing.
func modelListText(cfg *config.Config) string {
	if cfg == nil || (len(cfg.Providers) == 0 && len(cfg.Aliases) == 0) {
		return ""
	}
	var b strings.Builder
	if len(cfg.Providers) > 0 {
		fmt.Fprintf(&b, "\n%sProvider & model terkonfigurasi:%s\n", cli.ColorCyan, cli.ColorReset)
		pnames := make([]string, 0, len(cfg.Providers))
		for n := range cfg.Providers {
			pnames = append(pnames, n)
		}
		sort.Strings(pnames)
		for _, pn := range pnames {
			pc := cfg.Providers[pn]
			kind := pc.Kind
			if kind == "" {
				kind = "openai"
			}
			fmt.Fprintf(&b, "  %s%-12s%s %s(%s)%s default=%s%s%s",
				cli.ColorYellow, pn, cli.ColorReset,
				cli.ColorDim, kind, cli.ColorReset,
				cli.ColorGreen, pc.DefaultModel, cli.ColorReset)
			if len(pc.Models) > 0 {
				mnames := make([]string, 0, len(pc.Models))
				for mn := range pc.Models {
					mnames = append(mnames, mn)
				}
				sort.Strings(mnames)
				fmt.Fprintf(&b, " %s[%s]%s", cli.ColorDim, strings.Join(mnames, ", "), cli.ColorReset)
			}
			b.WriteString("\n")
		}
	}
	if len(cfg.Aliases) > 0 {
		fmt.Fprintf(&b, "\n%sAlias:%s\n", cli.ColorCyan, cli.ColorReset)
		anames := make([]string, 0, len(cfg.Aliases))
		for a := range cfg.Aliases {
			anames = append(anames, a)
		}
		sort.Strings(anames)
		for _, a := range anames {
			fmt.Fprintf(&b, "  %s%-12s%s -> %s%s%s\n", cli.ColorYellow, a, cli.ColorReset, cli.ColorGreen, cfg.Aliases[a], cli.ColorReset)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
