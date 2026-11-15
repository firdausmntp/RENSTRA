package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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

func New(cfg config.ProviderConfig, apiKey string) *Client {
	timeout := time.Duration(cfg.Timeout)
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if cfg.Proxy != "" {
		if u, err := url.Parse(cfg.Proxy); err == nil {
			transport.Proxy = http.ProxyURL(u)
		}
	}
	return &Client{
		cfg:    cfg,
		apiKey: apiKey,
		http:   &http.Client{Timeout: timeout, Transport: transport},
	}
}

func (c *Client) Name() string { return "openai" }

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
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
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []wireTool    `json:"tools,omitempty"`
	Stream   bool          `json:"stream"`
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
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(c.cfg.BaseURL, "/") + c.cfg.Path
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	for k, v := range c.cfg.Headers {
		httpReq.Header.Set(k, v)
	}

	ch := make(chan provider.Event)
	go func() {
		defer close(ch)
		resp, err := c.http.Do(httpReq)
		if err != nil {
			ch <- provider.Event{Err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			ch <- provider.Event{Err: fmt.Errorf("provider %s: HTTP %d: %s", c.cfg.BaseURL, resp.StatusCode, strings.TrimSpace(string(raw)))}
			return
		}

		c.consume(resp.Body, ch)
	}()
	return ch, nil
}

func (c *Client) buildRequest(req provider.Request) wireRequest {
	out := wireRequest{Model: req.Model, Stream: true}
	for _, m := range req.Messages {
		wm := wireMessage{Role: string(m.Role), Content: m.Content, ToolCallID: m.ToolCallID}
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

// consume reads the SSE stream. Partial tool-call arguments are accumulated
// until the stream ends; nothing is emitted half-parsed.
func (c *Client) consume(r io.Reader, ch chan<- provider.Event) {
	scanner := newSSEScanner(r)
	partial := map[int]*provider.ToolCall{}
	var usage *provider.Usage

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			break
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			ch <- provider.Event{Err: fmt.Errorf("chunk tidak valid: %w", err)}
			return
		}
		if chunk.Usage != nil {
			usage = &provider.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				ch <- provider.Event{Text: choice.Delta.Content}
			}
			for _, tc := range choice.Delta.ToolCalls {
				p, ok := partial[tc.Index]
				if !ok {
					p = &provider.ToolCall{}
					partial[tc.Index] = p
				}
				if tc.ID != "" {
					p.ID = tc.ID
				}
				if tc.Function.Name != "" {
					p.Name = tc.Function.Name
				}
				p.Arguments += tc.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		ch <- provider.Event{Err: err}
		return
	}

	calls := make([]provider.ToolCall, 0, len(partial))
	indexes := make([]int, 0, len(partial))
	for i := range partial {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		calls = append(calls, *partial[i])
	}
	ch <- provider.Event{ToolCalls: calls, Usage: usage, Done: true}
}

// newSSEScanner tolerates long tool-call argument lines.
func newSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}
