package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/store"
	"renstra/internal/tool"
)

func TestCanCompactAndPrepareRequestHistory(t *testing.T) {
	// Single-turn long history: 1 user + 8 assistant/tool pairs with large outputs.
	bigOut := "[untrusted]\n" + strings.Repeat("log line data ", 80) + "\n[/untrusted]"
	history := []provider.Message{
		{Role: provider.RoleUser, Content: "kerjakan tugas panjang"},
	}
	for i := 0; i < 8; i++ {
		id := "call_" + string(rune('a'+i))
		history = append(history,
			provider.Message{Role: provider.RoleAssistant, Content: "cek langkah", ToolCalls: []provider.ToolCall{{ID: id, Name: "terminal", Arguments: `{"cmd":"ls"}`}}},
			provider.Message{Role: provider.RoleTool, Content: bigOut, ToolCallID: id},
		)
	}

	if CanCompact(history, keepTurns) {
		t.Fatalf("expected CanCompact=false for single-turn history with only 1 user message")
	}

	prepared, masked, folded := PrepareRequestHistory("sys", nil, history, 1200, 0.7)
	if masked == 0 {
		t.Fatalf("expected older verbose tool results to be masked")
	}
	if folded == 0 {
		t.Fatalf("expected single-turn overflow to fold older assistant+tool blocks")
	}
	if prepared[0].Role != provider.RoleUser {
		t.Fatalf("expected first message to stay RoleUser, got %s", prepared[0].Role)
	}
	// Verify every RoleTool in prepared has a matching preceding assistant tool call.
	seenCalls := map[string]bool{}
	for _, m := range prepared {
		for _, tc := range m.ToolCalls {
			seenCalls[tc.ID] = true
		}
		if m.Role == provider.RoleTool && !seenCalls[m.ToolCallID] {
			t.Fatalf("orphaned tool message after folding: %s", m.ToolCallID)
		}
	}
	// Original history slice must remain untouched.
	if history[2].Content != bigOut {
		t.Fatalf("PrepareRequestHistory mutated caller history slice")
	}
}

func TestLoopGuardCyclesStagnationAndRepetition(t *testing.T) {
	lg := &loopGuard{}
	// Alternating A -> B -> A -> B -> A -> B cycle.
	for i := 0; i < 2; i++ {
		if w, stop := lg.checkCall("terminal", `{"cmd":"pgrep x"}`); w != "" || stop {
			t.Fatalf("unexpected early trigger on A #%d: %q %v", i, w, stop)
		}
		if w, stop := lg.checkCall("read_file", `{"path":"tmp/a.log"}`); w != "" || stop {
			t.Fatalf("unexpected early trigger on B #%d: %q %v", i, w, stop)
		}
	}
	_, _ = lg.checkCall("terminal", `{"cmd":"pgrep x"}`)
	w, stop := lg.checkCall("read_file", `{"path":"tmp/a.log"}`)
	if w == "" || stop {
		t.Fatalf("expected alternating cycle warning (not yet hard stop), got w=%q stop=%v", w, stop)
	}

	// Stagnant identical output 4x triggers warning.
	lg2 := &loopGuard{}
	for i := 0; i < 3; i++ {
		if sw, _ := lg2.recordResult("terminal", "still running pid 12345"); sw != "" {
			t.Fatalf("unexpected stagnant warning at #%d", i)
		}
	}
	if sw, _ := lg2.recordResult("terminal", "still running pid 12345"); !strings.Contains(sw, "STAGNASI") {
		t.Fatalf("expected stagnation warning on 4th identical output, got %q", sw)
	}

	// Repeated assistant narration 3x triggers repetition.
	lg3 := &loopGuard{}
	msg := "Saya sedang mengecek kembali proses yang masih berjalan di server sekarang."
	if rep, _ := lg3.checkAssistantText(msg); rep {
		t.Fatal("unexpected rep on 1st")
	}
	if rep, _ := lg3.checkAssistantText(msg); rep {
		t.Fatal("unexpected rep on 2nd")
	}
	if rep, _ := lg3.checkAssistantText(msg); !rep {
		t.Fatal("expected repetition detected on 3rd identical assistant text")
	}
}

