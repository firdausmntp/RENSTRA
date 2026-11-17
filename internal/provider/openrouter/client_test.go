package openrouter_test

import (
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/openrouter"
)

func TestNewProvider(t *testing.T) {
	if got := openrouter.Name(); got != "openrouter" {
		t.Fatalf("got %q", got)
	}
	p, err := openrouter.New(config.ProviderConfig{}, "test-key")
	if err != nil || p == nil || p.Name() != "openrouter" {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
