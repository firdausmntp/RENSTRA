package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/store"
)

func TestLogHubCaptureFilterAndMask(t *testing.T) {
	var out bytes.Buffer
	hub := NewLogHub(t.TempDir(), 50, &out)
	logger := slog.New(hub)

	logger.Info("gateway_ready", "port", 8080)
	logger.Error("provider_fail", "key", "sk-secret-1234567890abcdef")

	errs, warns, buf := hub.Counts()
	if errs != 1 || warns != 0 || buf != 2 {
		t.Fatalf("unexpected counts: err=%d warn=%d buf=%d", errs, warns, buf)
	}
	entries := hub.Query("ERROR", "provider_fail", 10)
	if len(entries) != 1 {
		t.Fatalf("expected 1 error entry, got %d", len(entries))
	}
	if strings.Contains(out.String(), "1234567890abcdef") {
		t.Fatalf("secret leaked in log output: %s", out.String())
	}
}

func TestDashboardServerEndpointsAndAuth(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mem, err := memory.Open(filepath.Join(dir, "memory"))
	if err != nil {
		t.Fatal(err)
	}
	skills, err := skill.Load(skill.Layer{Dir: filepath.Join(dir, "skills"), Writable: true})
	if err != nil {
		t.Fatal(err)
	}
	hub := NewLogHub(filepath.Join(dir, "logs"), 100, nil)
	slog.New(hub).Warn("test_warn", "chat_id", 42)

	srv, err := New(Options{
		Addr:            "127.0.0.1:0",
		Token:           "dash-secret",
		Version:         "v1.3.0",
		HomeDir:         dir,
		Store:           st,
		Memory:          mem,
		Skills:          skills,
		Config:          &config.Config{DefaultModel: "openai:gpt-4o"},
		LogHub:          hub,
		GetActiveModel:  func() string { return "gpt-4o" },
		GetProviderKind: func() string { return "openai" },
	})
	if err != nil {
		t.Fatal(err)
	}

	// Unauthenticated API call must return 401.
	req := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", rec.Code)
	}

	// Authenticated overview call.
	req = httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	req.Header.Set("Authorization", "Bearer dash-secret")
	rec = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d: %s", rec.Code, rec.Body.String())
	}
	var ov map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ov); err != nil {
		t.Fatal(err)
	}
	if ov["version"] != "v1.3.0" || ov["active_model"] != "gpt-4o" {
		t.Fatalf("unexpected overview payload: %+v", ov)
	}

	// Create skill via API.
	body := `{"mode":"create","name":"hello-dash","description":"Skill dari dashboard","body":"Langkah 1: cek."}`
	req = httptest.NewRequest(http.MethodPost, "/api/skills/save", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer dash-secret")
	rec = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("skill save failed: %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := skills.Get("hello-dash"); !ok {
		t.Fatal("skill hello-dash should exist in registry after save")
	}

	// Verify SPA route fallback serves index.html on /skills and /cron.
	for _, route := range []string{"/", "/skills", "/cron", "/logs"} {
		req = httptest.NewRequest(http.MethodGet, route, nil)
		rec = httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "RENSTRA") {
			t.Fatalf("SPA route %s failed: %d", route, rec.Code)
		}
	}

	// Verify Cron Create & Update CRUD.
	cronBody := `{"chat_id":1,"schedule":"0 9 * * *","prompt":"cek server"}`
	req = httptest.NewRequest(http.MethodPost, "/api/cron/save", strings.NewReader(cronBody))
	req.Header.Set("Authorization", "Bearer dash-secret")
	rec = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cron save failed: %d %s", rec.Code, rec.Body.String())
	}
	_ = context.Background()
}

func TestResolveUIFSLocalAndCacheOverride(t *testing.T) {
	home := t.TempDir()
	cacheDir := filepath.Join(home, "dashboard-cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(cacheDir, "index.html"), []byte("<html>synced</html>"), 0o644)
	_ = os.WriteFile(filepath.Join(cacheDir, "manifest.json"), []byte(`{"ui_version":"1.3.0-beta"}`), 0o644)

	_, src, ver := ResolveUIFS(home, uiFS)
	if src != "synced" || ver != "1.3.0-beta" {
		t.Fatalf("expected synced 1.3.0-beta, got src=%q ver=%q", src, ver)
	}

	localDir := filepath.Join(home, "dashboard-ui")
	if err := os.MkdirAll(localDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(localDir, "index.html"), []byte("<html>local</html>"), 0o644)
	_, src2, _ := ResolveUIFS(home, uiFS)
	if src2 != "local" {
		t.Fatalf("expected local override, got %q", src2)
	}
}

func TestChatPlaygroundAndBackupEndpoint(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var mcpReloads int
	srv, err := New(Options{
		Addr:    "127.0.0.1:0",
		HomeDir: home,
		Store:   st,
		ReloadMCP: func() {
			mcpReloads++
		},
		RunChat: func(_ context.Context, chatID int64, prompt string) (string, error) {
			return "echo:" + prompt, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(`{"chat_id":7,"prompt":"halo"}`))
	rec := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "echo:halo") {
		t.Fatalf("POST /api/chat failed: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/mcp/save", strings.NewReader(`{"name":"pw","command":"npx","args":"-y @playwright/mcp"}`))
	rec = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || mcpReloads != 1 {
		t.Fatalf("POST /api/mcp/save failed: %d reloads=%d", rec.Code, mcpReloads)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/backup", nil)
	rec = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || rec.Body.Len() == 0 {
		t.Fatalf("GET /api/backup failed: %d len=%d", rec.Code, rec.Body.Len())
	}
	backupBytes := rec.Body.Bytes()

	req = httptest.NewRequest(http.MethodPost, "/api/restore", strings.NewReader(string(backupBytes)))
	rec = httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || mcpReloads < 2 {
		t.Fatalf("POST /api/restore failed: %d %s", rec.Code, rec.Body.String())
	}
}
