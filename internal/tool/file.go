package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const maxReadSize = 10 * 1024 * 1024

// resolve makes relative paths resolve against the tool's cwd, matching the
// terminal and the prompt. Absolute paths are left alone: the agent may read
// anywhere the OS user can.
func resolve(cwd, path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if cwd == "" {
		return path
	}
	return filepath.Join(cwd, path)
}

type ReadFile struct{ cwd string }

func NewReadFile(cwd string) *ReadFile { return &ReadFile{cwd: cwd} }

func (t *ReadFile) Name() string { return "read_file" }
func (t *ReadFile) Description() string {
	return "Membaca file teks (UTF-8). Path absolut diperbolehkan; path relatif dihitung dari workspace. Mendukung offset/limit baris (0-based, tidak boleh negatif). Menolak file > 10 MB dan non-regular file."
}
func (t *ReadFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","minimum":0,"description":"baris awal, 0-based"},"limit":{"type":"integer","minimum":0,"description":"jumlah baris"}},"required":["path"]}`)
}
func (t *ReadFile) Danger() bool { return false }

func (t *ReadFile) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen read_file tidak valid: %w", err)
	}
	if in.Offset < 0 || in.Limit < 0 {
		return "", fmt.Errorf("offset/limit tidak boleh negatif (offset=%d limit=%d)", in.Offset, in.Limit)
	}
	path := resolve(t.cwd, in.Path)
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s bukan file regular (%s)", path, info.Mode())
	}
	if info.Size() > maxReadSize {
		return "", fmt.Errorf("%s berukuran %d byte (batas %d)", path, info.Size(), maxReadSize)
	}
	raw2, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSuffix(string(raw2), "\n"), "\n")
	if in.Offset > 0 || in.Limit > 0 {
		if in.Offset >= len(lines) {
			return "", nil
		}
		end := len(lines)
		if in.Limit > 0 && in.Offset+in.Limit < end {
			end = in.Offset + in.Limit
		}
		lines = lines[in.Offset:end]
	}
	return strings.Join(lines, "\n"), nil
}

type WriteFile struct{ cwd string }

func NewWriteFile(cwd string) *WriteFile { return &WriteFile{cwd: cwd} }

func (t *WriteFile) Name() string { return "write_file" }
func (t *WriteFile) Description() string {
	return "Menulis file (menimpa isi lama). Direktori induk dibuat bila belum ada. Path relatif dihitung dari workspace. Penulisan atomik (temp + rename)."
}
func (t *WriteFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"}},"required":["path","content"]}`)
}
func (t *WriteFile) Danger() bool { return false }

func (t *WriteFile) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen write_file tidak valid: %w", err)
	}
	path := resolve(t.cwd, in.Path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(path, []byte(in.Content)); err != nil {
		return "", err
	}
	return fmt.Sprintf("ditulis: %s (%d byte)", path, len(in.Content)), nil
}

type Patch struct{ cwd string }

func NewPatch(cwd string) *Patch { return &Patch{cwd: cwd} }

func (t *Patch) Name() string { return "patch" }
func (t *Patch) Description() string {
	return "Mengganti satu kemunculan teks `old` dengan `new` di sebuah file. Gagal bila `old` tidak ditemukan atau muncul lebih dari sekali. Path relatif dihitung dari workspace."
}
func (t *Patch) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"old":{"type":"string"},"new":{"type":"string"}},"required":["path","old","new"]}`)
}
func (t *Patch) Danger() bool { return false }

func (t *Patch) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
		Old  string `json:"old"`
		New  string `json:"new"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen patch tidak valid: %w", err)
	}
	if in.Old == "" {
		return "", errors.New("old tidak boleh kosong")
	}
	path := resolve(t.cwd, in.Path)
	raw2, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	body := string(raw2)
	switch n := strings.Count(body, in.Old); n {
	case 0:
		return "", fmt.Errorf("teks old tidak ditemukan di %s", path)
	case 1:
	default:
		return "", fmt.Errorf("teks old muncul %d kali di %s; perlu konteks lebih unik", n, path)
	}
	if err := atomicWrite(path, []byte(strings.Replace(body, in.Old, in.New, 1))); err != nil {
		return "", err
	}
	return fmt.Sprintf("diperbarui: %s", path), nil
}

// atomicWrite writes to a sibling temp file and renames, so a crash never
// leaves a half-written file.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".renstra-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o644); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
