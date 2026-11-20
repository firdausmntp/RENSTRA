package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type hermesConfig struct {
	Model struct {
		Provider string `yaml:"provider"`
		Default  string `yaml:"default"`
		BaseURL  string `yaml:"base_url"`
	} `yaml:"model"`
	Agent struct {
		MaxTurns         int     `yaml:"max_turns"`
		SystemPrompt     string  `yaml:"system_prompt"`
		CompactThreshold float64 `yaml:"compact_threshold"`
	} `yaml:"agent"`
	MCPServers map[string]struct {
		Command string            `yaml:"command"`
		Args    []string          `yaml:"args"`
		Env     map[string]string `yaml:"env"`
		URL     string            `yaml:"url"`
		Headers map[string]string `yaml:"headers"`
	} `yaml:"mcp_servers"`
	Platforms struct {
		Telegram struct {
			Enabled      bool    `yaml:"enabled"`
			Token        string  `yaml:"token"`
			AllowedUsers []int64 `yaml:"allowed_users"`
			Extra        struct {
				DMTopics []struct {
					ChatID int64 `yaml:"chat_id"`
				} `yaml:"dm_topics"`
			} `yaml:"extra"`
		} `yaml:"telegram"`
	} `yaml:"platforms"`
}

func migrateConfigAndMCP(src string, dotEnv map[string]string, opts Options, rep *Report) error {
	cfgSrc := filepath.Join(src, "config.yaml")
	raw, err := os.ReadFile(cfgSrc)
	hasConfig := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("membaca %s: %w", cfgSrc, err)
	}

	var hc hermesConfig
	if hasConfig {
		if err := yaml.Unmarshal(raw, &hc); err != nil {
			return fmt.Errorf("parse %s: %w", cfgSrc, err)
		}
	}

	soulText := ""
	if soulRaw, err := os.ReadFile(filepath.Join(src, "SOUL.md")); err == nil {
		soulText = strings.TrimSpace(string(soulRaw))
	}

	tgToken := strings.TrimSpace(hc.Platforms.Telegram.Token)
	if tgToken == "" {
		tgToken = strings.TrimSpace(dotEnv["TELEGRAM_BOT_TOKEN"])
	}
	if hasConfig || soulText != "" || tgToken != "" {
		cfgDst := filepath.Join(opts.ToDir, "config.yaml")
		if _, err := os.Stat(cfgDst); err == nil && !opts.Force {
			rep.SkippedItems = append(rep.SkippedItems, "config.yaml sudah ada (pakai --force untuk menimpa)")
		} else {
			kind, baseURL, apiKeyEnv := mapHermesProvider(hc.Model.Provider, hc.Model.BaseURL)
			modelName := strings.TrimSpace(hc.Model.Default)
			if modelName == "" {
				modelName = defaultModelForKind(kind)
			}
			provName := kind
			if provName == "" {
				provName = "utama"
			}

			maxIters := hc.Agent.MaxTurns
			if maxIters <= 0 {
				maxIters = 40
			}
			threshold := hc.Agent.CompactThreshold
			if threshold <= 0 {
				threshold = 0.7
			}
			prefix := soulText
			if prefix == "" {
				prefix = strings.TrimSpace(hc.Agent.SystemPrompt)
			}

			allowed := collectTelegramUsers(hc, dotEnv)

			doc := map[string]any{
				"default_model": provName + ":" + modelName,
				"providers": map[string]any{
					provName: map[string]any{
						"kind":          kind,
						"base_url":      baseURL,
						"api_key_env":   apiKeyEnv,
						"default_model": modelName,
					},
				},
				"aliases": map[string]any{},
				"approval": map[string]any{
					"mode":    "prompt",
					"timeout": "120s",
				},
				"telegram": map[string]any{
					"token":         tgToken,
					"allowed_users": allowed,
					"poll_timeout":  "30s",
				},
				"agent": map[string]any{
					"system_prompt_prefix": prefix,
					"max_iterations":       maxIters,
					"max_turn_timeout":     "15m",
					"auto_compact":         true,
					"compact_threshold":    threshold,
				},
				"limits": map[string]any{
					"warn_daily_usd":   1.0,
					"max_daily_usd":    0,
					"max_daily_tokens": 0,
				},
			}

			if !opts.DryRun {
				outYAML, err := yaml.Marshal(doc)
				if err != nil {
					return err
				}
				if err := os.WriteFile(cfgDst, outYAML, 0o600); err != nil {
					return fmt.Errorf("menulis %s: %w", cfgDst, err)
				}
			}
			rep.ConfigWritten = true
		}
	}

	if len(hc.MCPServers) > 0 {
		mcpDst := filepath.Join(opts.ToDir, "mcp.yaml")
		if _, err := os.Stat(mcpDst); err == nil && !opts.Force {
			rep.SkippedItems = append(rep.SkippedItems, "mcp.yaml sudah ada (pakai --force untuk menimpa)")
		} else {
			type outMCPServer struct {
				Command string            `yaml:"command,omitempty"`
				Args    []string          `yaml:"args,omitempty"`
				Env     []string          `yaml:"env,omitempty"`
				URL     string            `yaml:"url,omitempty"`
				Headers map[string]string `yaml:"headers,omitempty"`
			}
			outDoc := struct {
				Servers map[string]outMCPServer `yaml:"servers"`
			}{
				Servers: map[string]outMCPServer{},
			}
			for name, s := range hc.MCPServers {
				var envSlice []string
				keys := make([]string, 0, len(s.Env))
				for k := range s.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					val := expandEnvPlaceholders(s.Env[k], dotEnv)
					envSlice = append(envSlice, k+"="+val)
				}
				headers := map[string]string{}
				for hk, hv := range s.Headers {
					headers[hk] = expandEnvPlaceholders(hv, dotEnv)
				}
				outDoc.Servers[name] = outMCPServer{
					Command: s.Command,
					Args:    s.Args,
					Env:     envSlice,
					URL:     s.URL,
					Headers: headers,
				}
			}
			if !opts.DryRun {
				outYAML, err := yaml.Marshal(outDoc)
				if err != nil {
					return err
				}
				if err := os.WriteFile(mcpDst, outYAML, 0o600); err != nil {
					return fmt.Errorf("menulis %s: %w", mcpDst, err)
				}
			}
			rep.MCPWritten = true
			rep.MCPServers = len(outDoc.Servers)
		}
	}

	return nil
}

