package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration exists because yaml.v3 cannot decode "120s" into time.Duration.
type Duration time.Duration

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

type Limits struct {
	WarnDailyUSD   float64 `yaml:"warn_daily_usd"`
	MaxDailyUSD    float64 `yaml:"max_daily_usd"`
	MaxDailyTokens int64   `yaml:"max_daily_tokens"`
}

type Agent struct {
	MaxIterations  int      `yaml:"max_iterations"`
	MaxTurnTimeout Duration `yaml:"max_turn_timeout"`
	AuxModel       string   `yaml:"aux_model"`
}

type ModelConfig struct {
	Context   int     `yaml:"context"`
	MaxOutput int     `yaml:"max_output"`
	Tools     *bool   `yaml:"tools"`
	Vision    bool    `yaml:"vision"`
	PriceIn   float64 `yaml:"price_in"`
	PriceOut  float64 `yaml:"price_out"`
}

type ProviderConfig struct {
	Kind         string                 `yaml:"kind"`
	APIStyle     string                 `yaml:"api_style"`
	BaseURL      string                 `yaml:"base_url"`
	Path         string                 `yaml:"path"`
	APIKeyEnv    string                 `yaml:"api_key_env"`
	Headers      map[string]string      `yaml:"headers"`
	Proxy        string                 `yaml:"proxy"`
	Timeout      Duration               `yaml:"timeout"`
	Models       map[string]ModelConfig `yaml:"models"`
	DefaultModel string                 `yaml:"default_model"`
}

type Config struct {
	DefaultModel string                    `yaml:"default_model"`
	Providers    map[string]ProviderConfig `yaml:"providers"`
	Aliases      map[string]string         `yaml:"aliases"`
	Limits       Limits                    `yaml:"limits"`
	Agent        Agent                     `yaml:"agent"`
}

func Home() string {
	if v := os.Getenv("RENSTRA_HOME"); v != "" {
		return v
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return ".renstra"
	}
	return filepath.Join(base, "renstra")
}

func defaults() *Config {
	return &Config{
		Providers: map[string]ProviderConfig{},
		Aliases:   map[string]string{},
		Limits:    Limits{WarnDailyUSD: 1.0},
		Agent: Agent{
			MaxIterations:  40,
			MaxTurnTimeout: Duration(15 * time.Minute),
		},
	}
}

func Load() (*Config, error) {
	cfg := defaults()
	path := filepath.Join(Home(), "config.yaml")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for name, p := range cfg.Providers {
		if p.Timeout == 0 {
			p.Timeout = Duration(120 * time.Second)
		}
		if p.Path == "" {
			p.Path = "/chat/completions"
		}
		cfg.Providers[name] = p
	}
	return cfg, nil
}

// Resolve turns "alias", "provider" or "provider:model" into a provider entry and model name.
func Resolve(ref string, cfg *Config) (ProviderConfig, string, error) {
	for i := 0; i < 5; i++ {
		target, ok := cfg.Aliases[ref]
		if !ok {
			break
		}
		ref = target
	}
	if ref == "" {
		return ProviderConfig{}, "", errors.New("model belum diset")
	}

	providerName, model := ref, ""
	if idx := strings.Index(ref, ":"); idx >= 0 {
		providerName, model = ref[:idx], ref[idx+1:]
	}

	p, ok := cfg.Providers[providerName]
	if !ok {
		names := make([]string, 0, len(cfg.Providers))
		for n := range cfg.Providers {
			names = append(names, n)
		}
		return ProviderConfig{}, "", fmt.Errorf("provider %q tidak dikenal (tersedia: %s)", providerName, strings.Join(names, ", "))
	}
	if model == "" {
		model = p.DefaultModel
	}
	if model == "" {
		return ProviderConfig{}, "", fmt.Errorf("provider %q tidak punya model default", providerName)
	}
	return p, model, nil
}

// ModelConfig returns the capability table entry, with safe defaults filled in.
func (p ProviderConfig) ModelConfig(model string) ModelConfig {
	m, ok := p.Models[model]
	if !ok {
		m = ModelConfig{Context: 32000}
	}
	if m.Context == 0 {
		m.Context = 32000
	}
	return m
}
