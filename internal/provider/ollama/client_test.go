package ollama_test

import (
	"errors"
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/ollama"
)

func TestStub(t *testing.T) {
	if got := ollama.Name(); got != "ollama" {
		t.Fatalf("got %q", got)
	}
	p, err := ollama.New(config.ProviderConfig{}, "")
	if p != nil || !errors.Is(err, ollama.ErrNotImplemented) {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
