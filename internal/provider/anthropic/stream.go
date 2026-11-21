package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"renstra/internal/provider"
)

func (c *Client) consume(ctx context.Context, r io.Reader, ch chan<- provider.Event) {
	scanner := newSSEScanner(r)
	partial := map[int]*provider.ToolCall{}
	args := map[int]*strings.Builder{}
	var usage *provider.Usage
	sawStop := false
	var pendingData strings.Builder

	processPayload := func(raw string) bool {
		var ev streamEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			send(ctx, ch, provider.Event{Err: fmt.Errorf("event tidak valid: %w", err)})
			return false
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
				return true
			}
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" {
					if !send(ctx, ch, provider.Event{Text: ev.Delta.Text}) {
						return false
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
			return false
		}
		return true
	}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			if pendingData.Len() > 0 && json.Valid([]byte(pendingData.String())) {
				raw := pendingData.String()
				pendingData.Reset()
				if !processPayload(raw) {
					return
				}
			}
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if pendingData.Len() > 0 {
			pendingData.WriteByte('\n')
		}
		pendingData.WriteString(payload)
		if json.Valid([]byte(pendingData.String())) {
			raw := pendingData.String()
			pendingData.Reset()
			if !processPayload(raw) {
				return
			}
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
	if pendingData.Len() > 0 {
		raw := pendingData.String()
		pendingData.Reset()
		if json.Valid([]byte(raw)) {
			if !processPayload(raw) {
				return
			}
		} else if !sawStop {
			send(ctx, ch, provider.Event{Err: errors.New("stream terputus sebelum JSON event lengkap")})
			return
		}
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
