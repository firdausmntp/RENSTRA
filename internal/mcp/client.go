package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

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
	Name   string
	Client *Client
	Tools  []tool.Tool
}

type Client struct {
	name       string
	cfg        ServerConfig
	cmd        *exec.Cmd
	in         io.Writer
	out        io.Reader
	httpClient *http.Client
	sessionID  string

	reconnMu sync.Mutex
	writeMu  sync.Mutex
	pendMu   sync.Mutex
	pending  map[int64]chan rpcMessage
	nextID   int64
	closed   bool
	dead     bool

	tools []tool.Tool
}

// Dead reports whether the transport has ended, so a caller can reconnect
// instead of handing every later call to a closed pipe.
func (c *Client) Dead() bool {
	c.pendMu.Lock()
	defer c.pendMu.Unlock()
	return c.dead
}

// newClientWithStreams builds a client over arbitrary streams; the transport is
// newline-delimited JSON-RPC, which is what MCP stdio uses.
func newClientWithStreams(in io.Writer, out io.Reader, name string) *Client {
	c := &Client{name: name, in: in, out: out, pending: map[int64]chan rpcMessage{}}
	go c.readLoop()
	return c
}

func Connect(ctx context.Context, cfg ServerConfig) (*Client, error) {
	if strings.TrimSpace(cfg.URL) != "" {
		c := &Client{
			name:       cfg.Name,
			cfg:        cfg,
			httpClient: &http.Client{Timeout: 60 * time.Second},
			pending:    map[int64]chan rpcMessage{},
		}
		if err := c.handshake(ctx); err != nil {
			return nil, fmt.Errorf("MCP server %q: %w", cfg.Name, err)
		}
		return c, nil
	}
	if strings.TrimSpace(cfg.Command) == "" {
		return nil, fmt.Errorf("MCP server %q: command dan url kosong", cfg.Name)
	}

	c := &Client{name: cfg.Name, cfg: cfg, pending: map[int64]chan rpcMessage{}}
	if err := c.startStdio(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) startStdio(ctx context.Context) error {
	cmd := exec.Command(c.cfg.Command, c.cfg.Args...)
	cmd.Env = mergeEnv(c.cfg.Env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("MCP server %q: %w", c.cfg.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("MCP server %q: %w", c.cfg.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("MCP server %q gagal dijalankan: %w", c.cfg.Name, err)
	}

	c.writeMu.Lock()
	c.cmd = cmd
	c.in = stdin
	c.out = stdout
	c.closed = false
	c.writeMu.Unlock()

	c.pendMu.Lock()
	c.dead = false
	c.pending = map[int64]chan rpcMessage{}
	c.pendMu.Unlock()

	go c.readLoop()

	if err := c.handshake(ctx); err != nil {
		c.Close()
		return fmt.Errorf("MCP server %q: %w", c.cfg.Name, err)
	}
	return nil
}

// Reconnect restarts a dead stdio MCP server process (or refreshes HTTP handshake)
// and re-runs handshake to update tool metadata.
func (c *Client) Reconnect(ctx context.Context) error {
	c.reconnMu.Lock()
	defer c.reconnMu.Unlock()

	if !c.Dead() {
		return nil
	}
	if strings.TrimSpace(c.cfg.URL) != "" {
		c.pendMu.Lock()
		c.dead = false
		c.pendMu.Unlock()
		return c.handshake(ctx)
	}
	if strings.TrimSpace(c.cfg.Command) == "" {
		return fmt.Errorf("server MCP %q telah mati dan tidak bisa di-restart", c.name)
	}
	_ = c.Close()
	return c.startStdio(ctx)
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
		servers = append(servers, Server{Name: cfg.Name, Client: c, Tools: c.Tools()})
	}
	return servers, errs
}

// mergeEnv gives the MCP process a working environment (PATH, HOME, etc.)
// with user overrides on top.
func mergeEnv(custom []string) []string {
	base := os.Environ()
	if len(custom) == 0 {
		return base
	}
	out := append([]string(nil), base...)
	return append(out, custom...)
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

	existing := make(map[string]*mcpTool, len(c.tools))
	for _, old := range c.tools {
		if mt, ok := old.(*mcpTool); ok {
			existing[mt.name] = mt
		}
	}

	next := make([]tool.Tool, 0, len(res.Tools))
	for _, t := range res.Tools {
		schema := t.InputSchema
		if len(schema) == 0 || string(schema) == "null" {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		fullName := "mcp__" + c.name + "__" + t.Name
		if mt, ok := existing[fullName]; ok {
			mt.mu.Lock()
			mt.description = t.Description
			mt.schema = schema
			mt.mu.Unlock()
			next = append(next, mt)
			continue
		}
		next = append(next, &mcpTool{
			client:      c,
			name:        fullName,
			toolName:    t.Name,
			description: t.Description,
			schema:      schema,
		})
	}
	c.tools = next
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
