package config_test

import (
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
