package groq_test

import (
	"errors"
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/groq"
)

func TestStub(t *testing.T) {
	if got := groq.Name(); got != "groq" {
		t.Fatalf("got %q", got)
	}
	p, err := groq.New(config.ProviderConfig{}, "")
	if p != nil || !errors.Is(err, groq.ErrNotImplemented) {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
