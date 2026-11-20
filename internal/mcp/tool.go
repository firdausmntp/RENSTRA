package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type mcpTool struct {
	mu          sync.RWMutex
	client      *Client
	name        string
	toolName    string
	description string
	schema      json.RawMessage
}

func (t *mcpTool) Name() string { return t.name }
func (t *mcpTool) Description() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.description
}
func (t *mcpTool) Schema() json.RawMessage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.schema
}
func (t *mcpTool) Danger() bool { return true }

func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	if t.client.Dead() {
		if err := t.client.Reconnect(ctx); err != nil {
			return "", err
		}
	}
	if len(args) == 0 || string(args) == "null" {
		args = json.RawMessage(`{}`)
	}
	raw, err := t.client.call(ctx, "tools/call", map[string]any{
		"name":      t.toolName,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}

	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("hasil tools/call tidak valid: %w", err)
	}

	var b strings.Builder
	for _, block := range res.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	text := b.String()
	if res.IsError {
		return "", fmt.Errorf("tool MCP %s gagal: %s", t.toolName, text)
	}
	return "[untrusted]\n" + text + "\n[/untrusted]", nil
}
