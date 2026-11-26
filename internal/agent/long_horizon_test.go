package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"renstra/internal/provider"
	"renstra/internal/store"
	"renstra/internal/tool"
)

type captureSystemProv struct {
	sawSystem string
}

func (p *captureSystemProv) Name() string { return "cap" }
func (p *captureSystemProv) Stream(_ context.Context, req provider.Request) (<-chan provider.Event, error) {
	if len(req.Messages) > 0 {
		p.sawSystem = req.Messages[0].Content
	}
	ch := make(chan provider.Event, 2)
	ch <- provider.Event{Text: "oke"}
	ch <- provider.Event{Done: true}
	close(ch)
	return ch, nil
}

type deliverThenTimeoutProv struct {
	step int
}

func (p *deliverThenTimeoutProv) Name() string { return "slow" }
func (p *deliverThenTimeoutProv) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	p.step++
	ch := make(chan provider.Event, 2)
	if p.step == 1 {
		ch <- provider.Event{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "fake_deliver", Arguments: `{}`}}}
		ch <- provider.Event{Done: true}
		close(ch)
		return ch, nil
	}
	go func() {
		defer close(ch)
		<-ctx.Done()
		ch <- provider.Event{Err: ctx.Err()}
	}()
	return ch, nil
}

type fakeDeliverTool struct{}

func (f *fakeDeliverTool) Name() string            { return "fake_deliver" }
func (f *fakeDeliverTool) Description() string     { return "deliver" }
func (f *fakeDeliverTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *fakeDeliverTool) Danger() bool            { return false }
func (f *fakeDeliverTool) Execute(ctx context.Context, _ json.RawMessage) (string, error) {
	tool.MarkChatDelivered(ctx, tool.ChatIDFrom(ctx))
	return "sent", nil
}

func TestAutoRecallAndDeliveredTimeoutRecovery(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	if _, err := st.SaveMemoryFact(ctx, "global", "threads", "Gunakan GraphQL bukan REST delete media"); err != nil {
		t.Fatal(err)
	}

	capProv := &captureSystemProv{}
	r1 := &Runner{Provider: capProv, Tools: tool.NewRegistry(), Store: st, Model: "v4"}
	if _, err := r1.Run(ctx, 55, "bantu hapus komentar threads"); err != nil {
		t.Fatalf("r1.Run: %v", err)
	}
	if !strings.Contains(capProv.sawSystem, "Memori Relevan Otomatis") || !strings.Contains(capProv.sawSystem, "GraphQL") {
		t.Fatalf("expected auto-recalled memory in system prompt, got: %s", capProv.sawSystem)
	}

	reg := tool.NewRegistry(&fakeDeliverTool{})
	r2 := &Runner{
		Provider:    &deliverThenTimeoutProv{},
		Tools:       reg,
		Store:       st,
		Model:       "v4",
		TurnTimeout: 60 * time.Millisecond,
	}
	out, err := r2.Run(tool.WithChatID(context.Background(), 777), 777, "kirim laporan")
	if err != nil {
		t.Fatalf("expected graceful recovery after send_telegram delivery, got err=%v", err)
	}
	if out != "" {
		t.Fatalf("expected empty duplicate reply after direct delivery, got %q", out)
	}
}
