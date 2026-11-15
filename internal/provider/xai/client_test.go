package xai_test

import (
	"errors"
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/xai"
)

func TestStub(t *testing.T) {
	if got := xai.Name(); got != "xai" {
		t.Fatalf("got %q", got)
	}
	p, err := xai.New(config.ProviderConfig{}, "")
	if p != nil || !errors.Is(err, xai.ErrNotImplemented) {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
