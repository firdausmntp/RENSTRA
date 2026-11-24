package dashboard

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"renstra/internal/config"
	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/store"
)

//go:embed ui
var uiFS embed.FS

// FallbackStatusItem mirrors circuit-breaker status without importing internal/agent.
type FallbackStatusItem struct {
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	Failures      int    `json:"failures"`
	CooldownLeft  string `json:"cooldown_left,omitempty"`
	LastError     string `json:"last_error,omitempty"`
	LastKind      string `json:"last_kind,omitempty"`
	ContextWindow int    `json:"context_window"`
	Vision        bool   `json:"vision"`
}

// Options wires runtime stores and callbacks into the web dashboard server.
type Options struct {
	Addr               string
	Token              string
	Version            string
	HomeDir            string
	Store              *store.Store
	Memory             *memory.Store
	Skills             *skill.Registry
	Config             *config.Config
	LogHub             *LogHub
	StartedAt          time.Time
	GetActiveModel     func() string
	GetProviderKind    func() string
	GetContextWindow   func() int
	GetFallbackStatus  func() []FallbackStatusItem
	ResetFallbacks     func()
	InvalidatePrompts  func()
	SetDefaultModelRun func(ref string) error
	ReloadConfig       func(cfg *config.Config)
	ReloadMCP          func()
	RunChat            func(ctx context.Context, chatID int64, prompt string) (string, error)
}

// Server serves the embedded or hot-synced RENSTRA web dashboard and REST/SSE APIs.
type Server struct {
	opts       Options
	http       *http.Server
	embeddedUI fs.FS
	uiMu       sync.RWMutex
	uiRoot     fs.FS
	uiSource   string
	uiVersion  string
}

// New creates a configured dashboard Server.
func New(opts Options) (*Server, error) {
	if opts.StartedAt.IsZero() {
		opts.StartedAt = time.Now()
	}
	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		return nil, err
	}
	s := &Server{opts: opts, embeddedUI: sub}
	s.reloadUIFS()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/overview", s.withAuth(s.handleOverview))
	mux.HandleFunc("/api/logs", s.withAuth(s.handleLogs))
	mux.HandleFunc("/api/logs/stream", s.withAuth(s.handleLogStream))
	mux.HandleFunc("/api/logs/download", s.withAuth(s.handleLogsDownload))
	mux.HandleFunc("/api/logs/clear", s.withAuth(s.handleLogsClear))
	mux.HandleFunc("/api/turns", s.withAuth(s.handleTurns))
	mux.HandleFunc("/api/turns/delete", s.withAuth(s.handleTurnDelete))
	mux.HandleFunc("/api/sessions", s.withAuth(s.handleSessions))
	mux.HandleFunc("/api/sessions/messages", s.withAuth(s.handleSessionMessages))
	mux.HandleFunc("/api/sessions/clear", s.withAuth(s.handleSessionClear))
	mux.HandleFunc("/api/cron", s.withAuth(s.handleCronList))
	mux.HandleFunc("/api/cron/save", s.withAuth(s.handleCronSave))
	mux.HandleFunc("/api/cron/toggle", s.withAuth(s.handleCronToggle))
	mux.HandleFunc("/api/cron/delete", s.withAuth(s.handleCronDelete))
	mux.HandleFunc("/api/skills", s.withAuth(s.handleSkillsList))
	mux.HandleFunc("/api/skills/save", s.withAuth(s.handleSkillSave))
	mux.HandleFunc("/api/skills/delete", s.withAuth(s.handleSkillDelete))
	mux.HandleFunc("/api/skills/reload", s.withAuth(s.handleSkillsReload))
	mux.HandleFunc("/api/memory", s.withAuth(s.handleMemory))
	mux.HandleFunc("/api/accounts", s.withAuth(s.handleAccounts))
	mux.HandleFunc("/api/accounts/delete", s.withAuth(s.handleAccountDelete))
	mux.HandleFunc("/api/models/add", s.withAuth(s.handleModelAdd))
	mux.HandleFunc("/api/models/delete", s.withAuth(s.handleModelDelete))
	mux.HandleFunc("/api/model/default", s.withAuth(s.handleModelDefault))
	mux.HandleFunc("/api/fallback/reset", s.withAuth(s.handleFallbackReset))
	mux.HandleFunc("/api/ui/sync", s.withAuth(s.handleUISync))
	mux.HandleFunc("/api/ui/reset", s.withAuth(s.handleUIReset))
	mux.HandleFunc("/api/chat", s.withAuth(s.handleChat))
	mux.HandleFunc("/api/backup", s.withAuth(s.handleBackup))
	mux.HandleFunc("/api/restore", s.withAuth(s.handleRestore))
	mux.HandleFunc("/api/mcp", s.withAuth(s.handleMCPList))
	mux.HandleFunc("/api/mcp/save", s.withAuth(s.handleMCPSave))
	mux.HandleFunc("/api/mcp/delete", s.withAuth(s.handleMCPDelete))
	mux.Handle("/", s.handleUI())

	s.http = &http.Server{
		Addr:              opts.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s, nil
}

