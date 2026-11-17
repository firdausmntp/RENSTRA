package groq_test

import (
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/groq"
)

func TestNewProvider(t *testing.T) {
	if got := groq.Name(); got != "groq" {
		t.Fatalf("got %q", got)
	}
	p, err := groq.New(config.ProviderConfig{}, "test-key")
	if err != nil || p == nil || p.Name() != "groq" {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
