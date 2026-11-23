package dashboard

import (
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"time"

	"renstra/internal/cron"
	"renstra/internal/store"
)

func (s *Server) handleUI() http.Handler {
	fileSrv := http.FileServer(http.FS(s.uiRoot))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" || !strings.Contains(clean, ".") {
			raw, err := fs.ReadFile(s.uiRoot, "index.html")
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(raw)
			return
		}
		fileSrv.ServeHTTP(w, r)
	})
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
