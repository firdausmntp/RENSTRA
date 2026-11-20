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
	"strings"
)

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
