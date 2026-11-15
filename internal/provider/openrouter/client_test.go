package openrouter_test

import (
	"errors"
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/openrouter"
)

func TestStub(t *testing.T) {
	if got := openrouter.Name(); got != "openrouter" {
		t.Fatalf("got %q", got)
	}
	p, err := openrouter.New(config.ProviderConfig{}, "")
	if p != nil || !errors.Is(err, openrouter.ErrNotImplemented) {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
