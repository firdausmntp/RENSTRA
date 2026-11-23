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

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	envContent := `
# test comment
TEST_RENSTRA_KEY_A=secret123
export TEST_RENSTRA_KEY_B="secret456"
TEST_RENSTRA_KEY_C='secret789'
`
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := config.LoadDotEnv(envPath); err != nil {
		t.Fatalf("LoadDotEnv failed: %v", err)
	}

	if got := os.Getenv("TEST_RENSTRA_KEY_A"); got != "secret123" {
		t.Errorf("KEY_A = %q, want secret123", got)
	}
	if got := os.Getenv("TEST_RENSTRA_KEY_B"); got != "secret456" {
		t.Errorf("KEY_B = %q, want secret456", got)
	}
	if got := os.Getenv("TEST_RENSTRA_KEY_C"); got != "secret789" {
		t.Errorf("KEY_C = %q, want secret789", got)
	}
}

func TestResolvePlainModelAndSetDefaultModel(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RENSTRA_HOME", dir)

	yamlContent := `
default_model: "oai:gpt-4o-mini"
providers:
  oai:
    kind: "openai"
    base_url: "https://api.openai.com/v1"
    default_model: "gpt-4o-mini"
  ant:
    kind: "anthropic"
    base_url: "https://api.anthropic.com"
    default_model: "claude-3-5-sonnet"
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}

	// Plain model without prefix resolves via model prefix inference or default provider
	p, m, err := config.Resolve("claude-sonnet-4-5", cfg)
	if err != nil || p.Kind != "anthropic" || m != "claude-sonnet-4-5" {
		t.Fatalf("Resolve(claude-sonnet-4-5) = %q/%q err=%v", p.Kind, m, err)
	}
	p2, m2, err := config.Resolve("gpt-4o", cfg)
	if err != nil || p2.Kind != "openai" || m2 != "gpt-4o" {
		t.Fatalf("Resolve(gpt-4o) = %q/%q err=%v", p2.Kind, m2, err)
	}

	// SetDefaultModel persists to config.yaml
	model, stored, err := config.SetDefaultModel("gpt-4o")
	if err != nil || model != "gpt-4o" || stored != "oai:gpt-4o" {
		t.Fatalf("SetDefaultModel = %q/%q err=%v", model, stored, err)
	}
	reloaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DefaultModel != "oai:gpt-4o" {
		t.Fatalf("reloaded DefaultModel = %q, want oai:gpt-4o", reloaded.DefaultModel)
	}

	// ParseAddModelArgs & AddModel
	spec, err := config.ParseAddModelArgs([]string{"claude-3-7-sonnet", "ant", "200k", "8192", "vision", "alias=s37"}, reloaded)
	if err != nil {
		t.Fatalf("ParseAddModelArgs: %v", err)
	}
	if spec.Model != "claude-3-7-sonnet" || spec.Provider != "ant" || spec.Context != 200000 || spec.MaxOutput != 8192 || !spec.Vision || spec.Alias != "s37" {
		t.Fatalf("unexpected spec: %+v", spec)
	}
	afterAdd, err := config.AddModel(spec, reloaded)
	if err != nil {
		t.Fatalf("AddModel: %v", err)
	}
	mc := afterAdd.Providers["ant"].ModelConfig("claude-3-7-sonnet")
	if mc.Context != 200000 || mc.MaxOutput != 8192 || !mc.Vision || afterAdd.Aliases["s37"] != "ant:claude-3-7-sonnet" {
		t.Fatalf("added model config mismatch: %+v aliases=%+v", mc, afterAdd.Aliases)
	}
}

func TestAccountManagement(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("RENSTRA_HOME", dir)
	cfgPath := filepath.Join(dir, "config.yaml")
	raw := `default_model: openai:gpt-4o-mini
providers:
  openai:
    kind: openai
    api_key_env: OPENAI_API_KEY
    default_model: gpt-4o-mini
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "sk-test-12345678")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	updated, info, err := config.UpsertAccount("openrouter2", "openrouter", "sk-or-v1-1234567890abcdef", "anthropic/claude-3.5-sonnet", "", cfg)
	if err != nil {
		t.Fatalf("UpsertAccount: %v", err)
	}
	if !info.HasKey || info.DefaultModel != "anthropic/claude-3.5-sonnet" {
		t.Fatalf("unexpected AccountInfo: %+v", info)
	}
	accounts := config.ListAccounts(updated)
	if len(accounts) != 2 {
		t.Fatalf("len(accounts) = %d, want 2", len(accounts))
	}
	afterRm, err := config.RemoveAccount("openrouter2", updated)
	if err != nil {
		t.Fatalf("RemoveAccount: %v", err)
	}
	if len(afterRm.Providers) != 1 {
		t.Fatalf("expected 1 provider after remove, got %d", len(afterRm.Providers))
	}
}

