package cli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/provider"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/tool"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

type fakeProvider struct{ steps [][]provider.Event }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	go func() {
		defer close(ch)
		if len(f.steps) == 0 {
			ch <- provider.Event{Text: "selesai", Done: true}
			return
		}
		for _, ev := range f.steps[0] {
			ch <- ev
		}
		f.steps = f.steps[1:]
	}()
	return ch, nil
}

func TestReplNewResetsSession(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{{{Text: "hai", Done: true}}, {{Text: "hai lagi", Done: true}}}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	in := strings.NewReader("halo\n/new\nhalo lagi\n/exit\n")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), r, 1, in, &out); err != nil {
		t.Fatal(err)
	}
	msgs, err := s.Messages(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("pesan = %d, want 2 (satu sesi baru)", len(msgs))
	}
}

func TestReplUnknownCommandIsSentToModel(t *testing.T) {
	fp := &fakeProvider{steps: [][]provider.Event{{{Text: "ok", Done: true}}}}
	r := &agent.Runner{Provider: fp, Tools: tool.NewRegistry(), Store: newStore(t), Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard}
	in := strings.NewReader("/bukan-perintah halo\n/exit\n")
	if err := cli.Run(context.Background(), r, 1, in, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestReplParityCommands(t *testing.T) {
	s := newStore(t)
	r := &agent.Runner{
		Provider: &fakeProvider{},
		Tools:    tool.NewRegistry(),
		Store:    s,
		Model:    "gpt-4o-mini",
		Cwd:      t.TempDir(),
		Out:      io.Discard,
	}
	in := strings.NewReader("/usage\n/model\n/model gpt-4o\n/skills\n/memory\n/exit\n")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), r, 1, in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Penggunaan hari ini",
		"Model aktif:",
		"gpt-4o-mini",
		"Model aktif diganti ke: gpt-4o",
		"Skill registry tidak aktif.",
		"Memory store tidak aktif.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in REPL output:\n%s", want, got)
		}
	}
}

func TestReplInsightsStatusVersionHelp(t *testing.T) {
	s := newStore(t)
	r := &agent.Runner{
		Provider: &fakeProvider{}, Tools: tool.NewRegistry(), Store: s,
		Model: "m-test", Cwd: t.TempDir(), ContextWindow: 16000, Out: io.Discard,
	}
	in := strings.NewReader("/insights\n/status\n/version\n/help\n/exit\n")
	var out bytes.Buffer
	if err := cli.RunWithOptions(context.Background(), r, 1, in, &out, cli.RunOptions{Version: "v9.9.9", Provider: "demo"}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Insights pemakaian", "Hari ini:", "7 hari:", "30 hari:",
		"Status sesi", "m-test", "(demo)", "Context", "renstra v9.9.9",
		"/cron add", "/insights", "/skills <nama>", "/status",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestReplCronRoundtrip(t *testing.T) {
	s := newStore(t)
	r := &agent.Runner{Provider: &fakeProvider{}, Tools: tool.NewRegistry(), Store: s, Model: "m", Cwd: t.TempDir(), Out: io.Discard}
	in := strings.NewReader("/cron list\n/cron add 0 9 * * * | ringkas berita\n/cron list\n/cron toggle 1\n/cron rm 1\n/cron list\n/exit\n")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), r, 1, in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"(tidak ada jadwal cron aktif)",
		"Jadwal cron ditambahkan (ID 1)",
		"ringkas berita",
		"Jadwal ID 1 dinonaktifkan.",
		"Jadwal ID 1 berhasil dihapus.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in cron output:\n%s", want, got)
		}
	}
	// Setelah dihapus, daftar harus kosong lagi.
	if strings.Count(got, "(tidak ada jadwal cron aktif)") != 2 {
		t.Fatalf("daftar cron akhir harus kosong:\n%s", got)
	}
}

func TestReplCronInvalidSchedule(t *testing.T) {
	s := newStore(t)
	r := &agent.Runner{Provider: &fakeProvider{}, Tools: tool.NewRegistry(), Store: s, Model: "m", Cwd: t.TempDir(), Out: io.Discard}
	in := strings.NewReader("/cron add bukan jadwal | halo\n/cron add 0 9 * * *\n/exit\n")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), r, 1, in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "jadwal cron tidak valid") || !strings.Contains(got, "Format: /cron add") {
		t.Fatalf("error cron tidak informatif:\n%s", got)
	}
}

func TestReplModelListing(t *testing.T) {
	s := newStore(t)
	r := &agent.Runner{Provider: &fakeProvider{}, Tools: tool.NewRegistry(), Store: s, Model: "m-aktif", Cwd: t.TempDir(), Out: io.Discard}
	in := strings.NewReader("/model\n/exit\n")
	var out bytes.Buffer
	opts := cli.RunOptions{ModelList: func() string { return "DAFTAR-MODEL-KUSTOM:\n  provider-x [model-a, model-b]" }}
	if err := cli.RunWithOptions(context.Background(), r, 1, in, &out, opts); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "m-aktif") || !strings.Contains(got, "DAFTAR-MODEL-KUSTOM") || !strings.Contains(got, "model-b") {
		t.Fatalf("listing model tidak tampil:\n%s", got)
	}
}

