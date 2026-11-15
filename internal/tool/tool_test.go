package tool_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"renstra/internal/tool"
)

func TestTruncateKeepsValidUTF8AndSpills(t *testing.T) {
	dir := t.TempDir()
	in := strings.Repeat("é", 20000) // 40000 byte
	out, err := tool.Truncate(in, 30*1024, dir, "call1")
	if err != nil {
		t.Fatal(err)
	}
	if !utf8.ValidString(out) {
		t.Fatal("output bukan utf8 valid")
	}
	if len(out) > 30*1024+200 {
		t.Fatalf("output %d byte", len(out))
	}
	if !strings.Contains(out, filepath.Join(".tool-output", "call1")) {
		t.Fatalf("tidak menyebut path spill: %s", out[:200])
	}
	entries, err := os.ReadDir(filepath.Join(dir, ".tool-output"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("spill file = %d", len(entries))
	}
}

func TestTerminalRunsCommand(t *testing.T) {
	term := tool.NewTerminal(t.TempDir(), 10*time.Second)
	out, err := term.Execute(context.Background(), json.RawMessage(`{"cmd":"echo hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("got %q", out)
	}
}

func TestTerminalReportsExitCode(t *testing.T) {
	term := tool.NewTerminal(t.TempDir(), 10*time.Second)
	out, _ := term.Execute(context.Background(), json.RawMessage(`{"cmd":"exit 3"}`))
	if !strings.Contains(out, "exit code 3") {
		t.Fatalf("got %q", out)
	}
}
