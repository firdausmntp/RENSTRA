package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"renstra/internal/config"
	"renstra/internal/mcp"
	"renstra/internal/provider/google"
	"renstra/internal/provider/groq"
	"renstra/internal/provider/mistral"
	"renstra/internal/provider/ollama"
	"renstra/internal/provider/openrouter"
	"renstra/internal/provider/xai"
	"renstra/internal/skill"
	"renstra/internal/store"
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
				continue
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
