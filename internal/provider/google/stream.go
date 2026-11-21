package google

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"renstra/internal/provider"
)

func (c *Client) consume(ctx context.Context, r io.Reader, ch chan<- provider.Event) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var calls []provider.ToolCall
	var usage *provider.Usage
	callIdx := 0
	var pendingData strings.Builder

	processPayload := func(raw string) bool {
		var chunk geminiChunk
		if err := json.Unmarshal([]byte(raw), &chunk); err != nil {
			send(ctx, ch, provider.Event{Err: fmt.Errorf("google: chunk tidak valid: %w", err)})
			return false
		}
		if chunk.Error != nil {
			send(ctx, ch, provider.Event{Err: fmt.Errorf("google: %s", chunk.Error.Message)})
			return false
		}
		if chunk.UsageMetadata != nil {
			usage = &provider.Usage{
				InputTokens:  chunk.UsageMetadata.PromptTokenCount,
				OutputTokens: chunk.UsageMetadata.CandidatesTokenCount,
			}
		}
		for _, cand := range chunk.Candidates {
			for _, part := range cand.Content.Parts {
				if part.Text != "" {
					if !send(ctx, ch, provider.Event{Text: part.Text}) {
						return false
					}
				}
				if part.FunctionCall != nil {
					callIdx++
					args := "{}"
					if len(part.FunctionCall.Args) > 0 {
						args = string(part.FunctionCall.Args)
					}
					calls = append(calls, provider.ToolCall{
						ID:        fmt.Sprintf("gemini_%d", callIdx),
						Name:      part.FunctionCall.Name,
						Arguments: args,
					})
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
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
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
		}
	}
	send(ctx, ch, provider.Event{ToolCalls: calls, Usage: usage, Done: true})
}

func send(ctx context.Context, ch chan<- provider.Event, ev provider.Event) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