type deliverSameReplyProv struct{ step int }

func (p *deliverSameReplyProv) Name() string { return "dup" }
func (p *deliverSameReplyProv) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	p.step++
	ch := make(chan provider.Event, 2)
	if p.step == 1 {
		ch <- provider.Event{ToolCalls: []provider.ToolCall{{ID: "d1", Name: "deliver_text", Arguments: `{"text":"Laporan lengkap hasil pengecekan server selesai tanpa kendala sama sekali."}`}}}
	} else {
		ch <- provider.Event{Text: "Laporan lengkap hasil pengecekan server selesai tanpa kendala sama sekali."}
	}
	ch <- provider.Event{Done: true}
	close(ch)
	return ch, nil
}

type fakeTextDeliverTool struct{}

func (f *fakeTextDeliverTool) Name() string            { return "deliver_text" }
func (f *fakeTextDeliverTool) Description() string     { return "deliver" }
func (f *fakeTextDeliverTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *fakeTextDeliverTool) Danger() bool            { return false }
func (f *fakeTextDeliverTool) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct{ Text string }
	_ = json.Unmarshal(raw, &in)
	tool.MarkChatDeliveredText(ctx, tool.ChatIDFrom(ctx), in.Text)
	return "sent", nil
}

func TestDuplicateDeliverySuppressionAndRuleAnchor(t *testing.T) {
	tmp := t.TempDir()
	st, err := store.Open(filepath.Join(tmp, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mem, err := memory.Open(filepath.Join(tmp, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Write(memory.KindMemory, "- Jangan pernah pakai REST delete media"); err != nil {
		t.Fatal(err)
	}

	r := &Runner{
		Provider: &deliverSameReplyProv{},
		Tools:    tool.NewRegistry(&fakeTextDeliverTool{}),
		Store:    st,
		Memory:   mem,
		Model:    "v4",
	}
	sys1 := r.system()
	if !strings.Contains(sys1, "Jangan pernah pakai REST delete media") {
		t.Fatalf("expected memory rule in system prompt")
	}

	// Simulate external edit on disk without calling InvalidatePrompt.
	time.Sleep(15 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(tmp, "memory", "MEMORY.md"), []byte("- Wajib gunakan GraphQL v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	sys2 := r.system()
	if !strings.Contains(sys2, "Wajib gunakan GraphQL v2") {
		t.Fatalf("expected Runner.system() to auto-refresh on disk change, got: %s", sys2)
	}

	anchor := r.ruleAnchorBlock()
	if !strings.Contains(anchor, "PENGINGAT ATURAN AKTIF") || !strings.Contains(anchor, "GraphQL v2") {
		t.Fatalf("expected active rule anchor with memory rule, got: %s", anchor)
	}

	out, err := r.Run(tool.WithChatID(context.Background(), 999), 999, "kirim laporan")
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected duplicate final reply to be suppressed, got %q", out)
	}
}

func TestDetailedStructuredTelemetryLogs(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	r := &Runner{
		Provider: &deliverSameReplyProv{},
		Tools:    tool.NewRegistry(&fakeTextDeliverTool{}),
		Store:    st,
		Model:    "v4",
		Logger:   logger,
	}
	if _, err := r.Run(tool.WithChatID(context.Background(), 321), 321, "cek log detail"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	logs := logBuf.String()
	for _, want := range []string{`"msg":"turn_started"`, `"msg":"llm_step"`, `"msg":"tool_executed"`, `"msg":"turn_finished"`, `"prompt_preview":"cek log detail"`, `"output_preview":"sent"`} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected %s in detailed logs, got:\n%s", want, logs)
		}
	}
}
