package tool_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/tool"
)

func TestMemoryToolWritesAndReports(t *testing.T) {
	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mt := tool.NewMemory(mem)

	out, err := mt.Execute(context.Background(), json.RawMessage(`{"target":"memory","action":"write","content":"user suka kopi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "berhasil") {
		t.Fatalf("got %q", out)
	}
	snap := mem.Snapshot()
	if snap.Memory != "user suka kopi" {
		t.Fatalf("snapshot memory = %q", snap.Memory)
	}
}

func TestMemoryToolRejectsOverflow(t *testing.T) {
	mem, _ := memory.Open(t.TempDir())
	mt := tool.NewMemory(mem)

	long := strings.Repeat("x", 2300)
	_, err := mt.Execute(context.Background(), json.RawMessage(`{"target":"memory","action":"write","content":"`+long+`"}`))
	if err == nil || !strings.Contains(err.Error(), "terlalu panjang") {
		t.Fatalf("want overflow error, got %v", err)
	}
}

func TestSkillToolLoadsBody(t *testing.T) {
	dir := t.TempDir()
	reg, _ := skill.Load(skill.Layer{Dir: dir, Writable: true})
	_ = reg.Create("git-helper", "bantu git", "selalu gunakan git status dulu")

	st := tool.NewSkill(reg)
	out, err := st.Execute(context.Background(), json.RawMessage(`{"action":"load","name":"git-helper"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "git status") {
		t.Fatalf("got %q", out)
	}
}

func TestSkillToolSaveUpserts(t *testing.T) {
	reg, _ := skill.Load(skill.Layer{Dir: t.TempDir(), Writable: true})
	st := tool.NewSkill(reg)

	// Pertama: belum ada -> create
	out, err := st.Execute(context.Background(), json.RawMessage(`{"action":"save","name":"deploy","description":"cara deploy","body":"step 1"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "baru") {
		t.Fatalf("got %q", out)
	}

	// Kedua: sudah ada -> update, tidak error duplikat
	out, err = st.Execute(context.Background(), json.RawMessage(`{"action":"save","name":"deploy","description":"cara deploy v2","body":"step 1, step 2"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "diperbarui") {
		t.Fatalf("got %q", out)
	}

	sk, _ := reg.Get("deploy")
	if sk.Description != "cara deploy v2" || !strings.Contains(sk.Body, "step 2") {
		t.Fatalf("skill tidak ter-update: %+v", sk)
	}

	// Delete
	out, err = st.Execute(context.Background(), json.RawMessage(`{"action":"delete","name":"deploy"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "dihapus") {
		t.Fatalf("got %q", out)
	}
	if _, ok := reg.Get("deploy"); ok {
		t.Fatal("skill harus terhapus")
	}
}
