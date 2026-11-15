package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/provider"
	"renstra/internal/tool"
)

type panickyTool struct{}

func (p *panickyTool) Name() string            { return "panicky" }
func (p *panickyTool) Description() string     { return "panics" }
func (p *panickyTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (p *panickyTool) Danger() bool            { return false }
func (p *panickyTool) Execute(context.Context, json.RawMessage) (string, error) {
	panic("boom")
}

type dangerTool struct{}

func (d *dangerTool) Name() string            { return "danger" }
func (d *dangerTool) Description() string     { return "dangerous" }
func (d *dangerTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (d *dangerTool) Danger() bool            { return true }
func (d *dangerTool) Execute(context.Context, json.RawMessage) (string, error) {
	return "dijalankan", nil
}

// streamThenCancel sends text, then blocks until ctx is cancelled — the shape
// of a real provider stream when the user hits Ctrl+C.
type streamThenCancel struct {
	text    string
	started chan struct{}
	once    sync.Once
}

func (s *streamThenCancel) Name() string { return "slow" }
func (s *streamThenCancel) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	go func() {
		defer close(ch)
		ch <- provider.Event{Text: s.text}
		s.once.Do(func() { close(s.started) })
		<-ctx.Done()
	}()
	return ch, nil
}

func TestToolPanicBecomesErrorResult(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "panicky", Arguments: `{}`}}}},
		{{Text: "lanjut", Done: true}},
	}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(&panickyTool{}), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	got, err := r.Run(context.Background(), 1, "x")
	if err != nil {
		t.Fatalf("panic membunuh turn: %v", err)
	}
	if got != "lanjut" {
		t.Fatalf("got %q", got)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if !strings.Contains(msgs[2].Content, "panic") {
		t.Fatalf("hasil tool = %q", msgs[2].Content)
	}
}

func TestDangerousToolDeniedWhenNoApprover(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "danger", Arguments: `{}`}}}},
		{{Text: "ok", Done: true}},
	}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(&dangerTool{}), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	if _, err := r.Run(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if strings.Contains(msgs[2].Content, "dijalankan") {
		t.Fatalf("tool berbahaya jalan tanpa approver: %q", msgs[2].Content)
	}
	if !strings.Contains(msgs[2].Content, "ditolak") {
		t.Fatalf("hasil tool = %q", msgs[2].Content)
	}
}

func TestCancelMidStreamSavesPartialText(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	sp := &streamThenCancel{text: "separuh jawaban", started: make(chan struct{})}
	r := &agent.Runner{Provider: sp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}

	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, 1, "x")
		done <- err
	}()
	<-sp.started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run tidak berhenti setelah cancel")
	}

	msgs, _ := s.Messages(context.Background(), 1)
	if len(msgs) != 2 {
		t.Fatalf("pesan = %d, want 2 (user + assistant parsial)", len(msgs))
	}
	if msgs[1].Role != provider.RoleAssistant || msgs[1].Content != "separuh jawaban" {
		t.Fatalf("got %+v", msgs[1])
	}
}

func TestCancelDuringToolKeepsCallResultPairing(t *testing.T) {
	s := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{
			{ID: "c1", Name: "danger", Arguments: `{}`},
			{ID: "c2", Name: "danger", Arguments: `{}`},
		}}},
		{{Text: "selesai", Done: true}},
	}}
	approve := func(context.Context, string, json.RawMessage) (bool, error) {
		cancel() // cancels the turn while the first tool is being approved
		return true, nil
	}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(&dangerTool{}), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard, Approve: approve}
	if _, err := r.Run(ctx, 1, "x"); err == nil {
		t.Fatal("want error setelah cancel")
	}

	msgs, _ := s.Messages(context.Background(), 1)
	calls := 0
	results := 0
	for _, m := range msgs {
		calls += len(m.ToolCalls)
		if m.Role == provider.RoleTool {
			results++
		}
	}
	if calls != results {
		t.Fatalf("tool_calls=%d hasil=%d — riwayat tidak berpasangan: %+v", calls, results, msgs)
	}
}

func TestStoreErrorIsNotSwallowed(t *testing.T) {
	s := newStore(t)
	s.Close() // every write now fails
	fp := &fakeProvider{steps: [][]provider.Event{{{Text: "hai", Done: true}}}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	if _, err := r.Run(context.Background(), 1, "x"); err == nil {
		t.Fatal("want error saat store gagal")
	}
}

var _ = filepath.Join
