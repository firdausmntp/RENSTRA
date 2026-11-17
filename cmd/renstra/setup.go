package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"renstra/internal/config"
)

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

	b.WriteString("default_model: " + yamlQuote("utama:"+defaultModel) + "\n")
	b.WriteString("# fallbacks:                 # provider cadangan bila provider utama gagal\n")
	b.WriteString("#   - \"cadangan:gpt-4o-mini\"\n\n")

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
	b.WriteString("  web_search: true\n")
	b.WriteString("  memory: true\n")
	b.WriteString("  skill: true\n")
	b.WriteString("  cron: true\n")
	b.WriteString("  delegate: true\n")
	b.WriteString("  session_search: true\n\n")

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
