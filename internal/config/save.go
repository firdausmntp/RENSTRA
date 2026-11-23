package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParseTokenCount parses token counts like "128000", "128k", "200K", "1m", "2M".
func ParseTokenCount(raw string) (int, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return 0, fmt.Errorf("jumlah token kosong")
	}
	mult := 1
	switch {
	case strings.HasSuffix(s, "k"):
		mult = 1000
		s = strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult = 1000000
		s = strings.TrimSuffix(s, "m")
	}
	v, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("jumlah token tidak valid: %q", raw)
	}
	return v * mult, nil
}

// AddModelSpec describes a model to be added or updated in config.yaml.
type AddModelSpec struct {
	Provider  string
	Kind      string
	Model     string
	Context   int
	MaxOutput int
	Vision    bool
	PriceIn   float64
	PriceOut  float64
	Alias     string
}

// ParseAddModelArgs parses `/model add <name> [provider] [context] [max_output] [vision|alias=...]`
// or `/model add <provider>:<name> [context] ...`.
func ParseAddModelArgs(args []string, cfg *Config) (AddModelSpec, error) {
	if len(args) == 0 {
		return AddModelSpec{}, fmt.Errorf("pemakaian: /model add <nama_model> [provider] [context] [max_output] [vision] [alias=<nama>]")
	}
	var spec AddModelSpec
	var positional []string

	for _, tok := range args {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		lower := strings.ToLower(tok)
		switch {
		case lower == "vision" || lower == "+vision" || lower == "vision=true" || lower == "vision=1":
			spec.Vision = true
		case lower == "novision" || lower == "vision=false" || lower == "vision=0":
			spec.Vision = false
		case strings.HasPrefix(lower, "context=") || strings.HasPrefix(lower, "ctx="):
			val := tok[strings.Index(tok, "=")+1:]
			n, err := ParseTokenCount(val)
			if err != nil {
				return AddModelSpec{}, err
			}
			spec.Context = n
		case strings.HasPrefix(lower, "max_output=") || strings.HasPrefix(lower, "max="):
			val := tok[strings.Index(tok, "=")+1:]
			n, err := ParseTokenCount(val)
			if err != nil {
				return AddModelSpec{}, err
			}
			spec.MaxOutput = n
		case strings.HasPrefix(lower, "alias="):
			spec.Alias = strings.TrimSpace(tok[strings.Index(tok, "=")+1:])
		case strings.HasPrefix(lower, "provider="):
			spec.Provider = strings.TrimSpace(tok[strings.Index(tok, "=")+1:])
		case strings.HasPrefix(lower, "price_in="):
			if v, err := strconv.ParseFloat(tok[strings.Index(tok, "=")+1:], 64); err == nil {
				spec.PriceIn = v
			}
		case strings.HasPrefix(lower, "price_out="):
			if v, err := strconv.ParseFloat(tok[strings.Index(tok, "=")+1:], 64); err == nil {
				spec.PriceOut = v
			}
		default:
			positional = append(positional, tok)
		}
	}

	if len(positional) == 0 {
		return AddModelSpec{}, fmt.Errorf("nama model wajib diisi")
	}

	first := positional[0]
	rest := positional[1:]
	if idx := strings.Index(first, ":"); idx > 0 {
		left, right := first[:idx], first[idx+1:]
		if (cfg != nil && hasProvider(cfg, left)) || isKnownProviderKind(left) {
			spec.Provider = left
			spec.Model = right
		} else {
			spec.Model = first
		}
	} else {
		spec.Model = first
	}

	for _, item := range rest {
		if n, err := ParseTokenCount(item); err == nil && n >= 256 {
			if spec.Context == 0 {
				spec.Context = n
			} else if spec.MaxOutput == 0 {
				spec.MaxOutput = n
			}
			continue
		}
		if spec.Provider == "" {
			spec.Provider = item
		} else if spec.Alias == "" {
			spec.Alias = item
		}
	}

	if spec.Provider == "" {
		spec.Provider = pickDefaultProviderName(spec.Model, cfg)
	}
	def := DefaultModelConfig(spec.Model)
	if spec.Context <= 0 {
		spec.Context = def.Context
	}
	if spec.MaxOutput <= 0 {
		spec.MaxOutput = def.MaxOutput
	}
	if !spec.Vision && def.Vision {
		spec.Vision = true
	}
	return spec, nil
}

