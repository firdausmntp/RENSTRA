package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"renstra/internal/config"
	"renstra/internal/provider"
)

type Client struct {
	cfg    config.ProviderConfig
	apiKey string
	http   *http.Client
}

func New(cfg config.ProviderConfig, apiKey string) (*Client, error) {
	timeout := time.Duration(cfg.Timeout)
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 32
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	if cfg.Proxy != "" {
		u, err := url.Parse(cfg.Proxy)
		if err != nil || u.Host == "" {
			return nil, fmt.Errorf("proxy tidak valid: %q", cfg.Proxy)
		}
		transport.Proxy = http.ProxyURL(u)
	}
	return &Client{
		cfg:    cfg,
		apiKey: apiKey,
		http:   &http.Client{Timeout: timeout, Transport: transport},
	}, nil
}

func (c *Client) Name() string { return "openai" }

type wireMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type wireRequest struct {
	Model     string        `json:"model"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
	MaxTokens int           `json:"max_tokens,omitempty"`
	Stream    bool          `json:"stream"`
}

type streamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		Message *struct {
			Content   string         `json:"content"`
			ToolCalls []wireToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, err
	}

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + c.cfg.Path

	ch := make(chan provider.Event)
	go func() {
		defer close(ch)

		for attempt := 1; attempt <= provider.MaxAttempts; attempt++ {
			if ctx.Err() != nil {
				send(ctx, ch, provider.Event{Err: ctx.Err()})
				return
			}

			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
			if err != nil {
				send(ctx, ch, provider.Event{Err: err})
				return
			}
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq.Header.Set("Accept", "text/event-stream")
			if c.apiKey != "" {
				httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
			}
			for k, v := range c.cfg.Headers {
				httpReq.Header.Set(k, v)
			}

			resp, err := c.http.Do(httpReq)
			if err != nil {
				if ctx.Err() != nil {
					send(ctx, ch, provider.Event{Err: ctx.Err()})
					return
				}
				if attempt < provider.MaxAttempts {
					select {
					case <-ctx.Done():
						send(ctx, ch, provider.Event{Err: ctx.Err()})
						return
					case <-time.After(provider.Backoff(attempt, "")):
						continue
					}
				}
				send(ctx, ch, provider.Event{Err: err})
				return
			}

			if provider.Retryable(resp.StatusCode) && attempt < provider.MaxAttempts {
				retryAfter := resp.Header.Get("Retry-After")
				resp.Body.Close()
				select {
				case <-ctx.Done():
					send(ctx, ch, provider.Event{Err: ctx.Err()})
					return
				case <-time.After(provider.Backoff(attempt, retryAfter)):
					continue
				}
			}

			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				send(ctx, ch, provider.Event{Err: fmt.Errorf("provider %s: HTTP %d: %s", c.cfg.BaseURL, resp.StatusCode, strings.TrimSpace(string(raw)))})
				return
			}

			c.consume(ctx, resp.Body, ch)
			resp.Body.Close()
			return
		}
	}()
	return ch, nil
}

func send(ctx context.Context, ch chan<- provider.Event, ev provider.Event) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Client) buildRequest(req provider.Request) wireRequest {
	out := wireRequest{Model: req.Model, Stream: true, MaxTokens: req.MaxTokens, Messages: []wireMessage{}}
	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role), ToolCallID: m.ToolCallID}
		if len(m.Attachments) > 0 {
			var parts []map[string]any
			if m.Content != "" {
				parts = append(parts, map[string]any{"type": "text", "text": m.Content})
			}
			for _, att := range m.Attachments {
				data, err := os.ReadFile(att.Path)
				if err != nil {
					continue
				}
				mime := att.MimeType
				if mime == "" {
					mime = "image/jpeg"
				}
				parts = append(parts, map[string]any{
					"type": "image_url",
					"image_url": map[string]string{
						"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data),
					},
				})
			}
			if len(parts) > 0 {
				wm.Content = parts
			} else if m.Content != "" {
				wm.Content = m.Content
			}
		} else if m.Content != "" {
			wm.Content = m.Content
		}
		for _, tc := range m.ToolCalls {
			var w wireToolCall
			w.ID = tc.ID
			w.Type = "function"
			w.Function.Name = tc.Name
			w.Function.Arguments = tc.Arguments
			wm.ToolCalls = append(wm.ToolCalls, w)
		}
		out.Messages = append(out.Messages, wm)
	}
	for _, t := range req.Tools {
		var w wireTool
		w.Type = "function"
		w.Function.Name = t.Name
		w.Function.Description = t.Description
		w.Function.Parameters = t.Schema
		out.Tools = append(out.Tools, w)
	}
	return out
}
