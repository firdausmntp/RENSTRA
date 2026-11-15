package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"renstra/internal/config"
)

func TestHomeUsesEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RENSTRA_HOME", dir)
	if got := config.Home(); got != dir {
		t.Fatalf("Home() = %q, want %q", got, dir)
	}
}

func TestResolveAlias(t *testing.T) {
	cfg := &config.Config{
		Providers: map[string]config.ProviderConfig{"anthropic": {Kind: "anthropic", DefaultModel: "claude-sonnet-4-5"}},
		Aliases:   map[string]string{"sonnet": "anthropic:claude-sonnet-4-5"},
	}
	p, model, err := config.Resolve("sonnet", cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p.Kind != "anthropic" || model != "claude-sonnet-4-5" {
		t.Fatalf("got %q/%q", p.Kind, model)
	}
}

func TestResolveUnknownProviderFails(t *testing.T) {
	if _, _, err := config.Resolve("nope:x", &config.Config{}); err == nil {
		t.Fatal("want error")
	}
}

func TestLoadDoesNotOverwriteAnthropicPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RENSTRA_HOME", dir)

	yamlContent := `
default_model: "ant:claude-3-5-sonnet"
providers:
  oai:
    kind: "openai"
    base_url: "https://api.openai.com/v1"
  ant:
    kind: "anthropic"
    base_url: "https://api.anthropic.com"
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	if p := cfg.Providers["oai"]; p.Path != "/chat/completions" {
		t.Fatalf("openai path = %q, want /chat/completions", p.Path)
	}
	if p := cfg.Providers["ant"]; p.Path != "" {
		t.Fatalf("anthropic path = %q, want empty so adapter defaults to /v1/messages", p.Path)
	}
}
