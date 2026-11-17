package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

// Reconnect restarts a dead stdio MCP server process and re-runs handshake.
func (c *Client) Reconnect(ctx context.Context) error {
	c.reconnMu.Lock()
	defer c.reconnMu.Unlock()

	if !c.Dead() {
		return nil
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
	msg := rpcMessage{JSONRPC: "2.0", Method: method, Params: raw}
	if c.httpClient != nil {
		_, err := c.postHTTP(context.Background(), msg)
		return err
	}
	return c.send(msg)
}

func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	rawParams, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	c.pendMu.Lock()
	c.nextID++
	id := c.nextID
	if c.httpClient != nil {
		c.pendMu.Unlock()
		msg := rpcMessage{JSONRPC: "2.0", ID: json.RawMessage(fmt.Sprintf("%d", id)), Method: method, Params: rawParams}
		respMsg, err := c.postHTTP(ctx, msg)
		if err != nil {
			return nil, err
		}
		if respMsg.Error != nil {
			return nil, respMsg.Error
		}
		return respMsg.Result, nil
	}
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

func (c *Client) postHTTP(ctx context.Context, msg rpcMessage) (rpcMessage, error) {
	body, err := json.Marshal(msg)
	if err != nil {
		return rpcMessage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return rpcMessage{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range c.cfg.Headers {
		req.Header.Set(k, v)
	}
	c.writeMu.Lock()
	sid := c.sessionID
	c.writeMu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return rpcMessage{}, err
	}
	defer resp.Body.Close()

	if newSID := resp.Header.Get("Mcp-Session-Id"); newSID != "" {
		c.writeMu.Lock()
		c.sessionID = newSID
		c.writeMu.Unlock()
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return rpcMessage{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return rpcMessage{}, fmt.Errorf("MCP HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return rpcMessage{}, nil
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "text/event-stream") {
		sc := bufio.NewScanner(bytes.NewReader(raw))
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			var out rpcMessage
			if err := json.Unmarshal([]byte(payload), &out); err == nil && len(out.ID) > 0 {
				return out, nil
			}
		}
		return rpcMessage{}, errors.New("MCP HTTP SSE tidak mengembalikan respons JSON-RPC")
	}

	var out rpcMessage
	if err := json.Unmarshal(raw, &out); err != nil {
		return rpcMessage{}, fmt.Errorf("MCP HTTP respons tidak valid: %w", err)
	}
	return out, nil
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

	// Stream mati: bangunkan semua penunggu dan tandai client mati.
	c.pendMu.Lock()
	c.dead = true
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
