package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultRepo     = "firdausmntp/RENSTRA"
	DefaultCacheTTL = 6 * time.Hour
	cacheFileName   = "update_check.json"
)

// ReleaseAsset describes one downloadable asset attached to a GitHub release.
type ReleaseAsset struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"browser_download_url"`
}

// ReleaseInfo holds metadata for a GitHub release.
type ReleaseInfo struct {
	TagName     string         `json:"tag_name"`
	Name        string         `json:"name"`
	HTMLURL     string         `json:"html_url"`
	PublishedAt time.Time      `json:"published_at"`
	Body        string         `json:"body"`
	Assets      []ReleaseAsset `json:"assets"`
}

type cachedCheck struct {
	CheckedAt time.Time    `json:"checked_at"`
	Release   *ReleaseInfo `json:"release"`
}

// TargetAssetName returns the release binary name for the current platform.
func TargetAssetName(goos, goarch string) string {
	ext := ""
	if goos == "windows" {
		ext = ".exe"
	}
	return fmt.Sprintf("renstra-%s-%s%s", goos, goarch, ext)
}

// IsNewer reports whether candidate is newer than current using semver comparison.
func IsNewer(candidate, current string) bool {
	cNorm := normalizeVersion(candidate)
	currNorm := normalizeVersion(current)
	if cNorm == "" || currNorm == "" {
		return false
	}
	cParts := parseSemver(cNorm)
	currParts := parseSemver(currNorm)
	for i := 0; i < 3; i++ {
		if cParts[i] > currParts[i] {
			return true
		}
		if cParts[i] < currParts[i] {
			return false
		}
	}
	return false
}

func normalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "v")
	return v
}

func parseSemver(v string) [3]int {
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		// Clean any extra suffix like -beta, -rc1
		sub := parts[i]
		if idx := strings.IndexAny(sub, "-+"); idx != -1 {
			sub = sub[:idx]
		}
		n, _ := strconv.Atoi(sub)
		out[i] = n
	}
	return out
}

// FetchLatestRelease queries the GitHub API for the latest published release.
func FetchLatestRelease(ctx context.Context, repo, currentVersion string) (*ReleaseInfo, error) {
	if repo == "" {
		repo = DefaultRepo
	}
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	userAgent := "renstra"
	if currentVersion != "" {
		userAgent += "/" + currentVersion
	}
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gagal menghubungi GitHub API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("belum ada rilis yang dipublikasikan")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API mengembalikan HTTP %d", resp.StatusCode)
	}

	var rel ReleaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, fmt.Errorf("gagal membaca data rilis: %w", err)
	}
	return &rel, nil
}

// PeekCachedUpdate reads the local update_check.json cache without performing
// any network request, returning the cached release if it is newer than currentVersion.
func PeekCachedUpdate(homeDir, currentVersion string) (*ReleaseInfo, bool) {
	if homeDir == "" {
		return nil, false
	}
	data, err := os.ReadFile(filepath.Join(homeDir, cacheFileName))
	if err != nil {
		return nil, false
	}
	var cc cachedCheck
	if json.Unmarshal(data, &cc) != nil || cc.Release == nil {
		return nil, false
	}
	if IsNewer(cc.Release.TagName, currentVersion) {
		return cc.Release, true
	}
	return cc.Release, false
}

// CheckCachedUpdate checks if a new version exists, utilizing a local cache
// file inside homeDir to prevent GitHub API rate limiting.
func CheckCachedUpdate(homeDir, currentVersion string, ttl time.Duration) (*ReleaseInfo, bool) {
	if homeDir == "" {
		return nil, false
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	cachePath := filepath.Join(homeDir, cacheFileName)
	if data, err := os.ReadFile(cachePath); err == nil {
		var cc cachedCheck
		if json.Unmarshal(data, &cc) == nil && time.Since(cc.CheckedAt) < ttl && cc.Release != nil {
			if IsNewer(cc.Release.TagName, currentVersion) {
				return cc.Release, true
			}
			return cc.Release, false
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	rel, err := FetchLatestRelease(ctx, DefaultRepo, currentVersion)
	if err != nil || rel == nil {
		return nil, false
	}

	cc := cachedCheck{
		CheckedAt: time.Now(),
		Release:   rel,
	}
	if b, err := json.Marshal(cc); err == nil {
		_ = os.WriteFile(cachePath, b, 0o600)
	}

	if IsNewer(rel.TagName, currentVersion) {
		return rel, true
	}
	return rel, false
}

// DownloadAsset downloads the binary matching the current OS/Arch and replaces
// the running executable safely across Linux, macOS, and Windows.
func DownloadAsset(ctx context.Context, rel *ReleaseInfo, progress func(downloaded, total int64)) error {
	targetName := TargetAssetName(runtime.GOOS, runtime.GOARCH)
	var asset *ReleaseAsset
	for i := range rel.Assets {
		if strings.EqualFold(rel.Assets[i].Name, targetName) {
			asset = &rel.Assets[i]
			break
		}
	}
	if asset == nil {
		return fmt.Errorf("asset binary %s tidak ditemukan di rilis %s", targetName, rel.TagName)
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("tidak dapat menemukan path executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	exeDir := filepath.Dir(exePath)
	tmpFile, err := os.CreateTemp(exeDir, ".renstra-update-*.tmp")
	if err != nil {
		return fmt.Errorf("gagal membuat file temp di %s: %w", exeDir, err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		_ = os.Remove(tmpPath)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.DownloadURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "renstra-updater")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("gagal mengunduh binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download gagal dengan HTTP %d", resp.StatusCode)
	}

	totalSize := asset.Size
	if totalSize <= 0 {
		totalSize = resp.ContentLength
	}

	var reader io.Reader = resp.Body
	if progress != nil {
		reader = &progressReader{r: resp.Body, total: totalSize, hook: progress}
	}

	if _, err := io.Copy(tmpFile, reader); err != nil {
		return fmt.Errorf("gagal menyimpan file unduhan: %w", err)
	}
	_ = tmpFile.Close()

	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("gagal mengatur izin eksekusi: %w", err)
	}

	return replaceExecutable(tmpPath, exePath)
}

func replaceExecutable(newPath, currentPath string) error {
	if runtime.GOOS == "windows" {
		oldBackup := currentPath + fmt.Sprintf(".old-%d", time.Now().UnixNano())
		if err := os.Rename(currentPath, oldBackup); err != nil {
			return fmt.Errorf("gagal memindahkan executable lama (Windows): %w", err)
		}
		if err := os.Rename(newPath, currentPath); err != nil {
			// Rollback if possible
			_ = os.Rename(oldBackup, currentPath)
			return fmt.Errorf("gagal memasang executable baru: %w", err)
		}
		_ = os.Remove(oldBackup) // Best-effort cleanup
		return nil
	}

	// Unix / macOS: atomic rename replaces active file inode directly
	if err := os.Rename(newPath, currentPath); err != nil {
		return fmt.Errorf("gagal mengganti binary (os.Rename): %w", err)
	}
	return nil
}

type progressReader struct {
	r          io.Reader
	downloaded int64
	total      int64
	hook       func(int64, int64)
	lastNotice time.Time
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.downloaded += int64(n)
	now := time.Now()
	if now.Sub(pr.lastNotice) >= 200*time.Millisecond || err != nil {
		pr.lastNotice = now
		if pr.hook != nil {
			pr.hook(pr.downloaded, pr.total)
		}
	}
	return n, err
}
