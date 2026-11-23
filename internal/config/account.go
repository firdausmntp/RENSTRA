package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// AccountInfo summarizes one configured provider account and its API key state.
type AccountInfo struct {
	Name         string
	Kind         string
	BaseURL      string
	DefaultModel string
	APIKeyEnv    string
	HasKey       bool
	MaskedKey    string
	IsDefault    bool
}

// MaskSecret masks an API key so only a short prefix remains visible.
func MaskSecret(val string) string {
	v := strings.TrimSpace(val)
	if v == "" {
		return "(belum diset)"
	}
	if len(v) <= 8 {
		return "****"
	}
	return v[:5] + "****"
}

// ListAccounts returns all configured provider accounts sorted by name.
func ListAccounts(cfg *Config) []AccountInfo {
	if cfg == nil || len(cfg.Providers) == 0 {
		return nil
	}
	defProv := ""
	if idx := strings.Index(cfg.DefaultModel, ":"); idx > 0 {
		defProv = cfg.DefaultModel[:idx]
	} else {
		defProv = cfg.DefaultModel
	}

	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]AccountInfo, 0, len(names))
	for _, n := range names {
		p := cfg.Providers[n]
		kind := p.Kind
		if kind == "" {
			kind = "openai"
		}
		keyVal := strings.TrimSpace(p.APIKey)
		if p.APIKeyEnv != "" {
			if envVal := strings.TrimSpace(os.Getenv(p.APIKeyEnv)); envVal != "" {
				keyVal = envVal
			}
		}
		hasKey := keyVal != "" || kind == "ollama"
		masked := MaskSecret(keyVal)
		if keyVal == "" && kind == "ollama" {
			masked = "(lokal/tanpa key)"
		}
		out = append(out, AccountInfo{
			Name:         n,
			Kind:         kind,
			BaseURL:      p.BaseURL,
			DefaultModel: p.DefaultModel,
			APIKeyEnv:    p.APIKeyEnv,
			HasKey:       hasKey,
			MaskedKey:    masked,
			IsDefault:    n == defProv,
		})
	}
	return out
}

// UpsertAccount adds or updates a provider account in config.yaml and stores
// its API key (if provided) in $RENSTRA_HOME/.env and the active process env.
func UpsertAccount(name, kind, apiKey, defaultModel, baseURL string, cfg *Config) (*Config, AccountInfo, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return cfg, AccountInfo{}, fmt.Errorf("nama akun/provider wajib diisi")
	}
	if cfg == nil {
		loaded, err := Load()
		if err != nil {
			return nil, AccountInfo{}, err
		}
		cfg = loaded
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}

	existing, exists := cfg.Providers[name]
	if strings.TrimSpace(kind) == "" {
		if exists && existing.Kind != "" {
			kind = existing.Kind
		} else if isKnownProviderKind(name) {
			kind = strings.ToLower(name)
		} else {
			kind = "openai"
		}
	}
	defEntry := defaultProviderEntry(kind)
	if !exists {
		existing = defEntry
	} else {
		existing.Kind = defEntry.Kind
	}
	if strings.TrimSpace(baseURL) != "" {
		existing.BaseURL = strings.TrimSpace(baseURL)
	} else if existing.BaseURL == "" {
		existing.BaseURL = defEntry.BaseURL
	}
	if strings.TrimSpace(defaultModel) != "" {
		existing.DefaultModel = strings.TrimSpace(defaultModel)
	} else if existing.DefaultModel == "" {
		existing.DefaultModel = defaultModelNameForKind(existing.Kind)
	}

	envKey := existing.APIKeyEnv
	if envKey == "" {
		upper := strings.ToUpper(strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
				return r
			}
			return '_'
		}, name))
		envKey = upper + "_API_KEY"
		if isKnownProviderKind(name) && defEntry.APIKeyEnv != "" {
			envKey = defEntry.APIKeyEnv
		}
		existing.APIKeyEnv = envKey
	}

	if strings.TrimSpace(apiKey) != "" && strings.TrimSpace(apiKey) != "-" {
		if err := SaveEnvKey(filepath.Join(Home(), ".env"), envKey, strings.TrimSpace(apiKey)); err != nil {
			return cfg, AccountInfo{}, err
		}
		_ = os.Setenv(envKey, strings.TrimSpace(apiKey))
	}

	cfg.Providers[name] = existing
	if cfg.DefaultModel == "" && existing.DefaultModel != "" {
		cfg.DefaultModel = name + ":" + existing.DefaultModel
	}
	if err := saveConfigStruct(cfg); err != nil {
		return cfg, AccountInfo{}, err
	}

	var info AccountInfo
	for _, a := range ListAccounts(cfg) {
		if a.Name == name {
			info = a
			break
		}
	}
	return cfg, info, nil
}

// RemoveAccount deletes a provider account from config.yaml.
func RemoveAccount(name string, cfg *Config) (*Config, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return cfg, fmt.Errorf("nama akun wajib diisi")
	}
	if cfg == nil {
		loaded, err := Load()
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}
	if _, ok := cfg.Providers[name]; !ok {
		return cfg, fmt.Errorf("akun/provider %q tidak ditemukan", name)
	}
	delete(cfg.Providers, name)
	if err := saveConfigStruct(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// SaveEnvKey updates or appends KEY="val" inside the given .env file (mode 0600).
func SaveEnvKey(envPath, key, val string) error {
	if err := os.MkdirAll(filepath.Dir(envPath), 0o700); err != nil {
		return err
	}
	var lines []string
	found := false
	if raw, err := os.ReadFile(envPath); err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(raw)))
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
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
	return os.WriteFile(envPath, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}

func defaultModelNameForKind(kind string) string {
	switch strings.ToLower(kind) {
	case "anthropic":
		return "claude-3-5-sonnet-latest"
	case "google", "gemini":
		return "gemini-2.5-flash"
	case "openrouter":
		return "openai/gpt-4o-mini"
	case "groq":
		return "llama-3.3-70b-versatile"
	case "ollama":
		return "qwen2.5-coder:7b"
	case "mistral":
		return "mistral-small-latest"
	case "xai":
		return "grok-3-mini"
	default:
		return "gpt-4o-mini"
	}
}
