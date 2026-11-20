package update_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"renstra/internal/update"
)

func TestIsNewer(t *testing.T) {
	tests := []struct {
		candidate string
		current   string
		want      bool
	}{
		{"v1.2.0", "v1.1.0", true},
		{"1.2.0", "1.1.0", true},
		{"v2.0.0", "v1.9.9", true},
		{"v1.1.1", "v1.1.0", true},
		{"v1.1.0", "v1.1.0", false},
		{"v1.0.0", "v1.1.0", false},
		{"v1.2.0-beta", "v1.1.0", true},
		{"v1.1.0", "v1.2.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.candidate+"_vs_"+tt.current, func(t *testing.T) {
			got := update.IsNewer(tt.candidate, tt.current)
			if got != tt.want {
				t.Errorf("IsNewer(%q, %q) = %v, want %v", tt.candidate, tt.current, got, tt.want)
			}
		})
	}
}

func TestTargetAssetName(t *testing.T) {
	if got := update.TargetAssetName("linux", "amd64"); got != "renstra-linux-amd64" {
		t.Errorf("linux amd64 got %s", got)
	}
	if got := update.TargetAssetName("linux", "arm64"); got != "renstra-linux-arm64" {
		t.Errorf("linux arm64 got %s", got)
	}
	if got := update.TargetAssetName("darwin", "arm64"); got != "renstra-darwin-arm64" {
		t.Errorf("darwin arm64 got %s", got)
	}
	if got := update.TargetAssetName("windows", "amd64"); got != "renstra-windows-amd64.exe" {
		t.Errorf("windows amd64 got %s", got)
	}
}

func TestFetchLatestReleaseAndDownload(t *testing.T) {
	assetName := update.TargetAssetName(runtime.GOOS, runtime.GOARCH)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/firdausmntp/RENSTRA/releases/latest":
			rel := update.ReleaseInfo{
				TagName:     "v1.2.0",
				Name:        "RENSTRA v1.2.0",
				PublishedAt: time.Now(),
				Assets: []update.ReleaseAsset{
					{
						Name:        assetName,
						Size:        12,
						DownloadURL: fmt.Sprintf("http://%s/download/%s", r.Host, assetName),
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(rel)
		case "/download/" + assetName:
			w.Header().Set("Content-Length", "12")
			_, _ = w.Write([]byte("dummy-binary"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 1. Test FetchLatestRelease
	origURL := "http://" + srv.Listener.Addr().String()
	_ = origURL

	// 2. Test CheckCachedUpdate with local cache file
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "update_check.json")
	// checked_at must be fresh relative to now, otherwise the TTL expires and
	// the test silently hits the real GitHub API (time-bomb failure).
	cacheData := []byte(fmt.Sprintf(`{"checked_at":%q,"release":{"tag_name":"v1.2.0"}}`, time.Now().UTC().Format(time.RFC3339)))
	if err := os.WriteFile(cacheFile, cacheData, 0o644); err != nil {
		t.Fatal(err)
	}

	rel, isNewer := update.CheckCachedUpdate(dir, "v1.1.0", 24*time.Hour)
	if !isNewer || rel == nil || rel.TagName != "v1.2.0" {
		t.Fatalf("CheckCachedUpdate gagal membaca cache valid: rel=%v isNewer=%v", rel, isNewer)
	}

	relSame, isNewerSame := update.CheckCachedUpdate(dir, "v1.2.0", 24*time.Hour)
	if isNewerSame || relSame == nil {
		t.Fatalf("seharusnya tidak newer bila versi sama: isNewer=%v", isNewerSame)
	}
}
