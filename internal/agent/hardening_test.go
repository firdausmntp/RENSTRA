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
	"renstra/internal/config"
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

type countTool struct{ count int }

func (c *countTool) Name() string            { return "counter" }
func (c *countTool) Description() string     { return "counts calls" }
func (c *countTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (c *countTool) Danger() bool            { return false }
func (c *countTool) Execute(context.Context, json.RawMessage) (string, error) {
	c.count++
	return "ok", nil
}

func TestLoopDetectionStopsRepeatedToolCalls(t *testing.T) {
	s := newStore(t)
	ct := &countTool{}
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "counter", Arguments: `{"x":1}`}}}},
		{{ToolCalls: []provider.ToolCall{{ID: "c2", Name: "counter", Arguments: `{"x":1}`}}}},
		{{ToolCalls: []provider.ToolCall{{ID: "c3", Name: "counter", Arguments: `{"x":1}`}}}},
		{{Text: "berhenti", Done: true}},
	}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(ct), Store: s, Model: "m", MaxIters: 6, Cwd: t.TempDir(), Out: io.Discard}
	got, err := r.Run(context.Background(), 1, "ulang")
	if err != nil {
		t.Fatal(err)
	}
	if got != "berhenti" {
		t.Fatalf("got %q", got)
	}
	if ct.count != 2 {
		t.Fatalf("tool dipanggil %d kali, harus berhenti di ke-3 (max 2 eksekusi)", ct.count)
	}
	msgs, _ := s.Messages(context.Background(), 1)
	foundLoop := false
	for _, m := range msgs {
		if m.Role == provider.RoleTool && strings.Contains(m.Content, "loop terdeteksi") {
			foundLoop = true
		}
	}
	if !foundLoop {
		t.Fatal("pesan loop terdeteksi tidak tersimpan di hasil tool ke-3")
	}
}

func TestDailyLimitsBlockAndWarn(t *testing.T) {
	s := newStore(t)
	today := time.Now().Format("2006-01-02")
	if err := s.RecordUsage(context.Background(), today, 1000, 500, 1.25); err != nil {
		t.Fatal(err)
	}

	// Test warning footer when WarnDailyUSD is crossed
	fp := &fakeProvider{steps: [][]provider.Event{{{Text: "jawaban", Done: true}}}}
	r := &agent.Runner{
		Provider: fp,
		Tools:    tool.NewRegistry(),
		Store:    s,
		Model:    "m",
		MaxIters: 3,
		Cwd:      t.TempDir(),
		Out:      io.Discard,
		Limits:   config.Limits{WarnDailyUSD: 1.0},
	}
	ans, err := r.Run(context.Background(), 1, "halo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ans, "Peringatan biaya") {
		t.Fatalf("harus ada peringatan biaya, dapat %q", ans)
	}

	// Test hard cap blocks turn before provider is called
	r.Limits.MaxDailyUSD = 1.0
	if _, err := r.Run(context.Background(), 1, "halo lagi"); err == nil || !strings.Contains(err.Error(), "batas biaya harian") {
		t.Fatalf("harus ditolak karena batas biaya harian, dapat %v", err)
	}
}

func TestUnattendedContextDeniesDangerousTool(t *testing.T) {
	s := newStore(t)
	approverCalled := false
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "danger", Arguments: `{}`}}}},
		{{Text: "selesai", Done: true}},
	}}
	r := &agent.Runner{
		Provider: fp,
		Tools:    tool.NewRegistry(&dangerTool{}),
		Store:    s,
		Model:    "m",
		MaxIters: 4,
		Cwd:      t.TempDir(),
		Out:      io.Discard,
		Approve: func(context.Context, string, json.RawMessage) (bool, error) {
			approverCalled = true
			return true, nil
		},
	}
	ctx := agent.WithUnattended(context.Background())
	if _, err := r.Run(ctx, 1, "cron"); err != nil {
		t.Fatal(err)
	}
	if approverCalled {
		t.Fatal("approver interaktif tidak boleh dipanggil pada konteks unattended")
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if !strings.Contains(msgs[2].Content, "cron") {
		t.Fatalf("pesan penolakan harus menyebut cron/unattended, dapat %q", msgs[2].Content)
	}
}

var _ = filepath.Join
