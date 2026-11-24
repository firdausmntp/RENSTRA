package dashboard

import (
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"

	"renstra/internal/config"
	"renstra/internal/memory"
)

type skillDTO struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Path        string `json:"path"`
	Dir         string `json:"dir"`
	Writable    bool   `json:"writable"`
}

func (s *Server) handleSkillsList(w http.ResponseWriter, _ *http.Request) {
	if s.opts.Skills == nil {
		writeJSON(w, http.StatusOK, map[string]any{"skills": []skillDTO{}, "conflicts": []any{}})
		return
	}
	names := s.opts.Skills.Names()
	out := make([]skillDTO, 0, len(names))
	for _, n := range names {
		sk, ok := s.opts.Skills.Get(n)
		if !ok {
			continue
		}
		writable := s.opts.HomeDir != "" && strings.HasPrefix(sk.Dir, s.opts.HomeDir)
		out = append(out, skillDTO{
			Name:        sk.Name,
			Description: sk.Description,
			Body:        sk.Body,
			Path:        sk.Path,
			Dir:         sk.Dir,
			Writable:    writable,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"skills":    out,
		"conflicts": s.opts.Skills.Conflicts(),
	})
}

func (s *Server) handleSkillSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Skills == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
		Mode        string `json:"mode"` // "create" or "update"
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var err error
	if _, exists := s.opts.Skills.Get(req.Name); exists && req.Mode != "create" {
		err = s.opts.Skills.Update(req.Name, req.Description, req.Body)
	} else {
		err = s.opts.Skills.Create(req.Name, req.Description, req.Body)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.opts.InvalidatePrompts != nil {
		s.opts.InvalidatePrompts()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Skills == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name wajib diisi"})
		return
	}
	if err := s.opts.Skills.Delete(req.Name); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.opts.InvalidatePrompts != nil {
		s.opts.InvalidatePrompts()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSkillsReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || s.opts.Skills == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}
	if err := s.opts.Skills.Reload(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.opts.InvalidatePrompts != nil {
		s.opts.InvalidatePrompts()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(s.opts.Skills.Names())})
}

func (s *Server) handleMemory(w http.ResponseWriter, r *http.Request) {
	if s.opts.Memory == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory store tidak aktif"})
		return
	}
	if r.Method == http.MethodGet {
		snap := s.opts.Memory.Snapshot()
		memLim, userLim, soulLim := s.opts.Memory.AllLimits()
		writeJSON(w, http.StatusOK, map[string]any{
			"memory":       snap.Memory,
			"user":         snap.User,
			"soul":         snap.Soul,
			"memory_chars": utf8.RuneCountInString(snap.Memory),
			"user_chars":   utf8.RuneCountInString(snap.User),
			"soul_chars":   utf8.RuneCountInString(snap.Soul),
			"memory_limit": memLim,
			"user_limit":   userLim,
			"soul_limit":   soulLim,
		})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST required"})
		return
	}
	var req struct {
		Target string `json:"target"` // "memory", "user", "soul", "reset_soul", "limit"
		Text   string `json:"text"`
		Limit  int    `json:"limit"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var err error
	switch req.Target {
	case "memory":
		err = s.opts.Memory.Write(memory.KindMemory, req.Text)
	case "user":
		err = s.opts.Memory.Write(memory.KindUser, req.Text)
	case "soul":
		err = s.opts.Memory.Write(memory.KindSoul, req.Text)
	case "reset_soul":
		err = s.opts.Memory.ResetSoul()
	case "limit":
		s.opts.Memory.SetLimits(req.Limit, req.Limit, 0)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target tidak valid"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if s.opts.InvalidatePrompts != nil {
		s.opts.InvalidatePrompts()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	cfg := s.opts.Config
	if cfg == nil {
		cfg, _ = config.Load()
	}
	if r.Method == http.MethodGet {
		accounts := config.ListAccounts(cfg)
		var models []string
		type modelItem struct {
			Ref       string `json:"ref"`
			Provider  string `json:"provider"`
			Model     string `json:"model"`
			Context   int    `json:"context"`
			MaxOutput int    `json:"max_output"`
			Vision    bool   `json:"vision"`
		}
		var details []modelItem
		seen := map[string]bool{}
		addModel := func(pn, mn string, pc config.ProviderConfig) {
			if mn == "" {
				return
			}
			ref := pn + ":" + mn
			if seen[ref] {
				return
			}
			seen[ref] = true
			mc := pc.ModelConfig(mn)
			models = append(models, ref)
			details = append(details, modelItem{
				Ref: ref, Provider: pn, Model: mn,
				Context: mc.Context, MaxOutput: mc.MaxOutput, Vision: mc.Vision,
			})
		}
		if cfg != nil {
			pnames := make([]string, 0, len(cfg.Providers))
			for pn := range cfg.Providers {
				pnames = append(pnames, pn)
			}
			sort.Strings(pnames)
			for _, pn := range pnames {
				pc := cfg.Providers[pn]
				addModel(pn, pc.DefaultModel, pc)
				mnames := make([]string, 0, len(pc.Models))
				for mn := range pc.Models {
					mnames = append(mnames, mn)
				}
				sort.Strings(mnames)
				for _, mn := range mnames {
					addModel(pn, mn, pc)
				}
			}
			anames := make([]string, 0, len(cfg.Aliases))
			for a := range cfg.Aliases {
				anames = append(anames, a)
			}
			sort.Strings(anames)
			for _, a := range anames {
				if !seen[a] {
					seen[a] = true
					models = append(models, a)
				}
			}
			if cfg.DefaultModel != "" && !seen[cfg.DefaultModel] {
				if pcfg, mname, rerr := config.Resolve(cfg.DefaultModel, cfg); rerr == nil {
					mc := pcfg.ModelConfig(mname)
					seen[cfg.DefaultModel] = true
					models = append(models, cfg.DefaultModel)
					details = append(details, modelItem{
						Ref: cfg.DefaultModel, Provider: pcfg.Kind, Model: mname,
						Context: mc.Context, MaxOutput: mc.MaxOutput, Vision: mc.Vision,
					})
				} else {
					models = append(models, cfg.DefaultModel)
				}
			}
			for _, fb := range cfg.Fallbacks {
				fb = strings.TrimSpace(fb)
				if fb == "" || seen[fb] {
					continue
				}
				if pcfg, mname, rerr := config.Resolve(fb, cfg); rerr == nil {
					pn := fb
					if i := strings.Index(pn, ":"); i > 0 {
						pn = pn[:i]
					}
					addModel(pn, mname, pcfg)
				}
			}
		}
		defModel := ""
		if cfg != nil {
			defModel = cfg.DefaultModel
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"accounts":      accounts,
			"default_model": defModel,
			"models":        models,
			"model_details": details,
		})
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST required"})
		return
	}
	var req struct {
		Name         string `json:"name"`
		Kind         string `json:"kind"`
		APIKey       string `json:"api_key"`
		DefaultModel string `json:"default_model"`
		BaseURL      string `json:"base_url"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated, info, err := config.UpsertAccount(req.Name, req.Kind, req.APIKey, req.DefaultModel, req.BaseURL, cfg)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.opts.Config = updated
	if s.opts.ReloadConfig != nil {
		s.opts.ReloadConfig(updated)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "account": info})
}

func (s *Server) handleAccountDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST required"})
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	updated, err := config.RemoveAccount(req.Name, s.opts.Config)
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
