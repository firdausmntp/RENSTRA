package openai

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
	var usage *provider.Usage
	sawDone := false
	finished := false
	sawData := false
	var pendingData strings.Builder
	var nonSSE strings.Builder

	processPayload := func(raw string) bool {
		var chunk streamChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			send(ctx, ch, provider.Event{Err: fmt.Errorf("chunk tidak valid: %w", err)})
			return false
		}
		if chunk.Error != nil {
			send(ctx, ch, provider.Event{Err: fmt.Errorf("provider error: %s", chunk.Error.Message)})
			return false
		}
		if chunk.Usage != nil {
			usage = &provider.Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finished = true
			}
			if choice.Delta.Content != "" {
				if !send(ctx, ch, provider.Event{Text: choice.Delta.Content}) {
					return false
				}
			}
			for _, tc := range choice.Delta.ToolCalls {
				p, ok := partial[tc.Index]
				if !ok {
					p = &provider.ToolCall{}
					partial[tc.Index] = p
				}
				if tc.ID != "" {
					p.ID = tc.ID
				}
				if tc.Function.Name != "" {
					p.Name = tc.Function.Name
				}
				p.Arguments += tc.Function.Arguments
			}
			if choice.Message != nil {
				finished = true
				if choice.Message.Content != "" {
					if !send(ctx, ch, provider.Event{Text: choice.Message.Content}) {
						return false
					}
				}
				for idx, tc := range choice.Message.ToolCalls {
					partial[idx] = &provider.ToolCall{
						ID:        tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					}
				}
			}
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
			if !sawData && !strings.HasPrefix(line, ":") && !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "id:") && !strings.HasPrefix(line, "retry:") {
				nonSSE.WriteString(line)
			}
			continue
		}
		sawData = true
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if pendingData.Len() == 0 && (strings.EqualFold(payload, "ping") || strings.EqualFold(payload, "keepalive")) {
			continue
		}
		if payload == "[DONE]" {
			if pendingData.Len() > 0 && json.Valid([]byte(pendingData.String())) {
				raw := pendingData.String()
				pendingData.Reset()
				if !processPayload(raw) {
					return
				}
			}
			pendingData.Reset()
			sawDone = true
			break
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
		} else if !finished && !sawDone {
			send(ctx, ch, provider.Event{Err: errors.New("stream terputus sebelum JSON chunk lengkap")})
			return
		}
	}
	if !sawData && nonSSE.Len() > 0 {
		if !processPayload(nonSSE.String()) {
			return
		}
	}
	if !sawDone && !finished {
		send(ctx, ch, provider.Event{Err: errors.New("stream terputus tanpa [DONE]")})
		return
	}

	calls := make([]provider.ToolCall, 0, len(partial))
	indexes := make([]int, 0, len(partial))
	for i := range partial {
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	for _, i := range indexes {
		calls = append(calls, *partial[i])
	}
	send(ctx, ch, provider.Event{ToolCalls: calls, Usage: usage, Done: true})
}

func newSSEScanner(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	return s
}
