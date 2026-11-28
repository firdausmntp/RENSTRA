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
	providerKind := strings.ToLower(strings.TrimSpace(promptString(reader,
		"Provider kind (openai / anthropic / google / openrouter / groq / ollama / mistral / xai)", "openai")))
	defBase, defKeyEnv, defModel := providerDefaults(providerKind)
	baseURL := promptString(reader, "Provider Base URL (kosong = bawaan provider)", defBase)
	apiKeyEnv := promptString(reader, "Nama env variable untuk API key", defKeyEnv)
	apiKeyVal := promptString(reader, fmt.Sprintf("Nilai API key untuk %s (bisa diisi sekarang atau nanti via .env)", apiKeyEnv), "")
	defaultModel := promptString(reader, "Model default", defModel)
	allowedUserStr := promptString(reader, "Telegram User ID kamu (angka, pisahkan koma)", "")
	approvalMode := promptString(reader, "Mode approval (prompt / auto / deny)", "prompt")
	promptPrefix := promptString(reader, "System prompt prefix (kosong = default)", "")

	if baseURL == "" {
		baseURL = defBase
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
	mc := config.DefaultModelConfig(defaultModel)
	b.WriteString("    models:\n")
	fmt.Fprintf(&b, "      %s:\n", yamlQuote(defaultModel))
	fmt.Fprintf(&b, "        context: %d\n", mc.Context)
	if mc.MaxOutput > 0 {
		fmt.Fprintf(&b, "        max_output: %d\n", mc.MaxOutput)
	}
	if mc.Vision {
		b.WriteString("        vision: true\n")
	}
	b.WriteString("        # price_in: 0.15\n")
	b.WriteString("        # price_out: 0.60\n\n")

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
	b.WriteString("  max_iterations: 80\n")
	b.WriteString("  max_turn_timeout: 30m\n")
	b.WriteString("  file_retention_days: 14\n")
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
	b.WriteString("  session_search: true\n")
	b.WriteString("  send_file: true\n\n")

	b.WriteString("# --- Limits (0 = nonaktif, hanya peringatan) ---\n")
	b.WriteString("limits:\n")
	b.WriteString("  warn_daily_usd: 1.0\n")
	b.WriteString("  max_daily_usd: 0\n")
	b.WriteString("  max_daily_tokens: 0\n")

	if err := os.WriteFile(cfgPath, []byte(b.String()), 0o600); err != nil {
		return err
	}

	envPath := filepath.Join(home, ".env")
	if apiKeyVal != "" {
		if err := saveEnvVar(envPath, apiKeyEnv, apiKeyVal); err != nil {
			fmt.Printf("Peringatan: gagal menyimpan API key ke .env: %v\n", err)
		} else {
			_ = os.Setenv(apiKeyEnv, apiKeyVal)
		}
	}

	fmt.Printf("\nConfig berhasil dibuat! (%s)\n", cfgPath)
	if apiKeyVal != "" {
		fmt.Printf("API Key berhasil disimpan dengan aman ke %s\n", envPath)
	} else {
		fmt.Printf("Catatan: Jangan lupa set environment variable %s atau isi ke %s\n", apiKeyEnv, envPath)
	}
	fmt.Println("Jalankan `renstra serve` untuk menyalakan bot Telegram, atau `renstra chat` untuk CLI.")
	return nil
}

// saveEnvVar updates or appends a key=value pair to the specified .env file.
func saveEnvVar(path, key, val string) error {
	var lines []string
	found := false
	if raw, err := os.ReadFile(path); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(raw)))
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "export ") {
				trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "export "))
			}
			if strings.HasPrefix(trimmed, key+"=") {
				lines = append(lines, fmt.Sprintf("%s=%q", key, val))
				found = true
			} else {
				lines = append(lines, line)
			}
		}
	}
	if !found {
		lines = append(lines, fmt.Sprintf("%s=%q", key, val))
	}
	content := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(content), 0o600)
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

func providerDefaults(kind string) (baseURL, apiKeyEnv, model string) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "anthropic":
		return "https://api.anthropic.com", "ANTHROPIC_API_KEY", "claude-3-5-sonnet-latest"
	case "google", "gemini":
		return "https://generativelanguage.googleapis.com", "GEMINI_API_KEY", "gemini-2.5-flash"
	case "openrouter":
		return "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY", "openai/gpt-4o-mini"
	case "groq":
		return "https://api.groq.com/openai/v1", "GROQ_API_KEY", "llama-3.3-70b-versatile"
	case "ollama":
		return "http://localhost:11434/v1", "OLLAMA_API_KEY", "qwen2.5-coder:7b"
	case "mistral":
		return "https://api.mistral.ai/v1", "MISTRAL_API_KEY", "mistral-small-latest"
	case "xai":
		return "https://api.x.ai/v1", "XAI_API_KEY", "grok-3-mini"
	default:
		return "https://api.openai.com/v1", "OPENAI_API_KEY", "gpt-4o-mini"
	}
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
