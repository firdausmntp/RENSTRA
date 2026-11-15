package provider

import (
	"context"
	"encoding/json"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ToolCall struct {
	ID        string
	Name      string
	Arguments string // raw JSON
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type ToolDef struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type Request struct {
	Model     string
	Messages  []Message
	Tools     []ToolDef
	MaxTokens int
}

// Event is one streaming update. Text is incremental; ToolCalls and Usage are
// only set on the final event before Done; Err ends the stream.
type Event struct {
	Text      string
	ToolCalls []ToolCall
	Usage     *Usage
	Err       error
	Done      bool
}

type Provider interface {
	Name() string
	Stream(ctx context.Context, req Request) (<-chan Event, error)
}
