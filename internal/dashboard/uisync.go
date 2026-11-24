package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CurrentAPIVersion is the backend REST/SSE contract version supported by this binary.
const CurrentAPIVersion = 1

const (
	defaultUIRepo   = "firdausmntp/RENSTRA"
	defaultUIBranch = "main"
)

// UIManifest describes a remotely syncable dashboard UI bundle.
type UIManifest struct {
	UIVersion     string   `json:"ui_version"`
	MinAPIVersion int      `json:"min_api_version"`
	MaxAPIVersion int      `json:"max_api_version"`
	Files         []string `json:"files"`
}

// ResolveUIFS selects the active UI filesystem using 3-layer priority:
// 1. $RENSTRA_HOME/dashboard-ui/ (local override)
// 2. $RENSTRA_HOME/dashboard-cache/ (synced from GitHub)
// 3. Embedded UI inside the Go binary (fallback)
func ResolveUIFS(homeDir string, embedded fs.FS) (fs.FS, string, string) {
	if strings.TrimSpace(homeDir) != "" {
		localDir := filepath.Join(homeDir, "dashboard-ui")
		if hasIndex(localDir) {
			return os.DirFS(localDir), "local", readManifestVersion(localDir)
		}
		cacheDir := filepath.Join(homeDir, "dashboard-cache")
		if hasIndex(cacheDir) {
			return os.DirFS(cacheDir), "synced", readManifestVersion(cacheDir)
		}
	}
	ver := ""
	if raw, err := fs.ReadFile(embedded, "manifest.json"); err == nil {
		var m UIManifest
		if json.Unmarshal(raw, &m) == nil {
			ver = m.UIVersion
		}
	}
	return embedded, "embedded", ver
}

func hasIndex(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !st.IsDir()
}

func readManifestVersion(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return ""
	}
	var m UIManifest
	if json.Unmarshal(raw, &m) != nil {
		return ""
	}
	return m.UIVersion
}

// SyncUI downloads the latest compatible dashboard UI files from GitHub into
// $RENSTRA_HOME/dashboard-cache/ atomically without rebuilding the Go binary.
func SyncUI(ctx context.Context, homeDir, repo, branch string) (*UIManifest, error) {
	if strings.TrimSpace(homeDir) == "" {
		return nil, errors.New("home directory kosong")
	}
	if strings.TrimSpace(repo) == "" {
		repo = defaultUIRepo
	}
	branches := []string{strings.TrimSpace(branch)}
	if branches[0] == "" {
		branches = []string{defaultUIBranch, "feat/core-agent"}
	}

	client := &http.Client{Timeout: 20 * time.Second}
	var baseURL string
	var rawManifest []byte
	var err error
	for _, br := range branches {
		baseURL = fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/internal/dashboard/ui", strings.Trim(repo, "/"), strings.Trim(br, "/"))
		rawManifest, err = fetchUIFile(ctx, client, baseURL+"/manifest.json")
		if err == nil {
			break
		}
	}
	if err != nil {
		return nil, fmt.Errorf("gagal mengambil manifest UI: %w", err)
	}
	var manifest UIManifest
	if err := json.Unmarshal(rawManifest, &manifest); err != nil {
		return nil, fmt.Errorf("manifest UI tidak valid: %w", err)
	}
	if manifest.MinAPIVersion > CurrentAPIVersion || (manifest.MaxAPIVersion > 0 && CurrentAPIVersion > manifest.MaxAPIVersion) {
		return nil, fmt.Errorf("UI %s butuh API versi %d..%d (binary saat ini API v%d); jalankan renstra update",
			manifest.UIVersion, manifest.MinAPIVersion, manifest.MaxAPIVersion, CurrentAPIVersion)
	}
	if len(manifest.Files) == 0 {
		return nil, errors.New("daftar file pada manifest UI kosong")
	}

	tmpDir := filepath.Join(homeDir, "dashboard-cache.tmp")
	_ = os.RemoveAll(tmpDir)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), rawManifest, 0o644); err != nil {
		return nil, err
	}

	for _, rel := range manifest.Files {
		clean := filepath.ToSlash(filepath.Clean(strings.TrimLeft(rel, "/")))
		if clean == "." || strings.HasPrefix(clean, "..") || !isAllowedUIExt(clean) {
			return nil, fmt.Errorf("path UI tidak diizinkan: %q", rel)
		}
		if clean == "manifest.json" {
			continue
		}
		data, err := fetchUIFile(ctx, client, baseURL+"/"+clean)
		if err != nil {
			return nil, fmt.Errorf("gagal mengunduh %s: %w", clean, err)
		}
		dst := filepath.Join(tmpDir, filepath.FromSlash(clean))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return nil, err
		}
	}

	if !hasIndex(tmpDir) {
		return nil, errors.New("index.html tidak ditemukan pada bundle UI")
	}

	cacheDir := filepath.Join(homeDir, "dashboard-cache")
	backupDir := filepath.Join(homeDir, "dashboard-cache.bak")
	_ = os.RemoveAll(backupDir)
	if _, err := os.Stat(cacheDir); err == nil {
		if err := os.Rename(cacheDir, backupDir); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(tmpDir, cacheDir); err != nil {
		if _, berr := os.Stat(backupDir); berr == nil {
			_ = os.Rename(backupDir, cacheDir)
		}
		return nil, err
	}
	_ = os.RemoveAll(backupDir)
	return &manifest, nil
}

// ResetSyncedUI removes the remote-synced cache and reverts to the built-in embedded UI.
func ResetSyncedUI(homeDir string) error {
	if strings.TrimSpace(homeDir) == "" {
		return nil
	}
	return os.RemoveAll(filepath.Join(homeDir, "dashboard-cache"))
}

func fetchUIFile(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cache-Control", "no-cache")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

func isAllowedUIExt(name string) bool {
	lower := strings.ToLower(name)
	for _, ext := range []string{".html", ".css", ".js", ".svg", ".json", ".png"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
