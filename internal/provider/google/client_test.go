package google_test

import (
	"errors"
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/google"
)

func TestStub(t *testing.T) {
	if got := google.Name(); got != "google" {
		t.Fatalf("got %q", got)
	}
	p, err := google.New(config.ProviderConfig{}, "")
	if p != nil || !errors.Is(err, google.ErrNotImplemented) {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
