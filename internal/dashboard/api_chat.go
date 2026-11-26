package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"renstra/internal/config"
	"renstra/internal/store"
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		ChatID int64  `json:"chat_id"`
		Prompt string `json:"prompt"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Prompt) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "prompt wajib diisi"})
		return
	}
	if req.ChatID == 0 {
		req.ChatID = 1
	}
	if s.opts.RunChat == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "chat runner belum aktif di mode ini"})
		return
	}
	reply, err := s.opts.RunChat(r.Context(), req.ChatID, strings.TrimSpace(req.Prompt))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"chat_id": req.ChatID,
		"reply":   reply,
	})
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil && strings.TrimSpace(s.opts.HomeDir) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store/homeDir tidak tersedia"})
		return
	}
	fname := fmt.Sprintf("renstra-backup-%s.tar.gz", time.Now().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, fname))
	_ = s.opts.Store.BackupArchive(r.Context(), s.opts.HomeDir, w)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if strings.TrimSpace(s.opts.HomeDir) == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "homeDir tidak tersedia"})
		return
	}
	var reader io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		f, _, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "berkas backup tidak ditemukan: " + err.Error()})
			return
		}
		defer f.Close()
		reader = f
	}
	files, err := store.RestoreArchive(s.opts.HomeDir, reader)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.opts.Skills != nil {
		_ = s.opts.Skills.Reload()
	}
	if s.opts.ReloadMCP != nil {
		s.opts.ReloadMCP()
	}
	if s.opts.InvalidatePrompts != nil {
		s.opts.InvalidatePrompts()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "files": files, "count": len(files)})
}

type mcpServerEntry struct {
	Command string            `yaml:"command,omitempty" json:"command,omitempty"`
	Args    []string          `yaml:"args,omitempty" json:"args,omitempty"`
	Env     []string          `yaml:"env,omitempty" json:"env,omitempty"`
	URL     string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
}

type mcpFileDoc struct {
	Servers map[string]mcpServerEntry `yaml:"servers"`
}

func (s *Server) mcpFilePath() string {
	if strings.TrimSpace(s.opts.HomeDir) == "" {
		return ""
	}
	return filepath.Join(s.opts.HomeDir, "mcp.yaml")
}

func (s *Server) readMCPDoc() mcpFileDoc {
	var doc mcpFileDoc
	p := s.mcpFilePath()
	if p != "" {
		if raw, err := os.ReadFile(p); err == nil {
			_ = yaml.Unmarshal(raw, &doc)
		}
	}
	if doc.Servers == nil {
		doc.Servers = map[string]mcpServerEntry{}
	}
	return doc
}

func (s *Server) writeMCPDoc(doc mcpFileDoc) error {
	p := s.mcpFilePath()
	if p == "" {
		return fmt.Errorf("homeDir tidak tersedia")
	}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

func (s *Server) handleMCPList(w http.ResponseWriter, _ *http.Request) {
	doc := s.readMCPDoc()
	names := make([]string, 0, len(doc.Servers))
	for k := range doc.Servers {
		names = append(names, k)
	}
	sort.Strings(names)
	type item struct {
		Name    string   `json:"name"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		URL     string   `json:"url"`
	}
	out := make([]item, 0, len(names))
	for _, n := range names {
		e := doc.Servers[n]
		out = append(out, item{Name: n, Command: e.Command, Args: e.Args, URL: e.URL})
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": out})
}

func (s *Server) handleMCPSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Name    string `json:"name"`
		Command string `json:"command"`
		Args    string `json:"args"`
		URL     string `json:"url"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nama server MCP wajib diisi"})
		return
	}
	cmd := strings.TrimSpace(req.Command)
	url := strings.TrimSpace(req.URL)
	if cmd == "" && url == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "command atau url wajib diisi"})
		return
	}
	var args []string
	if strings.TrimSpace(req.Args) != "" {
		args = strings.Fields(req.Args)
	}
	doc := s.readMCPDoc()
	doc.Servers[strings.TrimSpace(req.Name)] = mcpServerEntry{Command: cmd, Args: args, URL: url}
	if err := s.writeMCPDoc(doc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.opts.ReloadMCP != nil {
		s.opts.ReloadMCP()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMCPDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nama server MCP wajib diisi"})
		return
	}
	doc := s.readMCPDoc()
	delete(doc.Servers, strings.TrimSpace(req.Name))
	if err := s.writeMCPDoc(doc); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.opts.ReloadMCP != nil {
		s.opts.ReloadMCP()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleFallbackSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Fallbacks     string `json:"fallbacks"`
		SmartFallback *bool  `json:"smart_fallback"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	rawList := strings.FieldsFunc(req.Fallbacks, func(c rune) bool {
		return c == ',' || c == '\n' || c == '\r'
	})
	cfg := s.opts.Config
	updated, err := config.SaveFallbacks(rawList, req.SmartFallback, cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.opts.Config = updated
	if s.opts.ReloadConfig != nil {
		s.opts.ReloadConfig(updated)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "fallbacks": updated.Fallbacks, "smart_fallback": updated.SmartFallbackEnabled()})
}

func (s *Server) handleMemoryFacts(w http.ResponseWriter, r *http.Request) {
	if s.opts.Store == nil {
		writeJSON(w, http.StatusOK, map[string]any{"facts": []store.MemoryFact{}})
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	facts, err := s.opts.Store.SearchMemoryFacts(r.Context(), "", q, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"facts": facts})
}

func (s *Server) handleMemoryFactDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := s.opts.Store.DeleteMemoryFact(r.Context(), req.ID); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleServiceRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	if s.opts.RestartService == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "restart service hanya tersedia di mode serve"})
		return
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = s.opts.RestartService("Web Dashboard")
	}()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Service sedang di-restart & config baru akan dimuat..."})
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
