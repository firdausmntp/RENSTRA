package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func (c *Client) Name() string { return "anthropic" }

type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", c.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	for k, v := range c.cfg.Headers {
		httpReq.Header.Set(k, v)
	}

	ch := make(chan provider.Event)
	go func() {
		defer close(ch)
		resp, err := c.http.Do(httpReq)
		if err != nil {
			send(ctx, ch, provider.Event{Err: err})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			send(ctx, ch, provider.Event{Err: fmt.Errorf("provider %s: HTTP %d: %s", c.cfg.BaseURL, resp.StatusCode, strings.TrimSpace(string(raw)))})
			return
		}

		c.consume(ctx, resp.Body, ch)
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

// consume reads the SSE stream, assembling tool_use input from the
// input_json_delta fragments. A stream that ends without message_stop is an
// error: half a tool call must never run.
func (c *Client) consume(ctx context.Context, r io.Reader, ch chan<- provider.Event) {
	scanner := newSSEScanner(r)
	partial := map[int]*provider.ToolCall{}
	args := map[int]*strings.Builder{}
	var usage *provider.Usage
	sawStop := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		var ev streamEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			send(ctx, ch, provider.Event{Err: fmt.Errorf("event tidak valid: %w", err)})
			return
		}

		switch ev.Type {
		case "message_start":
			if ev.Message != nil && ev.Message.Usage != nil {
				usage = &provider.Usage{InputTokens: ev.Message.Usage.InputTokens, OutputTokens: ev.Message.Usage.OutputTokens}
			}
		case "content_block_start":
			if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
				partial[ev.Index] = &provider.ToolCall{ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name}
				args[ev.Index] = &strings.Builder{}
			}
		case "content_block_delta":
			if ev.Delta == nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					if !send(ctx, ch, provider.Event{Text: ev.Delta.Text}) {
						return
					}
				}
			case "input_json_delta":
				if b, ok := args[ev.Index]; ok {
					b.WriteString(ev.Delta.PartialJSON)
				}
			}
		case "message_delta":
			if ev.Usage != nil {
				if usage == nil {
					usage = &provider.Usage{}
				}
				if ev.Usage.InputTokens != 0 {
					usage.InputTokens = ev.Usage.InputTokens
				}
				usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "message_stop":
			sawStop = true
		case "error":
			msg := "provider error"
			if ev.Error != nil && ev.Error.Message != "" {
				msg = ev.Error.Message
			}
			send(ctx, ch, provider.Event{Err: errors.New(msg)})
			return
		}
		if sawStop {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		if ctx.Err() != nil {
			return
		}
		send(ctx, ch, provider.Event{Err: err})
		return
	}
	if !sawStop {
		send(ctx, ch, provider.Event{Err: errors.New("stream terputus tanpa message_stop")})
		return
	}

	indexes := make([]int, 0, len(partial))
	for i := range partial {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	calls := make([]provider.ToolCall, 0, len(indexes))
	for _, i := range indexes {
		tc := *partial[i]
		tc.Arguments = args[i].String()
		calls = append(calls, tc)
	}
	send(ctx, ch, provider.Event{ToolCalls: calls, Usage: usage, Done: true})
}

func newSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}
