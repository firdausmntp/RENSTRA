package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"renstra/internal/tool"
)

const protocolVersion = "2024-11-05"

type ServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     []string
	URL     string
	Headers map[string]string
}

type Server struct {
	Name  string
	Tools []tool.Tool
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *rpcError) Error() string {
	if e.Data != "" {
		return fmt.Sprintf("MCP error %d: %s (%s)", e.Code, e.Message, e.Data)
	}
	return fmt.Sprintf("MCP error %d: %s", e.Code, e.Message)
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type Client struct {
	name string
	cmd  *exec.Cmd
	in   io.Writer
	out  io.Reader

	writeMu sync.Mutex
	pendMu  sync.Mutex
	pending map[int64]chan rpcMessage
	nextID  int64
	closed  bool

	tools []tool.Tool
}

// newClientWithStreams builds a client over arbitrary streams; the transport is
// newline-delimited JSON-RPC, which is what MCP stdio uses.
func newClientWithStreams(in io.Writer, out io.Reader, name string) *Client {
	c := &Client{name: name, in: in, out: out, pending: map[int64]chan rpcMessage{}}
	go c.readLoop()
	return c
}

func Connect(ctx context.Context, cfg ServerConfig) (*Client, error) {
	if cfg.URL != "" {
		return nil, fmt.Errorf("MCP server %q: transport http belum didukung, pakai command", cfg.Name)
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("MCP server %q: command kosong", cfg.Name)
	}

	cmd := exec.Command(cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = cfg.Env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("MCP server %q: %w", cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("MCP server %q: %w", cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("MCP server %q gagal dijalankan: %w", cfg.Name, err)
	}

	c := newClientWithStreams(stdin, stdout, cfg.Name)
	c.cmd = cmd
	if err := c.handshake(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("MCP server %q: %w", cfg.Name, err)
	}
	return c, nil
}

// ConnectAll connects every server, collecting per-server errors so one broken
// config does not hide the rest.
func ConnectAll(ctx context.Context, cfgs []ServerConfig) ([]Server, []error) {
	var servers []Server
	var errs []error
	for _, cfg := range cfgs {
		c, err := Connect(ctx, cfg)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		servers = append(servers, Server{Name: cfg.Name, Tools: c.Tools()})
	}
	return servers, errs
}

func (c *Client) handshake(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "renstra", "version": "1"},
	}
	if _, err := c.call(ctx, "initialize", params); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if err := c.notify("notifications/initialized", map[string]any{}); err != nil {
		return fmt.Errorf("notifications/initialized: %w", err)
	}

	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	var res struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("tools/list tidak valid: %w", err)
	}

	c.tools = make([]tool.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema := t.InputSchema
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		c.tools = append(c.tools, &mcpTool{
			client:      c,
			name:        "mcp__" + c.name + "__" + t.Name,
			toolName:    t.Name,
			description: t.Description,
			schema:      schema,
		})
	}
	return nil
}

func (c *Client) Tools() []tool.Tool { return c.tools }

func (c *Client) Close() error {
	c.writeMu.Lock()
	if c.closed {
		c.writeMu.Unlock()
		return nil
	}
	c.closed = true
	c.writeMu.Unlock()

	if cl, ok := c.in.(io.Closer); ok {
		cl.Close()
	}
	if c.cmd != nil {
		return c.cmd.Wait()
	}
	return nil
}

func (c *Client) notify(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return c.send(rpcMessage{JSONRPC: "2.0", Method: method, Params: raw})
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	c.pendMu.Lock()
	c.nextID++
	id := c.nextID
	ch := make(chan rpcMessage, 1)
	c.pending[id] = ch
	c.pendMu.Unlock()

	defer func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}()

	if err := c.send(rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprintf("%d", id)), Method: method, Params: rawParams}); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case msg, ok := <-ch:
		if !ok {
			return nil, errors.New("koneksi MCP ditutup")
		}
		if msg.Error != nil {
			return nil, msg.Error
		}
		return msg.Result, nil
	}
}

func (c *Client) send(msg rpcMessage) error {
	line, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	line = append(line, '\n')

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed {
		return errors.New("koneksi MCP sudah ditutup")
	}
	if _, err := c.in.Write(line); err != nil {
		return fmt.Errorf("menulis ke MCP: %w", err)
	}
	return nil
}

func (c *Client) readLoop() {
	sc := bufio.NewScanner(c.out)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		if len(msg.ID) == 0 || string(msg.ID) == "null" {
			continue // notifikasi/permintaan dari server tidak diurus v1
		}
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			continue
		}
		c.pendMu.Lock()
		ch, ok := c.pending[id]
		c.pendMu.Unlock()
		if ok {
			ch <- msg
		}
	}

	// Stream mati: bangunkan semua penunggu.
	c.pendMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendMu.Unlock()
}

type mcpTool struct {
	client      *Client
	name        string
	toolName    string
	description string
	schema      json.RawMessage
}

func (t *mcpTool) Name() string            { return t.name }
func (t *mcpTool) Description() string     { return t.description }
func (t *mcpTool) Schema() json.RawMessage { return t.schema }
func (t *mcpTool) Danger() bool            { return true }

func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
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
