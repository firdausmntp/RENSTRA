package mistral_test

import (
	"errors"
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/mistral"
)

func TestStub(t *testing.T) {
	if got := mistral.Name(); got != "mistral" {
		t.Fatalf("got %q", got)
	}
	p, err := mistral.New(config.ProviderConfig{}, "")
	if p != nil || !errors.Is(err, mistral.ErrNotImplemented) {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
