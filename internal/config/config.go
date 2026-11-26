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
	Token                   string   `yaml:"token"`
	AllowedUsers            []int64  `yaml:"allowed_users"`
	AllowedChats            []int64  `yaml:"allowed_chats"`
	AllowedTopics           []int64  `yaml:"allowed_topics"`
	GroupMode               string   `yaml:"group_mode"` // "always" (default) or "mention"
	PollTimeout             Duration `yaml:"poll_timeout"` // default 30s
	CanJoinGroups           *bool    `yaml:"can_join_groups"`
	CanReadAllGroupMessages *bool    `yaml:"can_read_all_group_messages"`
	HasTopicsEnabled        *bool    `yaml:"has_topics_enabled"`
	HasTopicSidebar         *bool    `yaml:"has_topic_sidebar"`
	CallbackQueryMode       string   `yaml:"callback_query_mode"`
}

type DashboardConfig struct {
	Enabled    *bool  `yaml:"enabled"`
	Addr       string `yaml:"addr"`
	Port       int    `yaml:"port"`
	Token      string `yaml:"token"`
	UIRepo     string `yaml:"ui_repo"`
	UIBranch   string `yaml:"ui_branch"`
	AutoSyncUI *bool  `yaml:"auto_sync_ui"`
}

type ToolsConfig struct {
	Terminal          *bool `yaml:"terminal"`
	ReadFile          *bool `yaml:"read_file"`
	WriteFile         *bool `yaml:"write_file"`
	Patch             *bool `yaml:"patch"`
	WebFetch          *bool `yaml:"web_fetch"`
	WebSearch         *bool `yaml:"web_search"`
	Memory            *bool `yaml:"memory"`
	Skill             *bool `yaml:"skill"`
	Cron              *bool `yaml:"cron"`
	Delegate          *bool `yaml:"delegate"`
	SessionSearch     *bool `yaml:"session_search"`
	SendFile          *bool `yaml:"send_file"`
	SendTelegram      *bool `yaml:"send_telegram"`
	TelegramManage    *bool `yaml:"telegram_manage"`
	AllowPrivateFetch bool  `yaml:"allow_private_web_fetch"`
}

type Agent struct {
	SystemPromptPrefix string   `yaml:"system_prompt_prefix"`
	MaxIterations      int      `yaml:"max_iterations"`
	MaxTurnTimeout     Duration `yaml:"max_turn_timeout"`
	AuxModel           string   `yaml:"aux_model"`
	AutoCompact        *bool    `yaml:"auto_compact"`
	CompactThreshold   float64  `yaml:"compact_threshold"`
	FileRetentionDays  int      `yaml:"file_retention_days"`
}

type ModelConfig struct {
	Context        int     `yaml:"context"`
	MaxOutput      int     `yaml:"max_output"`
	Tools          *bool   `yaml:"tools"`
	Vision         bool    `yaml:"vision"`
	PriceIn        float64 `yaml:"price_in"`
	PriceOut       float64 `yaml:"price_out"`
	visionExplicit bool
}

// UnmarshalYAML tracks whether `vision` was explicitly set so omitted entries
// can inherit DefaultModelConfig.Vision while explicit `vision: false` is respected.
func (m *ModelConfig) UnmarshalYAML(value *yaml.Node) error {
	type rawModelConfig ModelConfig
	var raw rawModelConfig
	if err := value.Decode(&raw); err != nil {
		return err
	}
	if value.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(value.Content); i += 2 {
			if value.Content[i].Value == "vision" {
				raw.visionExplicit = true
				break
			}
		}
	}
	*m = ModelConfig(raw)
	return nil
}

type ProviderConfig struct {
	Kind         string                 `yaml:"kind"`
	APIStyle     string                 `yaml:"api_style"`
	BaseURL      string                 `yaml:"base_url"`
	Path         string                 `yaml:"path"`
	APIKey       string                 `yaml:"api_key"`
	APIKeyEnv    string                 `yaml:"api_key_env"`
	Headers      map[string]string      `yaml:"headers"`
	Proxy        string                 `yaml:"proxy"`
	Timeout      Duration               `yaml:"timeout"`
	Models       map[string]ModelConfig `yaml:"models"`
	DefaultModel string                 `yaml:"default_model"`
}

