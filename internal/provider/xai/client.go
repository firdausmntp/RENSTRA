package xai

import (
	"errors"

	"renstra/internal/config"
	"renstra/internal/provider"
)

var ErrNotImplemented = errors.New("provider xai belum diimplementasikan")

func New(cfg config.ProviderConfig, apiKey string) (provider.Provider, error) {
	return nil, ErrNotImplemented
}

func Name() string { return "xai" }