func hasProvider(cfg *Config, name string) bool {
	if cfg == nil {
		return false
	}
	_, ok := cfg.Providers[name]
	return ok
}

func isKnownProviderKind(k string) bool {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case "openai", "anthropic", "google", "gemini", "openrouter", "groq", "ollama", "mistral", "xai", "custom":
		return true
	}
	return false
}

func pickDefaultProviderName(model string, cfg *Config) string {
	if cfg != nil && len(cfg.Providers) > 0 {
		if idx := strings.Index(cfg.DefaultModel, ":"); idx > 0 {
			if p := cfg.DefaultModel[:idx]; hasProvider(cfg, p) {
				return p
			}
		}
		names := make([]string, 0, len(cfg.Providers))
		for n := range cfg.Providers {
			names = append(names, n)
		}
		sort.Strings(names)
		return names[0]
	}
	return "openai"
}

func defaultProviderEntry(nameOrKind string) ProviderConfig {
	kind := strings.ToLower(strings.TrimSpace(nameOrKind))
	switch kind {
	case "anthropic":
		return ProviderConfig{Kind: "anthropic", BaseURL: "https://api.anthropic.com", APIKeyEnv: "ANTHROPIC_API_KEY"}
	case "google", "gemini":
		return ProviderConfig{Kind: "google", BaseURL: "https://generativelanguage.googleapis.com", APIKeyEnv: "GEMINI_API_KEY"}
	case "openrouter":
		return ProviderConfig{Kind: "openrouter", BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY"}
	case "groq":
		return ProviderConfig{Kind: "groq", BaseURL: "https://api.groq.com/openai/v1", APIKeyEnv: "GROQ_API_KEY"}
	case "ollama":
		return ProviderConfig{Kind: "ollama", BaseURL: "http://localhost:11434/v1", APIKeyEnv: "OLLAMA_API_KEY"}
	case "mistral":
		return ProviderConfig{Kind: "mistral", BaseURL: "https://api.mistral.ai/v1", APIKeyEnv: "MISTRAL_API_KEY"}
	case "xai":
		return ProviderConfig{Kind: "xai", BaseURL: "https://api.x.ai/v1", APIKeyEnv: "XAI_API_KEY"}
	default:
		return ProviderConfig{Kind: "openai", BaseURL: "https://api.openai.com/v1", Path: "/chat/completions", APIKeyEnv: "OPENAI_API_KEY"}
	}
}

// AddModel registers or updates a model in cfg and persists it to $RENSTRA_HOME/config.yaml.
func AddModel(spec AddModelSpec, cfg *Config) (*Config, error) {
	if strings.TrimSpace(spec.Model) == "" {
		return cfg, fmt.Errorf("nama model kosong")
	}
	if cfg == nil {
		loaded, err := Load()
		if err != nil {
			return nil, err
		}
		cfg = loaded
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]ProviderConfig{}
	}
	if cfg.Aliases == nil {
		cfg.Aliases = map[string]string{}
	}
	provName := strings.TrimSpace(spec.Provider)
	if provName == "" {
		provName = pickDefaultProviderName(spec.Model, cfg)
	}
	pc, ok := cfg.Providers[provName]
	if !ok {
		pc = defaultProviderEntry(provName)
		pc.DefaultModel = spec.Model
	}
	if pc.Models == nil {
		pc.Models = map[string]ModelConfig{}
	}
	if pc.DefaultModel == "" {
		pc.DefaultModel = spec.Model
	}
	pc.Models[spec.Model] = ModelConfig{
		Context:   spec.Context,
		MaxOutput: spec.MaxOutput,
		Vision:    spec.Vision,
		PriceIn:   spec.PriceIn,
		PriceOut:  spec.PriceOut,
	}
	cfg.Providers[provName] = pc
	if spec.Alias != "" {
		cfg.Aliases[spec.Alias] = provName + ":" + spec.Model
	}
	if cfg.DefaultModel == "" {
		cfg.DefaultModel = provName + ":" + spec.Model
	}
	if err := saveConfigStruct(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// RemoveModel deletes an alias or a model entry from cfg and persists config.yaml.
func RemoveModel(ref string, cfg *Config) (*Config, string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return cfg, "", fmt.Errorf("pemakaian: /model rm <nama_model|alias|provider:model>")
	}
	if cfg == nil {
		loaded, err := Load()
		if err != nil {
			return nil, "", err
		}
		cfg = loaded
	}
	var removed []string
	if _, ok := cfg.Aliases[ref]; ok {
		delete(cfg.Aliases, ref)
		removed = append(removed, "alias "+ref)
	}
	if idx := strings.Index(ref, ":"); idx > 0 {
		pName, mName := ref[:idx], ref[idx+1:]
		if pc, ok := cfg.Providers[pName]; ok && pc.Models != nil {
			if _, mok := pc.Models[mName]; mok {
				delete(pc.Models, mName)
				cfg.Providers[pName] = pc
				removed = append(removed, pName+":"+mName)
			}
		}
	} else {
		for pName, pc := range cfg.Providers {
			if pc.Models != nil {
				if _, mok := pc.Models[ref]; mok {
					delete(pc.Models, ref)
					cfg.Providers[pName] = pc
					removed = append(removed, pName+":"+ref)
				}
			}
		}
	}
	if len(removed) == 0 {
		return cfg, "", fmt.Errorf("model atau alias %q tidak ditemukan di config.yaml", ref)
	}
	if err := saveConfigStruct(cfg); err != nil {
		return cfg, "", err
	}
	return cfg, strings.Join(removed, ", "), nil
}

func saveConfigStruct(cfg *Config) error {
	home := Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(home, "config.yaml")
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// SetDefaultModel validates ref against the current configuration and writes
// default_model to $RENSTRA_HOME/config.yaml, preserving existing YAML nodes.
func SetDefaultModel(ref string) (resolvedModel, storedRef string, err error) {
	cfg, err := Load()
	if err != nil {
		return "", "", err
	}
	pcfg, model, err := Resolve(ref, cfg)
	if err != nil {
		return "", "", err
	}

	storedRef = strings.TrimSpace(ref)
	if _, isAlias := cfg.Aliases[storedRef]; !isAlias && !strings.Contains(storedRef, ":") {
		for name, p := range cfg.Providers {
			if p.Kind == pcfg.Kind && p.BaseURL == pcfg.BaseURL && p.DefaultModel == pcfg.DefaultModel {
				storedRef = name + ":" + model
				break
			}
		}
	}

	home := Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return "", "", err
	}
	cfgPath := filepath.Join(home, "config.yaml")

	var doc yaml.Node
	raw, rerr := os.ReadFile(cfgPath)
	if rerr == nil && len(strings.TrimSpace(string(raw))) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return "", "", fmt.Errorf("%s: %w", cfgPath, err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", "", fmt.Errorf("%s: format YAML root bukan mapping", cfgPath)
	}
	updated := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "default_model" {
			root.Content[i+1].Kind, root.Content[i+1].Tag, root.Content[i+1].Value = yaml.ScalarNode, "!!str", storedRef
			updated = true
			break
		}
	}
	if !updated {
		root.Content = append([]*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: "default_model"},
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: storedRef},
		}, root.Content...)
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", "", err
	}
	tmp := cfgPath + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return "", "", err
	}
	if err := os.Rename(tmp, cfgPath); err != nil {
		_ = os.Remove(tmp)
		return "", "", err
	}
	return model, storedRef, nil
}