type Config struct {
	DefaultModel  string                    `yaml:"default_model"`
	Fallbacks     []string                  `yaml:"fallbacks"`
	SmartFallback *bool                     `yaml:"smart_fallback"`
	Providers     map[string]ProviderConfig `yaml:"providers"`
	Aliases      map[string]string         `yaml:"aliases"`
	Limits       Limits                    `yaml:"limits"`
	Agent        Agent                     `yaml:"agent"`
	Approval     ApprovalConfig            `yaml:"approval"`
	Telegram     TelegramConfig            `yaml:"telegram"`
	Dashboard    DashboardConfig           `yaml:"dashboard"`
	Tools        ToolsConfig               `yaml:"tools"`
}

// DashboardListenAddr resolves the HTTP bind address for the web dashboard.
func (c *Config) DashboardListenAddr(overridePort int, overrideAddr string) (addr string, enabled bool) {
	if c != nil && c.Dashboard.Enabled != nil && !*c.Dashboard.Enabled && overridePort <= 0 && strings.TrimSpace(overrideAddr) == "" {
		return "", false
	}
	if strings.TrimSpace(overrideAddr) != "" {
		return strings.TrimSpace(overrideAddr), true
	}
	if overridePort > 0 {
		host := "127.0.0.1"
		if c != nil && strings.Contains(c.Dashboard.Addr, ":") {
			if h := strings.SplitN(c.Dashboard.Addr, ":", 2)[0]; h != "" {
				host = h
			} else if strings.HasPrefix(c.Dashboard.Addr, ":") {
				host = ""
			}
		}
		return fmt.Sprintf("%s:%d", host, overridePort), true
	}
	if c != nil {
		if strings.TrimSpace(c.Dashboard.Addr) != "" {
			return strings.TrimSpace(c.Dashboard.Addr), true
		}
		if c.Dashboard.Port > 0 {
			return fmt.Sprintf("127.0.0.1:%d", c.Dashboard.Port), true
		}
	}
	return "127.0.0.1:8080", true
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
			MaxIterations:    80,
			MaxTurnTimeout:   Duration(30 * time.Minute),
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
	case "send_file":
		v = t.SendFile
	case "send_telegram":
		v = t.SendTelegram
	case "telegram_manage":
		v = t.TelegramManage
	}
	if v == nil {
		return true
	}
	return *v
}

func (a Agent) AutoCompaction() bool {
	if a.AutoCompact == nil {
		return true
	}
	return *a.AutoCompact
}

// SmartFallbackEnabled reports whether automatic multi-provider discovery is
// active (when smart_fallback is true or fallbacks contains "auto").
func (c *Config) SmartFallbackEnabled() bool {
	if c == nil {
		return false
	}
	if c.SmartFallback != nil {
		return *c.SmartFallback
	}
	for _, f := range c.Fallbacks {
		if strings.EqualFold(strings.TrimSpace(f), "auto") {
			return true
		}
	}
	return false
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

// LoadDotEnv reads key-value pairs from a .env file and sets them in the
// process environment if not already defined. It ignores comments and empty lines.
func LoadDotEnv(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		idx := strings.Index(line, "=")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, val)
		}
	}
	return nil
}

func Load() (*Config, error) {
	// Auto-load .env from Home() and current directory if present.
	_ = LoadDotEnv(filepath.Join(Home(), ".env"))
	_ = LoadDotEnv(".env")

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

// ModelConfig returns the capability table entry, with smart defaults inferred
// from known model families when not explicitly configured in config.yaml.
func (p ProviderConfig) ModelConfig(model string) ModelConfig {
	def := DefaultModelConfig(model)
	m, ok := p.Models[model]
	if !ok {
		return def
	}
	if m.Context == 0 {
		m.Context = def.Context
	}
	if m.MaxOutput == 0 {
		m.MaxOutput = def.MaxOutput
	}
	if !m.visionExplicit && !m.Vision {
		m.Vision = def.Vision
	}
	return m
}
