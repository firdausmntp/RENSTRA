package google

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

const defaultBaseURL = "https://generativelanguage.googleapis.com"

type Client struct {
	cfg    config.ProviderConfig
	apiKey string
	http   *http.Client
}

func New(cfg config.ProviderConfig, apiKey string) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
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

func Name() string { return "google" }

func (c *Client) Name() string { return "google" }

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiToolDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiToolDecl `json:"functionDeclarations"`
}

type geminiGenConfig struct {
	MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent   `json:"systemInstruction,omitempty"`
	Contents          []geminiContent  `json:"contents"`
	Tools             []geminiTool     `json:"tools,omitempty"`
	GenerationConfig  *geminiGenConfig `json:"generationConfig,omitempty"`
}

type geminiChunk struct {
	Candidates []struct {
		Content struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (c *Client) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return nil, err
	}

	path := c.cfg.Path
	if path == "" {
		path = "/v1beta/models/" + url.PathEscape(req.Model) + ":streamGenerateContent"
	}
	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + path
	if strings.Contains(endpoint, "?") {
		endpoint += "&alt=sse"
	} else {
		endpoint += "?alt=sse"
	}

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
				httpReq.Header.Set("x-goog-api-key", c.apiKey)
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
				send(ctx, ch, provider.Event{Err: fmt.Errorf("google: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))})
				return
			}

			c.consume(ctx, resp.Body, ch)
			resp.Body.Close()
			return
		}
	}()
	return ch, nil
}

func (c *Client) buildRequest(req provider.Request) geminiRequest {
	out := geminiRequest{Contents: []geminiContent{}}
	if req.MaxTokens > 0 {
		out.GenerationConfig = &geminiGenConfig{MaxOutputTokens: req.MaxTokens}
	}

	var sysParts []geminiPart
	callNames := map[string]string{}

	for _, m := range req.Messages {
		switch m.Role {
		case provider.RoleSystem:
			if m.Content != "" {
				sysParts = append(sysParts, geminiPart{Text: m.Content})
			}
		case provider.RoleUser:
			var parts []geminiPart
			for _, att := range m.Attachments {
				data, err := os.ReadFile(att.Path)
				if err != nil {
					continue
				}
				mime := att.MimeType
				if mime == "" {
					mime = "image/jpeg"
				}
				parts = append(parts, geminiPart{
					InlineData: &geminiInlineData{
						MimeType: mime,
						Data:     base64.StdEncoding.EncodeToString(data),
					},
				})
			}
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			if len(parts) > 0 {
				out.Contents = append(out.Contents, geminiContent{Role: "user", Parts: parts})
			}
		case provider.RoleAssistant:
			var parts []geminiPart
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				callNames[tc.ID] = tc.Name
				args := json.RawMessage("{}")
				if strings.TrimSpace(tc.Arguments) != "" && json.Valid([]byte(tc.Arguments)) {
					args = json.RawMessage(tc.Arguments)
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{Name: tc.Name, Args: args},
				})
			}
			if len(parts) > 0 {
				out.Contents = append(out.Contents, geminiContent{Role: "model", Parts: parts})
			}
		case provider.RoleTool:
			name := callNames[m.ToolCallID]
			if name == "" {
				name = "tool"
			}
			out.Contents = append(out.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     name,
						Response: map[string]any{"result": m.Content},
					},
				}},
			})
		}
	}
	if len(sysParts) > 0 {
		out.SystemInstruction = &geminiContent{Parts: sysParts}
	}
	if len(req.Tools) > 0 {
		decls := make([]geminiToolDecl, 0, len(req.Tools))
		for _, t := range req.Tools {
			decls = append(decls, geminiToolDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Schema,
			})
		}
		out.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}
	return out
}
