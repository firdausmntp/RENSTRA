package xai

import (
	"context"

	"renstra/internal/config"
	"renstra/internal/provider"
	"renstra/internal/provider/openai"
)

const defaultBaseURL = "https://api.x.ai/v1"

type Client struct {
	inner *openai.Client
}

func New(cfg config.ProviderConfig, apiKey string) (provider.Provider, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.Path == "" {
		cfg.Path = "/chat/completions"
	}
	c, err := openai.New(cfg, apiKey)
	if err != nil {
		return nil, err
	}
	return &Client{inner: c}, nil
}

func Name() string { return "xai" }

func (c *Client) Name() string { return "xai" }

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	return c.inner.Stream(ctx, req)
}
