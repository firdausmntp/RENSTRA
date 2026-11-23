package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxReadSize = 10 * 1024 * 1024

// checkSensitivePath menolak akses ke file kredensial, konfigurasi rahasia,
// dan direktori sensitif (termasuk via symlink).
func checkSensitivePath(path string) error {
	candidates := []string{filepath.Clean(path)}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		candidates = append(candidates, resolved)
	} else if parentResolved, perr := filepath.EvalSymlinks(filepath.Dir(path)); perr == nil {
		candidates = append(candidates, filepath.Join(parentResolved, filepath.Base(path)))
	}
	for _, c := range candidates {
		if isSensitivePath(c) {
			return fmt.Errorf("akses ke file/direktori sensitif ditolak: %s", filepath.Base(path))
		}
	}
	return nil
}

func isSensitivePath(path string) bool {
	slashed := filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(slashed, "/")
	for _, part := range parts {
		switch strings.ToLower(part) {
		case ".ssh", ".aws", ".gnupg", ".kube":
			return true
		}
	}
	base := strings.ToLower(filepath.Base(slashed))
	if base == ".env" || strings.HasPrefix(base, ".env.") {
		return true
	}
	if base == "state.db" || strings.HasPrefix(base, "state.db-") {
		return true
	}
	switch base {
	case "config.yaml", "mcp.yaml", "auth.json":
		return true
	}
	return false
}

func backupExistingFile(backupDir, cwd, path string) {
	if backupDir == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxReadSize {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return
	}
	label := filepath.Base(path)
	if cwd != "" {
		if rel, rerr := filepath.Rel(cwd, path); rerr == nil && !strings.HasPrefix(rel, "..") {
			label = rel
		}
	}
	name := fmt.Sprintf("%s.%s.bak", sanitize(label), time.Now().UTC().Format("20060102T150405.000000000"))
	bakPath := filepath.Join(backupDir, name)
	_ = os.WriteFile(bakPath, data, 0o600)
	_ = os.WriteFile(bakPath+".path", []byte(path), 0o600)
}

// RestoreLastCheckpoint restores the most recent file backup in backupDir and
// removes that checkpoint entry so repeated /undo walks back step by step.
func RestoreLastCheckpoint(backupDir string) (string, error) {
	if strings.TrimSpace(backupDir) == "" {
		return "", errors.New("direktori checkpoint tidak dikonfigurasi")
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errors.New("belum ada checkpoint file untuk di-undo")
		}
		return "", err
	}
	var latestBak string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bak") {
			continue
		}
		full := filepath.Join(backupDir, e.Name())
		if _, err := os.Stat(full + ".path"); err != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if latestBak == "" || info.ModTime().After(latestMod) || (info.ModTime().Equal(latestMod) && full > latestBak) {
			latestBak = full
			latestMod = info.ModTime()
		}
	}
	if latestBak == "" {
		return "", errors.New("belum ada checkpoint file yang dapat di-undo")
	}
	origBytes, err := os.ReadFile(latestBak + ".path")
	if err != nil {
		return "", err
	}
	origPath := strings.TrimSpace(string(origBytes))
	data, err := os.ReadFile(latestBak)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(origPath), 0o755); err != nil {
		return "", err
	}
	if err := atomicWrite(origPath, data); err != nil {
		return "", err
	}
	_ = os.Remove(latestBak)
	_ = os.Remove(latestBak + ".path")
	return origPath, nil
}

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
	if err := checkSensitivePath(path); err != nil {
		return "", err
	}
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

type WriteFile struct {
	cwd       string
	BackupDir string
}

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
	if err := checkSensitivePath(path); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	backupExistingFile(t.BackupDir, t.cwd, path)
	if err := atomicWrite(path, []byte(in.Content)); err != nil {
		return "", err
	}
	return fmt.Sprintf("ditulis: %s (%d byte)", path, len(in.Content)), nil
}

type Patch struct {
	cwd       string
	BackupDir string
}

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
	if err := checkSensitivePath(path); err != nil {
		return "", err
	}
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
	backupExistingFile(t.BackupDir, t.cwd, path)
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
