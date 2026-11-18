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
		cmd = "seq 1 20000"
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

type noopSender struct{}

func (noopSender) SendFile(context.Context, int64, string, string, string) error { return nil }

func TestSensitivePathsBlocked(t *testing.T) {
	dir := t.TempDir()
	sshDir := filepath.Join(dir, ".ssh")
	if err := os.MkdirAll(sshDir, 0o755); err != nil {
		t.Fatal(err)
	}
	targets := []string{
		filepath.Join(dir, ".env"),
		filepath.Join(dir, ".env.production"),
		filepath.Join(dir, "config.yaml"),
		filepath.Join(dir, "mcp.yaml"),
		filepath.Join(dir, "state.db"),
		filepath.Join(dir, "state.db-wal"),
		filepath.Join(dir, "auth.json"),
		filepath.Join(sshDir, "id_rsa"),
	}
	for _, p := range targets {
		if err := os.WriteFile(p, []byte("secret"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	linkPath := filepath.Join(dir, "public.txt")
	if err := os.Symlink(filepath.Join(dir, ".env"), linkPath); err == nil {
		targets = append(targets, linkPath)
	}

	rf := tool.NewReadFile(dir)
	wf := tool.NewWriteFile(dir)
	pf := tool.NewPatch(dir)
	sf := tool.NewSendFile(dir, noopSender{})
	ctx := tool.WithChatID(context.Background(), 123)

	for _, p := range targets {
		if _, err := rf.Execute(ctx, args(t, map[string]string{"path": p})); err == nil {
			t.Errorf("read_file seharusnya menolak %s", p)
		}
		if _, err := wf.Execute(ctx, args(t, map[string]string{"path": p, "content": "x"})); err == nil {
			t.Errorf("write_file seharusnya menolak %s", p)
		}
		if _, err := pf.Execute(ctx, args(t, map[string]string{"path": p, "old": "secret", "new": "x"})); err == nil {
			t.Errorf("patch seharusnya menolak %s", p)
		}
		if _, err := sf.Execute(ctx, args(t, map[string]string{"path": p})); err == nil {
			t.Errorf("send_file seharusnya menolak %s", p)
		}
	}
}

func TestWriteFileAndPatchCreateBackupCheckpoint(t *testing.T) {
	dir := t.TempDir()
	backupDir := t.TempDir()
	p := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(p, []byte("versi1"), 0o644); err != nil {
		t.Fatal(err)
	}
	wf := tool.NewWriteFile(dir)
	wf.BackupDir = backupDir
	if _, err := wf.Execute(context.Background(), args(t, map[string]string{"path": p, "content": "versi2"})); err != nil {
		t.Fatal(err)
	}
	pf := tool.NewPatch(dir)
	pf.BackupDir = backupDir
	if _, err := pf.Execute(context.Background(), args(t, map[string]string{"path": p, "old": "versi2", "new": "versi3"})); err != nil {
		t.Fatal(err)
	}
	backups, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) < 2 {
		t.Fatalf("harus ada minimal 2 backup checkpoint, dapat %d", len(backups))
	}
}

func TestPruneOldFiles(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "old.txt")
	newFile := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	removed, err := tool.PruneOldFiles(dir, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, ingin 1", removed)
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatal("old.txt seharusnya terhapus")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatal("new.txt seharusnya dipertahankan")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