func mapHermesProvider(rawKind, baseURL string) (kind, base, envKey string) {
	k := strings.ToLower(strings.TrimSpace(rawKind))
	switch k {
	case "", "openai":
		kind, base, envKey = "openai", "https://api.openai.com/v1", "OPENAI_API_KEY"
	case "anthropic":
		kind, base, envKey = "anthropic", "https://api.anthropic.com", "ANTHROPIC_API_KEY"
	case "openrouter":
		kind, base, envKey = "openrouter", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"
	case "groq":
		kind, base, envKey = "groq", "https://api.groq.com/openai/v1", "GROQ_API_KEY"
	case "ollama":
		kind, base, envKey = "ollama", "http://localhost:11434/v1", ""
	case "mistral":
		kind, base, envKey = "mistral", "https://api.mistral.ai/v1", "MISTRAL_API_KEY"
	case "xai":
		kind, base, envKey = "xai", "https://api.x.ai/v1", "XAI_API_KEY"
	case "google", "gemini":
		kind, base, envKey = "google", "https://generativelanguage.googleapis.com", "GEMINI_API_KEY"
	default:
		kind, base, envKey = "custom", "https://api.openai.com/v1", "OPENAI_API_KEY"
	}
	if strings.TrimSpace(baseURL) != "" {
		base = strings.TrimSpace(baseURL)
	}
	return kind, base, envKey
}

func defaultModelForKind(kind string) string {
	switch kind {
	case "anthropic":
		return "claude-sonnet-4-5"
	case "google":
		return "gemini-2.5-flash"
	case "openrouter":
		return "anthropic/claude-sonnet-4-5"
	case "groq":
		return "llama-3.3-70b-versatile"
	case "ollama":
		return "qwen2.5:14b"
	default:
		return "gpt-4o-mini"
	}
}

func collectTelegramUsers(hc hermesConfig, dotEnv map[string]string) []int64 {
	seen := map[int64]bool{}
	var out []int64
	add := func(id int64) {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range hc.Platforms.Telegram.AllowedUsers {
		add(id)
	}
	for _, t := range hc.Platforms.Telegram.Extra.DMTopics {
		add(t.ChatID)
	}
	for _, envKey := range []string{"TELEGRAM_ALLOWED_USERS", "TELEGRAM_GROUP_ALLOWED_USERS"} {
		for _, part := range strings.Split(dotEnv[envKey], ",") {
			part = strings.TrimSpace(part)
			if id, err := strconv.ParseInt(part, 10, 64); err == nil {
				add(id)
			}
		}
	}
	return out
}
