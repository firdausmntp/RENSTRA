package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
