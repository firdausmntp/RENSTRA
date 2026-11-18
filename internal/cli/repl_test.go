package cli_test

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/provider"
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
		"Model aktif: gpt-4o-mini",
		"Model aktif diganti ke: gpt-4o",
		"Skill registry tidak aktif.",
		"Memory store tidak aktif.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in REPL output:\n%s", want, got)
		}
	}
}
