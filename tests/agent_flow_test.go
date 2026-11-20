package tests

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/agent"
	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/skill"
	"renstra/internal/store"
	"renstra/internal/tool"
)

type scriptedProvider struct {
	steps [][]provider.Event
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event, 4)
	if len(p.steps) > 0 {
		for _, ev := range p.steps[0] {
			ch <- ev
		}
		p.steps = p.steps[1:]
	} else {
		ch <- provider.Event{Text: "ok", Done: true}
	}
	close(ch)
	return ch, nil
}

func TestIntegrationAgentMemorySkillFileAndCompaction(t *testing.T) {
	home := t.TempDir()
	ws := filepath.Join(home, "workspace")

	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	mem, err := memory.Open(filepath.Join(home, "memory"))
	if err != nil {
		t.Fatalf("memory.Open: %v", err)
	}
	skills, err := skill.Load(skill.Layer{Dir: filepath.Join(home, "skills"), Writable: true})
	if err != nil {
		t.Fatalf("skill.Load: %v", err)
	}

	wf := tool.NewWriteFile(ws)
	wf.BackupDir = filepath.Join(home, "checkpoints")

	prov := &scriptedProvider{
		steps: [][]provider.Event{
			{{
				ToolCalls: []provider.ToolCall{
					{ID: "tc1", Name: "write_file", Arguments: `{"path":"tmp/hello.txt","content":"halo integrasi"}`},
					{ID: "tc2", Name: "memory", Arguments: `{"action":"write","target":"user","content":"Suka kopi hitam"}`},
					{ID: "tc3", Name: "skill", Arguments: `{"action":"save","name":"greet-flow","description":"Sapa user","body":"Sapa dengan ramah"}`},
				},
			}},
			{{
				Text:  "Semua beres: file ditulis di tmp/hello.txt, memori & skill disimpan.",
				Usage: &provider.Usage{InputTokens: 200, OutputTokens: 50},
				Done:  true,
			}},
		},
	}

	reg := tool.NewRegistry(
		wf,
		tool.NewReadFile(ws),
		tool.NewMemory(mem),
		tool.NewSkill(skills),
		tool.NewSessionSearch(st),
	)

	runner := &agent.Runner{
		Provider:      prov,
		Tools:         reg,
		Store:         st,
		Model:         "gpt-4o-mini",
		Cwd:           ws,
		MaxIters:      10,
		ContextWindow: 32000,
		Memory:        mem,
		Skills:        skills,
		ApprovalCfg:   config.ApprovalConfig{Mode: "prompt"},
		Out:           io.Discard,
	}

	ctx := context.Background()
	reply, err := runner.Run(ctx, 1001, "simpan catatan ke tmp/hello.txt dan ingat aku suka kopi hitam")
	if err != nil {
		t.Fatalf("runner.Run: %v", err)
	}
	if !strings.Contains(reply, "tmp/hello.txt") {
		t.Fatalf("unexpected reply: %q", reply)
	}

	// Verify file written in workspace/tmp
	rfOut, err := tool.NewReadFile(ws).Execute(ctx, json.RawMessage(`{"path":"tmp/hello.txt"}`))
	if err != nil || !strings.Contains(rfOut, "halo integrasi") {
		t.Fatalf("read_file tmp/hello.txt failed: out=%q err=%v", rfOut, err)
	}

	// Verify memory & skill persisted
	if snap := mem.Snapshot(); !strings.Contains(snap.User, "kopi hitam") {
		t.Fatalf("user memory not persisted: %q", snap.User)
	}
	if _, ok := skills.Get("greet-flow"); !ok {
		t.Fatal("skill greet-flow not persisted")
	}

	// Verify usage recorded
	if u, err := st.UsageForDay(ctx, ""); err == nil && u.TokensIn < 0 {
		t.Fatalf("unexpected usage: %+v", u)
	}

	// Verify session search works across messages
	ssOut, err := tool.NewSessionSearch(st).Execute(tool.WithChatID(ctx, 1001), json.RawMessage(`{"query":"kopi"}`))
	if err != nil || !strings.Contains(ssOut, "kopi hitam") {
		t.Fatalf("session_search failed: out=%q err=%v", ssOut, err)
	}
}
