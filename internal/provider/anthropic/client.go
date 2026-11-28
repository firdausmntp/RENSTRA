package anthropic

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

const defaultPath = "/v1/messages"

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
	transport.ResponseHeaderTimeout = timeout
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
		http:   &http.Client{Transport: transport},
	}, nil
}

func (c *Client) Name() string { return "anthropic" }

type wireImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type wireBlock struct {
	Type      string           `json:"type"`
	Text      string           `json:"text,omitempty"`
	ID        string           `json:"id,omitempty"`
	Name      string           `json:"name,omitempty"`
	Input     json.RawMessage  `json:"input,omitempty"`
	ToolUseID string           `json:"tool_use_id,omitempty"`
	Content   string           `json:"content,omitempty"`
	Source    *wireImageSource `json:"source,omitempty"`
}

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

type wireTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type wireRequest struct {
	Model     string        `json:"model"`
	MaxTokens int           `json:"max_tokens"`
	System    string        `json:"system,omitempty"`
	Messages  []wireMessage `json:"messages"`
	Tools     []wireTool    `json:"tools,omitempty"`
	Stream    bool          `json:"stream"`
}

type streamEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message *struct {
		Usage *streamUsage `json:"usage"`
	} `json:"message"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Usage *streamUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type streamUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, err
	}

	path := c.cfg.Path
	if path == "" {
		path = defaultPath
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + path

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
			httpReq.Header.Set("x-api-key", c.apiKey)
			httpReq.Header.Set("anthropic-version", "2023-06-01")
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
				send(ctx, ch, provider.Event{Err: fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))})
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
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}
	out := wireRequest{Model: req.Model, MaxTokens: maxTokens, Stream: true, Messages: []wireMessage{}}

	var system []string
	for _, m := range req.Messages {
		if m.Role == provider.RoleSystem {
			if m.Content != "" {
				system = append(system, m.Content)
			}
			continue
		}
		if m.Role == provider.RoleTool {
			blocks := []wireBlock{{Type: "tool_result", ToolUseID: m.ToolCallID, Content: m.Content}}
			out.Messages = appendMessage(out.Messages, "user", blocks)
			continue
		}

		role := "user"
		if m.Role == provider.RoleAssistant {
			role = "assistant"
		}
		var blocks []wireBlock
		for _, att := range m.Attachments {
			data, err := os.ReadFile(att.Path)
			if err != nil {
				continue
			}
			mime := att.MimeType
			if mime == "" {
				mime = "image/jpeg"
			}
			blocks = append(blocks, wireBlock{
				Type: "image",
				Source: &wireImageSource{
					Type:      "base64",
					MediaType: mime,
					Data:      base64.StdEncoding.EncodeToString(data),
				},
			})
		}
		if m.Content != "" {
			blocks = append(blocks, wireBlock{Type: "text", Text: m.Content})
		}
		for _, tc := range m.ToolCalls {
			blocks = append(blocks, wireBlock{Type: "tool_use", ID: tc.ID, Name: tc.Name, Input: rawObject(tc.Arguments)})
		}
		if len(blocks) == 0 {
			continue
		}
		out.Messages = appendMessage(out.Messages, role, blocks)
	}
	out.System = strings.Join(system, "\n\n")

	for _, t := range req.Tools {
		out.Tools = append(out.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema})
	}
	return out
}

// appendMessage merges blocks into the previous message when the role repeats:
// the Messages API rejects two messages with the same role in a row.
func appendMessage(msgs []wireMessage, role string, blocks []wireBlock) []wireMessage {
	if n := len(msgs); n > 0 && msgs[n-1].Role == role {
		msgs[n-1].Content = append(msgs[n-1].Content, blocks...)
		return msgs
	}
	return append(msgs, wireMessage{Role: role, Content: blocks})
}

// rawObject keeps tool arguments as a JSON object; an empty or invalid
// argument string becomes {} so the request stays valid.
func rawObject(args string) json.RawMessage {
	if strings.TrimSpace(args) == "" || !json.Valid([]byte(args)) {
		return json.RawMessage("{}")
	}
	return json.RawMessage(args)
}