// Run starts the HTTP server and shuts it down cleanly when ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.http.Shutdown(shutCtx)
		return nil
	case err := <-errCh:
		return err
	}
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimSpace(s.opts.Token)
		if tok != "" {
			got := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if got == "" {
				got = strings.TrimSpace(r.URL.Query().Get("token"))
			}
			if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) != 1 {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized: dashboard token required"})
				return
			}
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(dst)
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now()
	today := now.Format("2006-01-02")

	var dayU, weekU, monthU store.Usage
	var daily []store.DailyUsage
	if s.opts.Store != nil {
		dayU, _ = s.opts.Store.UsageForDay(ctx, today)
		weekU, _ = s.opts.Store.UsageSince(ctx, now.AddDate(0, 0, -7))
		monthU, _ = s.opts.Store.UsageSince(ctx, now.AddDate(0, 0, -30))
		daily, _ = s.opts.Store.UsageHistory(ctx, 14)
	}
	var errCnt, warnCnt int64
	var buffered int
	if s.opts.LogHub != nil {
		errCnt, warnCnt, buffered = s.opts.LogHub.Counts()
	}
	activeModel := ""
	if s.opts.GetActiveModel != nil {
		activeModel = s.opts.GetActiveModel()
	}
	provKind := ""
	if s.opts.GetProviderKind != nil {
		provKind = s.opts.GetProviderKind()
	}
	ctxWin := 0
	if s.opts.GetContextWindow != nil {
		ctxWin = s.opts.GetContextWindow()
	}
	var fallbacks []FallbackStatusItem
	if s.opts.GetFallbackStatus != nil {
		fallbacks = s.opts.GetFallbackStatus()
	}
	skillsCount := 0
	if s.opts.Skills != nil {
		skillsCount = len(s.opts.Skills.Names())
	}

	_, uiSrc, uiVer := s.activeUIFS()
	writeJSON(w, http.StatusOK, map[string]any{
		"version":        s.opts.Version,
		"api_version":    CurrentAPIVersion,
		"ui_source":      uiSrc,
		"ui_version":     uiVer,
		"uptime_sec":     int64(time.Since(s.opts.StartedAt).Seconds()),
		"started_at":     s.opts.StartedAt.Format(time.RFC3339),
		"active_model":   activeModel,
		"provider":       provKind,
		"context_window": ctxWin,
		"skills_count":   skillsCount,
		"error_count":    errCnt,
		"warn_count":     warnCnt,
		"log_buffered":   buffered,
		"usage_today":    dayU,
		"usage_7d":       weekU,
		"usage_30d":      monthU,
		"daily_usage":    daily,
		"fallbacks":      fallbacks,
		"auth_required":  strings.TrimSpace(s.opts.Token) != "",
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if s.opts.LogHub == nil {
		writeJSON(w, http.StatusOK, map[string]any{"entries": []LogEntry{}})
		return
	}
	level := r.URL.Query().Get("level")
	q := r.URL.Query().Get("q")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 250
	}
	entries := s.opts.LogHub.Query(level, q, limit)
	errCnt, warnCnt, buffered := s.opts.LogHub.Counts()
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":     entries,
		"error_count": errCnt,
		"warn_count":  warnCnt,
		"buffered":    buffered,
	})
}

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	if s.opts.LogHub == nil {
		http.Error(w, "log hub unavailable", http.StatusServiceUnavailable)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.opts.LogHub.Subscribe()
	defer unsub()

	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	keep := time.NewTicker(20 * time.Second)
	defer keep.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case entry := <-ch:
			b, _ := json.Marshal(entry)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-keep.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func (s *Server) handleLogsClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if s.opts.LogHub != nil {
		if err := s.opts.LogHub.Clear(); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogsDownload(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="renstra-logs.jsonl"`)
	if s.opts.LogHub == nil {
		return
	}
	entries := s.opts.LogHub.Query("ALL", "", 1000)
	for i := len(entries) - 1; i >= 0; i-- {
		b, err := json.Marshal(entries[i])
		if err == nil {
			_, _ = w.Write(append(b, '\n'))
		}
	}
}
