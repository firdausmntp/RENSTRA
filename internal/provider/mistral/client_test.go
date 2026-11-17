package mistral_test

import (
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/mistral"
)

func TestNewProvider(t *testing.T) {
	if got := mistral.Name(); got != "mistral" {
		t.Fatalf("got %q", got)
	}
	p, err := mistral.New(config.ProviderConfig{}, "test-key")
	if err != nil || p == nil || p.Name() != "mistral" {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
