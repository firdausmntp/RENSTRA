package tool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"renstra/internal/tool"
)

func TestReadFileRejectsNegativeOffset(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(p, []byte("satu\ndua\ntiga"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []map[string]any{
		{"path": p, "offset": -1},
		{"path": p, "limit": -3},
		{"path": p, "offset": -1, "limit": 2},
	} {
		if _, err := tool.NewReadFile(dir).Execute(context.Background(), args(t, bad)); err == nil {
			t.Fatalf("want error untuk %+v", bad)
		}
	}
}

func TestReadFileRejectsNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := tool.NewReadFile(dir).Execute(context.Background(), args(t, map[string]any{"path": dir})); err == nil {
		t.Fatal("want error untuk direktori")
	}
}

func TestReadFileResolvesRelativeToCwd(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rel.txt"), []byte("isi relatif"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := tool.NewReadFile(dir).Execute(context.Background(), args(t, map[string]any{"path": "rel.txt"}))
	if err != nil {
		t.Fatal(err)
	}
	if out != "isi relatif" {
		t.Fatalf("got %q", out)
	}
}

func TestTruncateHandlesInvalidUTF8(t *testing.T) {
	dir := t.TempDir()
	in := "\xff" + strings.Repeat("a", 40000)
	out, err := tool.Truncate(in, 30*1024, dir, "call-invalid")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 30*1024-64 {
		t.Fatalf("output terlalu pendek: %d byte", len(out))
	}
	if !strings.Contains(out, "[truncated:") {
		t.Fatal("tidak ada anotasi truncate")
	}
}

func TestSpillNameCannotEscapeDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := tool.Truncate(strings.Repeat("x", 40000), 1024, dir, "../../escaped")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "..", "escaped.txt")); err == nil {
		t.Fatal("spill menulis di luar .tool-output")
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".tool-output"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("file spill = %d", len(entries))
	}
	if strings.ContainsAny(entries[0].Name(), `/\:`) {
		t.Fatalf("nama file tidak disanitasi: %q", entries[0].Name())
	}
}

func TestTerminalCapsOutput(t *testing.T) {
	term := tool.NewTerminal(t.TempDir(), 10*time.Second)
	cmd := "for /L %i in (1,1,20000) do @echo line %i"
	if runtime.GOOS != "windows" {
		cmd = "yes oom-test"
	}
	out, err := term.Execute(context.Background(), json.RawMessage(`{"cmd":"`+cmd+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 64*1024+500 {
		t.Fatalf("output %d byte, tidak dipotong", len(out))
	}
	if !strings.Contains(out, "dipotong") {
		t.Fatalf("tidak ada penanda potong: %q", out[max(0, len(out)-200):])
	}
}

func TestTerminalEnvAllowlist(t *testing.T) {
	t.Setenv("RENSTRA_SECRET_LEAK", "jangan-bocor")
	term := tool.NewTerminal(t.TempDir(), 10*time.Second)
	cmd := "echo [%RENSTRA_SECRET_LEAK%]"
	if runtime.GOOS != "windows" {
		cmd = "echo [$RENSTRA_SECRET_LEAK]"
	}
	out, err := term.Execute(context.Background(), json.RawMessage(`{"cmd":"`+cmd+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "jangan-bocor") {
		t.Fatalf("secret diwariskan ke subprocess: %q", out)
	}
}

func TestTerminalExtraEnvPassedThrough(t *testing.T) {
	term := tool.NewTerminal(t.TempDir(), 10*time.Second).WithExtraEnv([]string{"RENSTRA_TEST_EXTRA"})
	t.Setenv("RENSTRA_TEST_EXTRA", "boleh")
	cmd := "echo [%RENSTRA_TEST_EXTRA%]"
	if runtime.GOOS != "windows" {
		cmd = "echo [$RENSTRA_TEST_EXTRA]"
	}
	out, err := term.Execute(context.Background(), json.RawMessage(`{"cmd":"`+cmd+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "[boleh]") {
		t.Fatalf("env tambahan tidak diteruskan: %q", out)
	}
}

func TestWriteFileIsAtomicOnExistingFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.txt")
	if _, err := tool.NewWriteFile(dir).Execute(context.Background(), args(t, map[string]string{"path": p, "content": "lama"})); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.NewWriteFile(dir).Execute(context.Background(), args(t, map[string]string{"path": p, "content": "baru"})); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "baru" {
		t.Fatalf("got %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("file sisa temp: %d", len(entries))
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
