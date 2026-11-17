package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeServer mensimulasikan server MCP lewat pipe dua arah.
type fakeServer struct {
	in  *bufio.Scanner
	out io.Writer
}

func newFakeServer(r io.Reader, w io.Writer) *fakeServer {
	return &fakeServer{in: bufio.NewScanner(r), out: w}
}

func (s *fakeServer) read() (rpcMessage, error) {
	if !s.in.Scan() {
		return rpcMessage{}, io.EOF
	}
	var msg rpcMessage
	err := json.Unmarshal(s.in.Bytes(), &msg)
	return msg, err
}

func (s *fakeServer) reply(id json.RawMessage, result any) error {
	resRaw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	msg := rpcMessage{JSONRPC: "2.0", ID: id, Result: resRaw}
	b, _ := json.Marshal(msg)
	b = append(b, '\n')
	_, err = s.out.Write(b)
	return err
}

func (s *fakeServer) replyError(id json.RawMessage, code int, message string) error {
	msg := rpcMessage{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
	b, _ := json.Marshal(msg)
	b = append(b, '\n')
	_, err := s.out.Write(b)
	return err
}

func setupTestPair() (*Client, *fakeServer, func()) {
	clientInR, clientInW := io.Pipe()
	clientOutR, clientOutW := io.Pipe()

	client := newClientWithStreams(clientInW, clientOutR, "dummy")
	server := newFakeServer(clientInR, clientOutW)

	cleanup := func() {
		client.Close()
		clientInR.Close()
		clientInW.Close()
		clientOutR.Close()
		clientOutW.Close()
	}
	return client, server, cleanup
}

func TestHandshakeAndToolsList(t *testing.T) {
	client, server, cleanup := setupTestPair()
	defer cleanup()

	serverDone := make(chan error, 1)
	go func() {
		// 1. initialize
		msg, err := server.read()
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "initialize" {
			serverDone <- fmt.Errorf("method %q want initialize", msg.Method)
			return
		}
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		json.Unmarshal(msg.Params, &p)
		if p.ProtocolVersion != protocolVersion || p.ClientInfo.Name != "renstra" {
			serverDone <- fmt.Errorf("params initialize: %+v", p)
			return
		}
		if err := server.reply(msg.ID, map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{}}); err != nil {
			serverDone <- err
			return
		}

		// 2. notifications/initialized
		notif, err := server.read()
		if err != nil {
			serverDone <- err
			return
		}
		if notif.Method != "notifications/initialized" {
			serverDone <- fmt.Errorf("notif method %q want notifications/initialized", notif.Method)
			return
		}

		// 3. tools/list
		listMsg, err := server.read()
		if err != nil {
			serverDone <- err
			return
		}
		if listMsg.Method != "tools/list" {
			serverDone <- fmt.Errorf("method %q want tools/list", listMsg.Method)
			return
		}
		err = server.reply(listMsg.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name":        "search",
					"description": "cari sesuatu",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"q": map[string]any{"type": "string"},
						},
					},
				},
			},
		})
		serverDone <- err
	}()

	if err := client.handshake(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}

	tools := client.Tools()
	if len(tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(tools))
	}
	tl := tools[0]
	if tl.Name() != "mcp__dummy__search" {
		t.Fatalf("name = %q want mcp__dummy__search", tl.Name())
	}
	if tl.Description() != "cari sesuatu" {
		t.Fatalf("description = %q", tl.Description())
	}
	if !tl.Danger() {
		t.Fatal("danger want true")
	}
	if !strings.Contains(string(tl.Schema()), `"properties"`) {
		t.Fatalf("schema = %s", tl.Schema())
	}
}

func TestToolsCallSuccess(t *testing.T) {
	client, server, cleanup := setupTestPair()
	defer cleanup()

	mTool := &mcpTool{
		client:      client,
		name:        "mcp__dummy__echo",
		toolName:    "echo",
		description: "echo",
		schema:      json.RawMessage(`{}`),
	}

	serverDone := make(chan error, 1)
	go func() {
		msg, err := server.read()
		if err != nil {
			serverDone <- err
			return
		}
		if msg.Method != "tools/call" {
			serverDone <- fmt.Errorf("method %q want tools/call", msg.Method)
			return
		}
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		json.Unmarshal(msg.Params, &p)
		if p.Name != "echo" {
			serverDone <- fmt.Errorf("tool name %q", p.Name)
			return
		}
		err = server.reply(msg.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "halo dari "},
				{"type": "text", "text": "mcp server"},
			},
		})
		serverDone <- err
	}()

	res, err := mTool.Execute(context.Background(), json.RawMessage(`{"text":"halo"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(res, "[untrusted]\n") || !strings.HasSuffix(res, "\n[/untrusted]") {
		t.Fatalf("kurang penanda untrusted: %q", res)
	}
	if !strings.Contains(res, "halo dari mcp server") {
		t.Fatalf("konten salah: %q", res)
	}
}

func TestToolsCallSurfacesError(t *testing.T) {
	client, server, cleanup := setupTestPair()
	defer cleanup()

	mTool := &mcpTool{
		client:   client,
		name:     "mcp__dummy__fail",
		toolName: "fail",
	}

	serverDone := make(chan error, 1)
	go func() {
		msg, err := server.read()
		if err != nil {
			serverDone <- err
			return
		}
		err = server.replyError(msg.ID, -32603, "internal server error")
		serverDone <- err
	}()

	_, err := mTool.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "MCP error -32603: internal server error") {
		t.Fatalf("pesan error salah: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestConnectAllCollectsErrors(t *testing.T) {
	cfgs := []ServerConfig{
		{Name: "bad-url", URL: "http://127.0.0.1:1"},
		{Name: "bad-cmd", Command: ""},
	}
	servers, errs := ConnectAll(context.Background(), cfgs)
	if len(servers) != 0 {
		t.Fatalf("servers count = %d, want 0", len(servers))
	}
	if len(errs) != 2 {
		t.Fatalf("errs count = %d, want 2", len(errs))
	}
}

func TestConnectHTTPTransportAndCall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("missing auth header")
		}
		raw, _ := io.ReadAll(r.Body)
		var req rpcMessage
		_ = json.Unmarshal(raw, &req)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Mcp-Session-Id", "sess-1")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"protocolVersion":"2024-11-05","capabilities":{}}`)})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			_ = json.NewEncoder(w).Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"tools":[{"name":"ping","description":"ping server","inputSchema":{"type":"object"}}]}`)})
		case "tools/call":
			_ = json.NewEncoder(w).Encode(rpcMessage{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"content":[{"type":"text","text":"pong"}]}`)})
		}
	}))
	defer srv.Close()

	c, err := Connect(context.Background(), ServerConfig{
		Name:    "remote",
		URL:     srv.URL,
		Headers: map[string]string{"Authorization": "Bearer tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := c.Tools()
	if len(tools) != 1 || tools[0].Name() != "mcp__remote__ping" {
		t.Fatalf("tools = %v", tools)
	}
	res, err := tools[0].Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res, "pong") {
		t.Fatalf("res = %q", res)
	}
}
