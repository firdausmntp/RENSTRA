package dashboard

import (
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"renstra/internal/config"
	"renstra/internal/cron"
	"renstra/internal/store"
)

func (s *Server) reloadUIFS() {
	root, src, ver := ResolveUIFS(s.opts.HomeDir, s.embeddedUI)
	s.uiMu.Lock()
	s.uiRoot = root
	s.uiSource = src
	s.uiVersion = ver
	s.uiMu.Unlock()
}

func (s *Server) activeUIFS() (fs.FS, string, string) {
	s.uiMu.RLock()
	defer s.uiMu.RUnlock()
	return s.uiRoot, s.uiSource, s.uiVersion
}

func (s *Server) handleUI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		root, _, _ := s.activeUIFS()
		w.Header().Set("Cache-Control", "no-cache")
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "favicon.ico" {
			if raw, err := fs.ReadFile(root, "logo.svg"); err == nil {
				w.Header().Set("Content-Type", "image/svg+xml")
				_, _ = w.Write(raw)
				return
			}
		}
		if clean == "" || !strings.Contains(clean, ".") {
			raw, err := fs.ReadFile(root, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(raw)
			return
		}
		http.FileServer(http.FS(root)).ServeHTTP(w, r)
	})
}

func (s *Server) handleUISync(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	repo, branch := "", ""
	if s.opts.Config != nil {
		repo = s.opts.Config.Dashboard.UIRepo
		branch = s.opts.Config.Dashboard.UIBranch
	}
	manifest, err := SyncUI(r.Context(), s.opts.HomeDir, repo, branch)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.reloadUIFS()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ui_version": manifest.UIVersion, "source": "synced"})
}

func (s *Server) handleUIReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if err := ResetSyncedUI(s.opts.HomeDir); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.reloadUIFS()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "source": "embedded"})
}

func (s *Server) handleTurns(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"turns": []store.TurnRecord{}})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	turns, err := s.opts.Store.RecentTurns(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turns": turns})
}

func (s *Server) handleTurnDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Store == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &req); err != nil || req.ID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id wajib diisi"})
		return
	}
	if err := s.opts.Store.DeleteTurn(r.Context(), req.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"sessions": []store.ChatSummary{}})
		return
	}
	sessions, err := s.opts.Store.ListChatsSummary(r.Context(), 40)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}

func (s *Server) handleSessionClear(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Store == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	var req struct {
		ChatID int64 `json:"chat_id"`
	}
	if err := readJSON(r, &req); err != nil || req.ChatID == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat_id wajib diisi"})
		return
	}
	if err := s.opts.Store.SessionBreak(r.Context(), req.ChatID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSessionMessages(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"messages": []any{}})
		return
	}
	cid, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("chat_id")), 10, 64)
	if err != nil || cid == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat_id tidak valid"})
		return
	}
	msgs, err := s.opts.Store.Messages(r.Context(), cid)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if len(msgs) > 25 {
		msgs = msgs[len(msgs)-25:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"chat_id": cid, "messages": msgs})
}

func (s *Server) handleCronList(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []store.Job{}})
		return
	}
	jobs, err := s.opts.Store.AllJobs(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (s *Server) handleCronSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Store == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	var req struct {
		ID       int64  `json:"id"`
		ChatID   int64  `json:"chat_id"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	req.Schedule = strings.TrimSpace(req.Schedule)
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Schedule == "" || req.Prompt == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "schedule dan prompt wajib diisi"})
		return
	}
	next, err := cron.NextAfter(req.Schedule, time.Now(), time.Local)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "jadwal cron tidak valid: " + err.Error()})
		return
	}
	if req.ID > 0 {
		if err := s.opts.Store.UpdateJob(r.Context(), req.ID, req.Schedule, req.Prompt); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		_ = s.opts.Store.UpdateJobRun(r.Context(), req.ID, next.Format(time.RFC3339), "")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": req.ID})
		return
	}
	if req.ChatID == 0 {
		req.ChatID = 1
	}
	id, err := s.opts.Store.AddJob(r.Context(), req.ChatID, req.Schedule, req.Prompt)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = s.opts.Store.UpdateJobRun(r.Context(), id, next.Format(time.RFC3339), "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "id": id})
}

func (s *Server) handleCronToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Store == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	var req struct {
		ID      int64 `json:"id"`
		Enabled bool  `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil || req.ID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id wajib diisi"})
		return
	}
	if err := s.opts.Store.SetJobEnabled(r.Context(), req.ID, req.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleCronDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Store == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := readJSON(r, &req); err != nil || req.ID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id wajib diisi"})
		return
	}
	if err := s.opts.Store.RemoveJob(r.Context(), req.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleModelDefault(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Ref) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ref model wajib diisi"})
		return
	}
	if s.opts.SetDefaultModelRun != nil {
		if err := s.opts.SetDefaultModelRun(req.Ref); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	if fresh, err := config.Load(); err == nil {
		s.opts.Config = fresh
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFallbackReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if s.opts.ResetFallbacks != nil {
		s.opts.ResetFallbacks()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleModelAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Model    string `json:"model"`
		Provider string `json:"provider"`
		Context  string `json:"context"`
		Vision   bool   `json:"vision"`
		Alias    string `json:"alias"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Model) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nama model wajib diisi"})
		return
	}
	cfg := s.opts.Config
	if cfg == nil {
		cfg, _ = config.Load()
	}
	parts := strings.Split(req.Model, ",")
	var added []string
	for _, rawModel := range parts {
		m := strings.TrimSpace(rawModel)
		if m == "" {
			continue
		}
		args := []string{m}
		if strings.TrimSpace(req.Provider) != "" {
			args = append(args, strings.TrimSpace(req.Provider))
		}
		if strings.TrimSpace(req.Context) != "" {
			args = append(args, strings.TrimSpace(req.Context))
		}
		if req.Vision {
			args = append(args, "vision")
		}
		if strings.TrimSpace(req.Alias) != "" && len(parts) == 1 {
			args = append(args, "alias="+strings.TrimSpace(req.Alias))
		}
		spec, err := config.ParseAddModelArgs(args, cfg)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		updated, err := config.AddModel(spec, cfg)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		cfg = updated
		added = append(added, spec.Provider+":"+spec.Model)
	}
	if len(added) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nama model wajib diisi"})
		return
	}
	s.opts.Config = cfg
	if s.opts.ReloadConfig != nil {
		s.opts.ReloadConfig(cfg)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ref": added[0], "refs": added, "count": len(added)})
}

func (s *Server) handleModelDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Ref string `json:"ref"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Ref) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ref model wajib diisi"})
		return
	}
	cfg := s.opts.Config
	if cfg == nil {
		cfg, _ = config.Load()
	}
	updated, _, err := config.RemoveModel(req.Ref, cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.opts.Config = updated
	if s.opts.ReloadConfig != nil {
		s.opts.ReloadConfig(updated)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
