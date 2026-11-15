package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"renstra/internal/agent"
	"renstra/internal/cli"
	"renstra/internal/provider"
	"renstra/internal/tool"
)

func TestApproverRejectsOnNo(t *testing.T) {
	approve := cli.Approver(strings.NewReader("n\n"), io.Discard)
	ok, err := approve(context.Background(), "terminal", json.RawMessage(`{"cmd":"rm -rf /"}`))
	if err != nil || ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestApproverAcceptsYes(t *testing.T) {
	approve := cli.Approver(strings.NewReader("y\n"), io.Discard)
	ok, err := approve(context.Background(), "terminal", json.RawMessage(`{"cmd":"ls"}`))
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}

func TestDangerousToolDeniedDoesNotRun(t *testing.T) {
	s := newStore(t)
	fp := &fakeProvider{steps: [][]provider.Event{
		{{ToolCalls: []provider.ToolCall{{ID: "c1", Name: "terminal", Arguments: `{"cmd":"echo tidak-jalan"}`}}}},
		{{Text: "ok", Done: true}},
	}}
	var out bytes.Buffer
	r := &agent.Runner{
		Provider: fp,
		Tools:    tool.NewRegistry(tool.NewTerminal(t.TempDir(), time.Second)),
		Store:    s, Model: "m", MaxIters: 5, Cwd: t.TempDir(), Out: &out,
		Approve: func(context.Context, string, json.RawMessage) (bool, error) { return false, nil },
	}
	if _, err := r.Run(context.Background(), 1, "x"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "tidak-jalan") {
		t.Fatal("tool jalan padahal ditolak")
	}
	msgs, _ := s.Messages(context.Background(), 1)
	if !strings.Contains(msgs[2].Content, "ditolak") {
		t.Fatalf("hasil tool = %q", msgs[2].Content)
	}
}
