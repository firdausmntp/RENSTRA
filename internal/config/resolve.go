package config

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Resolve turns "alias", "provider", "provider:model", or a plain "model" name
// into a provider entry and model name.
func Resolve(ref string, cfg *Config) (ProviderConfig, string, error) {
	ref = strings.TrimSpace(ref)
	for i := 0; i < 5; i++ {
		target, ok := cfg.Aliases[ref]
		if !ok {
			break
		}
		ref = strings.TrimSpace(target)
	}
	if ref == "" {
		return ProviderConfig{}, "", errors.New("model belum diset; jalankan `renstra setup` atau pilih model dengan `renstra chat <provider>:<model>`")
	}

	if idx := strings.Index(ref, ":"); idx >= 0 {
		providerName, model := ref[:idx], ref[idx+1:]
		if p, ok := cfg.Providers[providerName]; ok {
			if model == "" {
				model = p.DefaultModel
			}
			if model == "" {
				return ProviderConfig{}, "", fmt.Errorf("provider %q tidak punya model default", providerName)
			}
			return p, model, nil
		}
		if p, m, ok := resolvePlainModel(ref, cfg); ok {
			return p, m, nil
		}
		return unknownProviderError(providerName, cfg)
	}

	if p, ok := cfg.Providers[ref]; ok {
		if p.DefaultModel == "" {
			return ProviderConfig{}, "", fmt.Errorf("provider %q tidak punya model default", ref)
		}
		return p, p.DefaultModel, nil
	}
	if p, m, ok := resolvePlainModel(ref, cfg); ok {
		return p, m, nil
	}
	return unknownProviderError(ref, cfg)
}

func resolvePlainModel(modelName string, cfg *Config) (ProviderConfig, string, bool) {
	if cfg == nil || len(cfg.Providers) == 0 {
		return ProviderConfig{}, "", false
	}
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)

	// 1. Exact match in any configured provider's Models or DefaultModel.
	for _, n := range names {
		p := cfg.Providers[n]
		if p.DefaultModel == modelName {
			return p, modelName, true
		}
		if _, ok := p.Models[modelName]; ok {
			return p, modelName, true
		}
	}

	// 2. Model names with tags (e.g. llama3.2:latest or org/model:free).
	if strings.Contains(modelName, ":") {
		if len(names) == 1 {
			p := cfg.Providers[names[0]]
			if p.Kind == "ollama" || p.Kind == "openrouter" || strings.Contains(modelName, "/") {
				return p, modelName, true
			}
		}
		return ProviderConfig{}, "", false
	}

	// 3. Single provider configured -> assume model belongs to it.
	if len(names) == 1 {
		return cfg.Providers[names[0]], modelName, true
	}

	// 4. Infer provider by well-known model prefix across configured kinds.
	lower := strings.ToLower(modelName)
	wantKind := ""
	switch {
	case strings.HasPrefix(lower, "claude"):
		wantKind = "anthropic"
	case strings.HasPrefix(lower, "gemini"):
		wantKind = "google"
	case strings.HasPrefix(lower, "grok"):
		wantKind = "xai"
	case strings.HasPrefix(lower, "mistral") || strings.HasPrefix(lower, "codestral"):
		wantKind = "mistral"
	}
	if wantKind != "" {
		for _, n := range names {
			if cfg.Providers[n].Kind == wantKind {
				return cfg.Providers[n], modelName, true
			}
		}
	}

	// 5. Fall back to the provider used by cfg.DefaultModel if resolvable.
	if cfg.DefaultModel != "" && cfg.DefaultModel != modelName {
		if defProv, _, err := Resolve(cfg.DefaultModel, cfg); err == nil {
			return defProv, modelName, true
		}
	}
	return ProviderConfig{}, "", false
}

func unknownProviderError(providerName string, cfg *Config) (ProviderConfig, string, error) {
	if cfg == nil || len(cfg.Providers) == 0 {
		return ProviderConfig{}, "", fmt.Errorf("provider %q tidak ditemukan dan belum ada provider terkonfigurasi; jalankan `renstra setup`", providerName)
	}
	names := make([]string, 0, len(cfg.Providers))
	for n := range cfg.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	return ProviderConfig{}, "", fmt.Errorf("provider %q tidak dikenal (tersedia: %s)", providerName, strings.Join(names, ", "))
}
