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

type ApprovalConfig struct {
	Mode          string   `yaml:"mode"`           // "prompt" (default), "auto" (allow all), "deny" (deny all)
	Timeout       Duration `yaml:"timeout"`        // default 120s
	AlwaysAllowed []string `yaml:"always_allowed"` // tool names or "tool:prefix"
	AlwaysDenied  []string `yaml:"always_denied"`  // tool names or "tool:prefix"
}

type TelegramConfig struct {
	Token        string   `yaml:"token"`
	AllowedUsers []int64  `yaml:"allowed_users"`
	PollTimeout  Duration `yaml:"poll_timeout"` // default 30s
}

type ToolsConfig struct {
	Terminal      *bool `yaml:"terminal"`
	ReadFile      *bool `yaml:"read_file"`
	WriteFile     *bool `yaml:"write_file"`
	Patch         *bool `yaml:"patch"`
	WebFetch      *bool `yaml:"web_fetch"`
	WebSearch     *bool `yaml:"web_search"`
	Memory        *bool `yaml:"memory"`
	Skill         *bool `yaml:"skill"`
	Cron          *bool `yaml:"cron"`
	Delegate      *bool `yaml:"delegate"`
	SessionSearch *bool `yaml:"session_search"`
}

type Agent struct {
	SystemPromptPrefix string   `yaml:"system_prompt_prefix"`
	MaxIterations      int      `yaml:"max_iterations"`
	MaxTurnTimeout     Duration `yaml:"max_turn_timeout"`
	AuxModel           string   `yaml:"aux_model"`
	AutoCompact        *bool    `yaml:"auto_compact"`
	CompactThreshold   float64  `yaml:"compact_threshold"`
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
	Fallbacks    []string                  `yaml:"fallbacks"`
	Providers    map[string]ProviderConfig `yaml:"providers"`
	Aliases      map[string]string         `yaml:"aliases"`
	Limits       Limits                    `yaml:"limits"`
	Agent        Agent                     `yaml:"agent"`
	Approval     ApprovalConfig            `yaml:"approval"`
	Telegram     TelegramConfig            `yaml:"telegram"`
	Tools        ToolsConfig               `yaml:"tools"`
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
	autoCompact := true
	return &Config{
		Providers: map[string]ProviderConfig{},
		Aliases:   map[string]string{},
		Limits:    Limits{WarnDailyUSD: 1.0},
		Agent: Agent{
			MaxIterations:    40,
			MaxTurnTimeout:   Duration(15 * time.Minute),
			AutoCompact:      &autoCompact,
			CompactThreshold: 0.7,
		},
		Approval: ApprovalConfig{
			Mode:    "prompt",
			Timeout: Duration(120 * time.Second),
		},
		Telegram: TelegramConfig{
			PollTimeout: Duration(30 * time.Second),
		},
	}
}

// IsEnabled reports whether a tool is switched on. Absent means enabled.
func (t ToolsConfig) IsEnabled(name string) bool {
	var v *bool
	switch name {
	case "terminal":
		v = t.Terminal
	case "read_file":
		v = t.ReadFile
	case "write_file":
		v = t.WriteFile
	case "patch":
		v = t.Patch
	case "web_fetch":
		v = t.WebFetch
	case "memory":
		v = t.Memory
	case "skill":
		v = t.Skill
	case "cron":
		v = t.Cron
	case "delegate":
		v = t.Delegate
	case "web_search":
		v = t.WebSearch
	case "session_search":
		v = t.SessionSearch
	}
	if v == nil {
		return true
	}
	return *v
}

// Check evaluates whether a dangerous tool execution is pre-approved,
// pre-denied, or needs to prompt the user. Returns (decided, allowed).
func (a ApprovalConfig) Check(toolName string, rawArgs []byte) (decided bool, allowed bool) {
	mode := strings.ToLower(strings.TrimSpace(a.Mode))
	if mode == "auto" || mode == "allow_all" || mode == "yes" {
		return true, true
	}
	if mode == "deny" || mode == "deny_all" || mode == "no" {
		return true, false
	}

	argStr := string(rawArgs)

	// Check always_denied rules first
	for _, rule := range a.AlwaysDenied {
		if matchRule(rule, toolName, argStr) {
			return true, false
		}
	}

	// Check always_allowed rules next
	for _, rule := range a.AlwaysAllowed {
		if matchRule(rule, toolName, argStr) {
			return true, true
		}
	}

	// Otherwise, let the approver prompt
	return false, false
}

func matchRule(rule, toolName, argStr string) bool {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return false
	}
	if rule == toolName || rule == "*" {
		return true
	}
	prefix := toolName + ":"
	if strings.HasPrefix(rule, prefix) {
		sub := strings.TrimPrefix(rule, prefix)
		return strings.Contains(argStr, sub)
	}
	return false
}
func (a Agent) AutoCompaction() bool {
	if a.AutoCompact == nil {
		return true
	}
	return *a.AutoCompact
}

// Threshold returns the compaction trigger ratio, clamped to a sane range.
func (a Agent) Threshold() float64 {
	t := a.CompactThreshold
	if t <= 0 {
		t = 0.7
	}
	if t < 0.3 {
		t = 0.3
	}
	if t > 0.95 {
		t = 0.95
	}
	return t
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
			// Only the OpenAI-compatible shape has a fixed default; other
			// kinds leave Path empty so their adapter picks its own.
			switch p.Kind {
			case "", "openai", "custom":
				p.Path = "/chat/completions"
			}
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