func TestReplSkillShow(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "demo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: demo-skill\ndescription: Skill demo untuk test\n---\n\nLangkah satu: lakukan X.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := skill.Load(skill.Layer{Dir: dir, Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	s := newStore(t)
	r := &agent.Runner{Provider: &fakeProvider{}, Tools: tool.NewRegistry(), Store: s, Model: "m", Cwd: t.TempDir(), Skills: reg, Out: io.Discard}
	in := strings.NewReader("/skills\n/skills demo-skill\n/skills tidak-ada\n/exit\n")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), r, 1, in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"demo-skill: Skill demo untuk test", "Skill: demo-skill", "Langkah satu: lakukan X.", "tidak ditemukan"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in skills output:\n%s", want, got)
		}
	}
}

type dangerTool struct{ executed *bool }

func (d *dangerTool) Name() string            { return "danger" }
func (d *dangerTool) Description() string     { return "tool berbahaya untuk test" }
func (d *dangerTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (d *dangerTool) Danger() bool            { return true }
func (d *dangerTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	*d.executed = true
	return "ok", nil
}

// TestReplApprovalSharesReader memastikan jawaban "y" yang di-pipe setelah
// perintah dibaca oleh approver lewat reader REPL yang sama (tidak dimakan
// read-ahead bufio), sehingga tool berbahaya benar-benar dieksekusi.
func TestReplApprovalSharesReader(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "danger", Arguments: `{}`}}}},
		{{Text: "selesai", Done: true}},
	}}
	executed := false
	r := &agent.Runner{
		Provider: fp, Tools: tool.NewRegistry(&dangerTool{&executed}), Store: s,
		Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: io.Discard,
	}
	in := strings.NewReader("jalankan\ny\n/exit\n")
	var out bytes.Buffer
	opts := cli.RunOptions{BindApprover: func(rd *bufio.Reader) {
		r.Approve = cli.ApproverFrom(rd, &out)
	}}
	if err := cli.RunWithOptions(context.Background(), r, 1, in, &out, opts); err != nil {
		t.Fatal(err)
	}
	if !executed {
		t.Fatalf("tool tidak dieksekusi padahal diapprove y:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Konfirmasi Eksekusi:") || !strings.Contains(out.String(), "Setujui? [y/N]") {
		t.Fatalf("prompt approval tidak tampil:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "selesai") {
		t.Fatalf("jawaban akhir hilang:\n%s", out.String())
	}
}

// delayProvider guarantees a measurable turn elapsed time even on Windows'
// coarse monotonic clock, so tok/s is always renderable in the footer test.
type delayProvider struct{ *fakeProvider }

func (d delayProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	time.Sleep(20 * time.Millisecond)
	return d.fakeProvider.Stream(ctx, req)
}

func TestReplTurnFooterShowsStats(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{{
		{Text: "jawaban akhir", Usage: &provider.Usage{InputTokens: 1500, OutputTokens: 300}, Done: true},
	}}}
	r := &agent.Runner{
		Provider: delayProvider{fp}, Tools: tool.NewRegistry(), Store: s, Model: "m",
		MaxIters: 5, Cwd: t.TempDir(), ContextWindow: 32000,
		PriceIn: 1.0, PriceOut: 2.0, Out: io.Discard,
	}
	in := strings.NewReader("halo\n/exit\n")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), r, 1, in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"jawaban akhir", "⏱", "↑1.5k ↓300 tok", "tok/s", "ctx", "$"} {
		if !strings.Contains(got, want) {
			t.Fatalf("footer stats missing %q in REPL output:\n%s", want, got)
		}
	}
	// Teks tidak boleh dobel (Out runner harus bisu saat CLI streaming).
	if strings.Count(got, "jawaban akhir") != 1 {
		t.Fatalf("teks stream terduplikasi:\n%s", got)
	}
}

func TestHelpSupportsEnglishAndIndonesian(t *testing.T) {
	s := newStore(t)
	r := &agent.Runner{Provider: &fakeProvider{}, Tools: tool.NewRegistry(), Store: s, Model: "m", Cwd: t.TempDir()}
	in := strings.NewReader("/help\n/lang en\n/help\n/help id\n/exit\n")
	var out bytes.Buffer
	if err := cli.Run(context.Background(), r, 1, in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Perintah RENSTRA CLI") || !strings.Contains(got, "RENSTRA CLI Commands") {
		t.Fatalf("output /help harus mendukung id & en:\n%s", got)
	}
}
