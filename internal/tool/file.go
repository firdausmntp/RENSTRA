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

type ReadFile struct{}

func NewReadFile() *ReadFile { return &ReadFile{} }

func (t *ReadFile) Name() string { return "read_file" }
func (t *ReadFile) Description() string {
	return "Membaca file teks (UTF-8). Path absolut diperbolehkan. Mendukung offset/limit baris. Menolak file > 10 MB."
}
func (t *ReadFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"integer","description":"baris awal, 0-based"},"limit":{"type":"integer","description":"jumlah baris"}},"required":["path"]}`)
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
	info, err := os.Stat(in.Path)
	if err != nil {
		return "", err
	}
	if info.Size() > maxReadSize {
		return "", fmt.Errorf("%s berukuran %d byte (batas %d)", in.Path, info.Size(), maxReadSize)
	}
	raw2, err := os.ReadFile(in.Path)
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

type WriteFile struct{}

func NewWriteFile() *WriteFile { return &WriteFile{} }

func (t *WriteFile) Name() string { return "write_file" }
func (t *WriteFile) Description() string {
	return "Menulis file (menimpa isi lama). Direktori induk dibuat bila belum ada."
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
	if err := os.MkdirAll(filepath.Dir(in.Path), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(in.Path, []byte(in.Content), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("ditulis: %s (%d byte)", in.Path, len(in.Content)), nil
}

type Patch struct{}

func NewPatch() *Patch { return &Patch{} }

func (t *Patch) Name() string { return "patch" }
func (t *Patch) Description() string {
	return "Mengganti satu kemunculan teks `old` dengan `new` di sebuah file. Gagal bila `old` tidak ditemukan atau muncul lebih dari sekali."
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
	raw2, err := os.ReadFile(in.Path)
	if err != nil {
		return "", err
	}
	body := string(raw2)
	switch n := strings.Count(body, in.Old); n {
	case 0:
		return "", fmt.Errorf("teks old tidak ditemukan di %s", in.Path)
	case 1:
	default:
		return "", fmt.Errorf("teks old muncul %d kali di %s; perlu konteks lebih unik", n, in.Path)
	}
	if err := os.WriteFile(in.Path, []byte(strings.Replace(body, in.Old, in.New, 1)), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("diperbarui: %s", in.Path), nil
}
