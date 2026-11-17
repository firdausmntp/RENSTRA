package xai_test

import (
	"testing"

	"renstra/internal/config"
	"renstra/internal/provider/xai"
)

func TestNewProvider(t *testing.T) {
	if got := xai.Name(); got != "xai" {
		t.Fatalf("got %q", got)
	}
	p, err := xai.New(config.ProviderConfig{}, "test-key")
	if err != nil || p == nil || p.Name() != "xai" {
		t.Fatalf("p=%v err=%v", p, err)
	}
}
