package tool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"renstra/internal/tool"
)

func args(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestPatchReplacesUniqueText(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("satu dua tiga"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := tool.NewPatch().Execute(context.Background(), args(t, map[string]string{"path": p, "old": "dua", "new": "DUA"}))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "satu DUA tiga" {
		t.Fatalf("got %q (%s)", b, out)
	}
}

func TestPatchFailsWhenOldNotFoundOrAmbiguous(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("x x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.NewPatch().Execute(context.Background(), args(t, map[string]string{"path": p, "old": "x", "new": "y"})); err == nil {
		t.Fatal("want error untuk old yang ambigu")
	}
	if _, err := tool.NewPatch().Execute(context.Background(), args(t, map[string]string{"path": p, "old": "tidak-ada", "new": "y"})); err == nil {
		t.Fatal("want error untuk old yang tidak ditemukan")
	}
}

func TestReadFileAbsolutePathAllowed(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("isi"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := tool.NewReadFile().Execute(context.Background(), args(t, map[string]any{"path": p}))
	if err != nil {
		t.Fatal(err)
	}
	if out != "isi" {
		t.Fatalf("got %q", out)
	}
}

func TestWriteFileCreatesParents(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x", "y", "b.txt")
	if _, err := tool.NewWriteFile().Execute(context.Background(), args(t, map[string]string{"path": p, "content": "halo"})); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "halo" {
		t.Fatalf("got %q", b)
	}
}
