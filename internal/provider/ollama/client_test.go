package ollama_test

import (
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/ollama"
)

func TestNewProvider(t *testing.T) {
	if got := ollama.Name(); got != "ollama" {
		t.Fatalf("got %q", got)
	}
	p, err := ollama.New(config.ProviderConfig{}, "")
	if err != nil || p == nil || p.Name() != "ollama" {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
