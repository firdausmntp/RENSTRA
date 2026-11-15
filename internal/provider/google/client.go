package google

import (
	"errors"

	"renstra/internal/config"
	"renstra/internal/provider"
)

var ErrNotImplemented = errors.New("provider google belum diimplementasikan")

func New(cfg config.ProviderConfig, apiKey string) (provider.Provider, error) {
	return nil, ErrNotImplemented
}

func Name() string { return "google" }
