// Package migrate imports configuration, memory, skills, MCP servers, and
// conversation history from an existing Hermes Agent home (~/.hermes) into
// RENSTRA ($RENSTRA_HOME).
package migrate

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	_ "modernc.org/sqlite"

	"renstra/internal/memory"
	"renstra/internal/provider"
	"renstra/internal/store"
)

// Options controls a Hermes-to-RENSTRA migration run.
type Options struct {
	FromDir string // Source Hermes home (defaults to $HERMES_HOME or ~/.hermes)
	ToDir   string // Destination RENSTRA home
	DryRun  bool   // Report actions without modifying destination files/DB
	Force   bool   // Overwrite existing config.yaml, mcp.yaml, memory, and skills
}

// Report summarizes what was migrated, skipped, or warned about.
type Report struct {
	SourceDir       string
	DestDir         string
	DryRun          bool
	ConfigWritten   bool
	MCPWritten      bool
	MCPServers      int
	MemoryCopied    bool
	UserCopied      bool
	SkillsCopied    int
	SkillsSkipped   int
	SessionsCopied  int
	SessionsSkipped int
	MessagesCopied  int
	EnvVarsFound    []string
	Warnings        []string
	SkippedItems    []string
}

// ResolveHermesHome returns the explicit dir, $HERMES_HOME, or ~/.hermes.
func ResolveHermesHome(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	if v := strings.TrimSpace(os.Getenv("HERMES_HOME")); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("menentukan home directory user: %w", err)
	}
	return filepath.Join(home, ".hermes"), nil
}

// FromHermes migrates data from a Hermes Agent directory into a RENSTRA home.
func FromHermes(ctx context.Context, opts Options) (*Report, error) {
	src, err := ResolveHermesHome(opts.FromDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(src)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("direktori sumber Hermes tidak ditemukan: %s", src)
	}
	if strings.TrimSpace(opts.ToDir) == "" {
		return nil, errors.New("direktori tujuan RENSTRA kosong")
	}

	rep := &Report{
		SourceDir: src,
		DestDir:   opts.ToDir,
		DryRun:    opts.DryRun,
	}

	if !opts.DryRun {
		if err := os.MkdirAll(opts.ToDir, 0o700); err != nil {
			return nil, fmt.Errorf("membuat direktori tujuan %s: %w", opts.ToDir, err)
		}
	}

	dotEnv := parseDotEnv(filepath.Join(src, ".env"))
	for k := range dotEnv {
		if strings.HasSuffix(k, "_API_KEY") || k == "OPENROUTER_API_KEY" {
			rep.EnvVarsFound = append(rep.EnvVarsFound, k)
		}
	}
	sort.Strings(rep.EnvVarsFound)

	if err := migrateMemory(src, opts, rep); err != nil {
		return rep, err
	}
	if err := migrateSkills(src, opts, rep); err != nil {
		return rep, err
	}
	if err := migrateConfigAndMCP(src, dotEnv, opts, rep); err != nil {
		return rep, err
	}
	if err := migrateStateDB(ctx, src, opts, rep); err != nil {
		return rep, err
	}

	return rep, nil
}

func migrateMemory(src string, opts Options, rep *Report) error {
	memSrc := firstExisting(filepath.Join(src, "memories", "MEMORY.md"), filepath.Join(src, "MEMORY.md"))
	userSrc := firstExisting(filepath.Join(src, "memories", "USER.md"), filepath.Join(src, "USER.md"))
	if memSrc == "" && userSrc == "" {
		return nil
	}

	var memStore *memory.Store
	if !opts.DryRun {
		var err error
		memStore, err = memory.Open(filepath.Join(opts.ToDir, "memory"))
		if err != nil {
			return fmt.Errorf("membuka memory tujuan: %w", err)
		}
	}

	copyOne := func(srcPath string, kind memory.Kind, limit int, label string) (bool, error) {
		if srcPath == "" {
			return false, nil
		}
		raw, err := os.ReadFile(srcPath)
		if err != nil {
			return false, fmt.Errorf("membaca %s: %w", srcPath, err)
		}
		content := strings.TrimSpace(string(raw))
		if content == "" {
			return false, nil
		}

		if n := utf8.RuneCountInString(content); n > limit {
			content = clipRunes(content, limit)
			rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s dipotong dari %d ke %d karakter agar sesuai batas RENSTRA", label, n, limit))
		}

		dstPath := filepath.Join(opts.ToDir, "memory", label)
		if existing, err := os.ReadFile(dstPath); err == nil && strings.TrimSpace(string(existing)) != "" && !opts.Force {
			rep.SkippedItems = append(rep.SkippedItems, fmt.Sprintf("memory/%s sudah terisi (pakai --force untuk menimpa)", label))
			return false, nil
		}
		if opts.DryRun {
			return true, nil
		}
		if err := memStore.Write(kind, content); err != nil {
			return false, err
		}
		return true, nil
	}

	var err error
	rep.MemoryCopied, err = copyOne(memSrc, memory.KindMemory, memory.MemoryLimit, "MEMORY.md")
	if err != nil {
		return err
	}
	rep.UserCopied, err = copyOne(userSrc, memory.KindUser, memory.UserLimit, "USER.md")
	return err
}

func migrateSkills(src string, opts Options, rep *Report) error {
	skillsSrc := filepath.Join(src, "skills")
	info, err := os.Stat(skillsSrc)
	if err != nil || !info.IsDir() {
		return nil
	}
	skillsDst := filepath.Join(opts.ToDir, "skills")

	seen := map[string]bool{}
	err = filepath.WalkDir(skillsSrc, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != skillsSrc {
				return filepath.SkipDir
			}
			return nil
		}

		if d.Name() == "SKILL.md" {
			skillDir := filepath.Dir(path)
			name := sanitizeSkillName(filepath.Base(skillDir))
			if name == "" || seen[name] {
				return nil
			}
			seen[name] = true

			targetDir := filepath.Join(skillsDst, name)
			if _, err := os.Stat(targetDir); err == nil && !opts.Force {
				rep.SkillsSkipped++
				return filepath.SkipDir
			}
			if !opts.DryRun {
				if err := copyDir(skillDir, targetDir); err != nil {
					return fmt.Errorf("menyalin skill %q: %w", name, err)
				}
			}
			rep.SkillsCopied++
			return filepath.SkipDir
		}

		// Top-level standalone <name>.md skill files
		if filepath.Dir(path) == skillsSrc && strings.HasSuffix(d.Name(), ".md") {
			name := sanitizeSkillName(strings.TrimSuffix(d.Name(), ".md"))
			if name == "" || seen[name] {
				return nil
			}
			seen[name] = true

			targetFile := filepath.Join(skillsDst, name, "SKILL.md")
			if _, err := os.Stat(targetFile); err == nil && !opts.Force {
				rep.SkillsSkipped++
				return nil
			}
			if !opts.DryRun {
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Dir(targetFile), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(targetFile, data, 0o644); err != nil {
					return err
				}
			}
			rep.SkillsCopied++
		}
		return nil
	})
	return err
}

type hermesConfig struct {
	Model struct {
		Provider string `yaml:"provider"`
		Default  string `yaml:"default"`
		BaseURL  string `yaml:"base_url"`
	} `yaml:"model"`
	Agent struct {
		MaxTurns         int     `yaml:"max_turns"`
		SystemPrompt     string  `yaml:"system_prompt"`
		CompactThreshold float64 `yaml:"compact_threshold"`
	} `yaml:"agent"`
	MCPServers map[string]struct {
		Command string            `yaml:"command"`
		Args    []string          `yaml:"args"`
		Env     map[string]string `yaml:"env"`
		URL     string            `yaml:"url"`
		Headers map[string]string `yaml:"headers"`
	} `yaml:"mcp_servers"`
	Platforms struct {
		Telegram struct {
			Enabled      bool    `yaml:"enabled"`
			Token        string  `yaml:"token"`
			AllowedUsers []int64 `yaml:"allowed_users"`
			Extra        struct {
				DMTopics []struct {
					ChatID int64 `yaml:"chat_id"`
				} `yaml:"dm_topics"`
			} `yaml:"extra"`
		} `yaml:"telegram"`
	} `yaml:"platforms"`
}

func migrateConfigAndMCP(src string, dotEnv map[string]string, opts Options, rep *Report) error {
	cfgSrc := filepath.Join(src, "config.yaml")
	raw, err := os.ReadFile(cfgSrc)
	hasConfig := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("membaca %s: %w", cfgSrc, err)
	}

	var hc hermesConfig
	if hasConfig {
		if err := yaml.Unmarshal(raw, &hc); err != nil {
			return fmt.Errorf("parse %s: %w", cfgSrc, err)
		}
	}

	soulText := ""
	if soulRaw, err := os.ReadFile(filepath.Join(src, "SOUL.md")); err == nil {
		soulText = strings.TrimSpace(string(soulRaw))
	}

	// Migrate config.yaml if Hermes config, SOUL.md, or Telegram token exists
	tgToken := strings.TrimSpace(hc.Platforms.Telegram.Token)
	if tgToken == "" {
		tgToken = strings.TrimSpace(dotEnv["TELEGRAM_BOT_TOKEN"])
	}
	if hasConfig || soulText != "" || tgToken != "" {
		cfgDst := filepath.Join(opts.ToDir, "config.yaml")
		if _, err := os.Stat(cfgDst); err == nil && !opts.Force {
			rep.SkippedItems = append(rep.SkippedItems, "config.yaml sudah ada (pakai --force untuk menimpa)")
		} else {
			kind, baseURL, apiKeyEnv := mapHermesProvider(hc.Model.Provider, hc.Model.BaseURL)
			modelName := strings.TrimSpace(hc.Model.Default)
			if modelName == "" {
				modelName = defaultModelForKind(kind)
			}
			provName := kind
			if provName == "" {
				provName = "utama"
			}

			maxIters := hc.Agent.MaxTurns
			if maxIters <= 0 {
				maxIters = 40
			}
			threshold := hc.Agent.CompactThreshold
			if threshold <= 0 {
				threshold = 0.7
			}
			prefix := soulText
			if prefix == "" {
				prefix = strings.TrimSpace(hc.Agent.SystemPrompt)
			}

			allowed := collectTelegramUsers(hc, dotEnv)

			doc := map[string]any{
				"default_model": provName + ":" + modelName,
				"providers": map[string]any{
					provName: map[string]any{
						"kind":          kind,
						"base_url":      baseURL,
						"api_key_env":   apiKeyEnv,
						"default_model": modelName,
					},
				},
				"aliases": map[string]any{},
				"approval": map[string]any{
					"mode":    "prompt",
					"timeout": "120s",
				},
				"telegram": map[string]any{
					"token":         tgToken,
					"allowed_users": allowed,
					"poll_timeout":  "30s",
				},
				"agent": map[string]any{
					"system_prompt_prefix": prefix,
					"max_iterations":       maxIters,
					"max_turn_timeout":     "15m",
					"auto_compact":         true,
					"compact_threshold":    threshold,
				},
				"limits": map[string]any{
					"warn_daily_usd":   1.0,
					"max_daily_usd":    0,
					"max_daily_tokens": 0,
				},
			}

			if !opts.DryRun {
				outYAML, err := yaml.Marshal(doc)
				if err != nil {
					return err
				}
				if err := os.WriteFile(cfgDst, outYAML, 0o600); err != nil {
					return fmt.Errorf("menulis %s: %w", cfgDst, err)
				}
			}
			rep.ConfigWritten = true
		}
	}

	// Migrate mcp_servers -> mcp.yaml
	if len(hc.MCPServers) > 0 {
		mcpDst := filepath.Join(opts.ToDir, "mcp.yaml")
		if _, err := os.Stat(mcpDst); err == nil && !opts.Force {
			rep.SkippedItems = append(rep.SkippedItems, "mcp.yaml sudah ada (pakai --force untuk menimpa)")
		} else {
			type outMCPServer struct {
				Command string            `yaml:"command,omitempty"`
				Args    []string          `yaml:"args,omitempty"`
				Env     []string          `yaml:"env,omitempty"`
				URL     string            `yaml:"url,omitempty"`
				Headers map[string]string `yaml:"headers,omitempty"`
			}
			outDoc := struct {
				Servers map[string]outMCPServer `yaml:"servers"`
			}{
				Servers: map[string]outMCPServer{},
			}
			for name, s := range hc.MCPServers {
				var envSlice []string
				keys := make([]string, 0, len(s.Env))
				for k := range s.Env {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					val := expandEnvPlaceholders(s.Env[k], dotEnv)
					envSlice = append(envSlice, k+"="+val)
				}
				headers := map[string]string{}
				for hk, hv := range s.Headers {
					headers[hk] = expandEnvPlaceholders(hv, dotEnv)
				}
				outDoc.Servers[name] = outMCPServer{
					Command: s.Command,
					Args:    s.Args,
					Env:     envSlice,
					URL:     s.URL,
					Headers: headers,
				}
			}
			if !opts.DryRun {
				outYAML, err := yaml.Marshal(outDoc)
				if err != nil {
					return err
				}
				if err := os.WriteFile(mcpDst, outYAML, 0o600); err != nil {
					return fmt.Errorf("menulis %s: %w", mcpDst, err)
				}
			}
			rep.MCPWritten = true
			rep.MCPServers = len(outDoc.Servers)
		}
	}

	return nil
}

func migrateStateDB(ctx context.Context, src string, opts Options, rep *Report) error {
	dbSrc := filepath.Join(src, "state.db")
	if _, err := os.Stat(dbSrc); errors.Is(err, os.ErrNotExist) {
		return nil
	}

	dsn := "file:" + filepath.ToSlash(dbSrc) + "?mode=ro&_pragma=busy_timeout(3000)"
	srcDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("membuka state.db Hermes: %w", err)
	}
	defer srcDB.Close()

	if !hasTable(ctx, srcDB, "sessions") || !hasTable(ctx, srcDB, "messages") {
		rep.Warnings = append(rep.Warnings, "state.db Hermes tidak memiliki tabel sessions/messages; dilewati")
		return nil
	}

	sessCols := tableColumns(ctx, srcDB, "sessions")
	msgCols := tableColumns(ctx, srcDB, "messages")

	type hermesSession struct {
		ID       string
		Platform string
		UserID   string
	}
	sessQuery := buildSessionQuery(sessCols)
	sRows, err := srcDB.QueryContext(ctx, sessQuery)
	if err != nil {
		return fmt.Errorf("membaca tabel sessions Hermes: %w", err)
	}
	var sessions []hermesSession
	for sRows.Next() {
		var hs hermesSession
		if err := sRows.Scan(&hs.ID, &hs.Platform, &hs.UserID); err != nil {
			continue
		}
		if hs.ID != "" {
			sessions = append(sessions, hs)
		}
	}
	sRows.Close()

	var dstStore *store.Store
	if !opts.DryRun {
		dstStore, err = store.Open(filepath.Join(opts.ToDir, "state.db"))
		if err != nil {
			return fmt.Errorf("membuka state.db tujuan: %w", err)
		}
		defer dstStore.Close()
	}

	msgQuery := buildMessageQuery(msgCols)
	for _, sess := range sessions {
		kvKey := "migrated_hermes_session:" + sess.ID
		if !opts.DryRun && !opts.Force {
			var existing string
			if err := dstStore.DB().QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, kvKey).Scan(&existing); err == nil {
				rep.SessionsSkipped++
				continue
			}
		}

		mRows, err := srcDB.QueryContext(ctx, msgQuery, sess.ID)
		if err != nil {
			continue
		}

		var msgs []provider.Message
		for mRows.Next() {
			var role, content, toolCallsRaw, toolCallID string
			if err := mRows.Scan(&role, &content, &toolCallsRaw, &toolCallID); err != nil {
				continue
			}
			r := normalizeRole(role)
			if r == "" {
				continue
			}
			m := provider.Message{
				Role:       r,
				Content:    content,
				ToolCallID: toolCallID,
			}
			if toolCallsRaw != "" && toolCallsRaw != "null" && toolCallsRaw != "[]" {
				_ = json.Unmarshal([]byte(toolCallsRaw), &m.ToolCalls)
			}
			msgs = append(msgs, m)
		}
		mRows.Close()

		if len(msgs) == 0 {
			continue
		}

		chatID := mapChatID(sess.Platform, sess.UserID)
		if !opts.DryRun {
			existingMsgs, _ := dstStore.Messages(ctx, chatID)
			if len(existingMsgs) > 0 {
				_ = dstStore.SessionBreak(ctx, chatID)
			}
			for _, m := range msgs {
				if _, err := dstStore.AppendMessage(ctx, chatID, m); err != nil {
					return fmt.Errorf("menyimpan pesan sesi %s: %w", sess.ID, err)
				}
			}
			_, _ = dstStore.DB().ExecContext(ctx, `INSERT OR REPLACE INTO kv(key, value) VALUES(?, '1')`, kvKey)
		}
		rep.SessionsCopied++
		rep.MessagesCopied += len(msgs)
	}

	return nil
}

func mapHermesProvider(rawKind, baseURL string) (kind, base, envKey string) {
	k := strings.ToLower(strings.TrimSpace(rawKind))
	switch k {
	case "", "openai":
		kind, base, envKey = "openai", "https://api.openai.com/v1", "OPENAI_API_KEY"
	case "anthropic":
		kind, base, envKey = "anthropic", "https://api.anthropic.com", "ANTHROPIC_API_KEY"
	case "openrouter":
		kind, base, envKey = "openrouter", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"
	case "groq":
		kind, base, envKey = "groq", "https://api.groq.com/openai/v1", "GROQ_API_KEY"
	case "ollama":
		kind, base, envKey = "ollama", "http://localhost:11434/v1", ""
	case "mistral":
		kind, base, envKey = "mistral", "https://api.mistral.ai/v1", "MISTRAL_API_KEY"
	case "xai":
		kind, base, envKey = "xai", "https://api.x.ai/v1", "XAI_API_KEY"
	case "google", "gemini":
		kind, base, envKey = "google", "https://generativelanguage.googleapis.com", "GEMINI_API_KEY"
	default:
		kind, base, envKey = "custom", "https://api.openai.com/v1", "OPENAI_API_KEY"
	}
	if strings.TrimSpace(baseURL) != "" {
		base = strings.TrimSpace(baseURL)
	}
	return kind, base, envKey
}

func defaultModelForKind(kind string) string {
	switch kind {
	case "anthropic":
		return "claude-sonnet-4-5"
	case "google":
		return "gemini-2.5-flash"
	case "openrouter":
		return "anthropic/claude-sonnet-4-5"
	case "groq":
		return "llama-3.3-70b-versatile"
	case "ollama":
		return "qwen2.5:14b"
	default:
		return "gpt-4o-mini"
	}
}

func collectTelegramUsers(hc hermesConfig, dotEnv map[string]string) []int64 {
	seen := map[int64]bool{}
	var out []int64
	add := func(id int64) {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range hc.Platforms.Telegram.AllowedUsers {
		add(id)
	}
	for _, t := range hc.Platforms.Telegram.Extra.DMTopics {
		add(t.ChatID)
	}
	for _, envKey := range []string{"TELEGRAM_ALLOWED_USERS", "TELEGRAM_GROUP_ALLOWED_USERS"} {
		for _, part := range strings.Split(dotEnv[envKey], ",") {
			part = strings.TrimSpace(part)
			if id, err := strconv.ParseInt(part, 10, 64); err == nil {
				add(id)
			}
		}
	}
	return out
}

func parseDotEnv(path string) map[string]string {
	out := map[string]string{}
	raw, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && ((v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '\'' && v[len(v)-1] == '\'')) {
			v = v[1 : len(v)-1]
		}
		if k != "" {
			out[k] = v
		}
	}
	return out
}

var envPlaceholderRE = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnvPlaceholders(s string, dotEnv map[string]string) string {
	return envPlaceholderRE.ReplaceAllStringFunc(s, func(m string) string {
		sub := envPlaceholderRE.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		if v, ok := dotEnv[sub[1]]; ok && v != "" {
			return v
		}
		if v := os.Getenv(sub[1]); v != "" {
			return v
		}
		return m
	})
}

func sanitizeSkillName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		if r == ' ' {
			return '-'
		}
		return -1
	}, name)
	return strings.Trim(name, ".-")
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func clipRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit])
}

func hasTable(ctx context.Context, db *sql.DB, name string) bool {
	var c int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&c); err != nil {
		return false
	}
	return c > 0
}

func tableColumns(ctx context.Context, db *sql.DB, table string) map[string]bool {
	cols := map[string]bool{}
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return cols
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err == nil {
			cols[strings.ToLower(name)] = true
		}
	}
	return cols
}

func buildSessionQuery(cols map[string]bool) string {
	idCol := "''"
	if cols["session_id"] {
		idCol = "COALESCE(session_id,'')"
	} else if cols["id"] {
		idCol = "CAST(id AS TEXT)"
	}
	platCol := "''"
	if cols["source_platform"] {
		platCol = "COALESCE(source_platform,'')"
	} else if cols["platform"] {
		platCol = "COALESCE(platform,'')"
	}
	userCol := "''"
	if cols["user_id"] {
		userCol = "COALESCE(CAST(user_id AS TEXT),'')"
	}
	orderCol := "rowid"
	if cols["started_at"] {
		orderCol = "started_at, rowid"
	}
	return fmt.Sprintf("SELECT %s, %s, %s FROM sessions ORDER BY %s", idCol, platCol, userCol, orderCol)
}

func buildMessageQuery(cols map[string]bool) string {
	roleCol := "COALESCE(role,'')"
	contentCol := "COALESCE(content,'')"
	callsCol := "''"
	if cols["tool_calls_json"] {
		callsCol = "COALESCE(tool_calls_json,'')"
	} else if cols["tool_calls"] {
		callsCol = "COALESCE(tool_calls,'')"
	}
	callIDCol := "''"
	if cols["tool_call_id"] {
		callIDCol = "COALESCE(tool_call_id,'')"
	}
	orderCol := "rowid"
	if cols["timestamp"] {
		orderCol = "timestamp, rowid"
	} else if cols["created_at"] {
		orderCol = "created_at, rowid"
	}
	return fmt.Sprintf("SELECT %s, %s, %s, %s FROM messages WHERE session_id = ? ORDER BY %s",
		roleCol, contentCol, callsCol, callIDCol, orderCol)
}

func normalizeRole(r string) provider.Role {
	switch strings.ToLower(strings.TrimSpace(r)) {
	case "user":
		return provider.RoleUser
	case "assistant", "model":
		return provider.RoleAssistant
	case "tool":
		return provider.RoleTool
	case "system":
		return provider.RoleSystem
	default:
		return ""
	}
}

func mapChatID(platform, userID string) int64 {
	if strings.EqualFold(strings.TrimSpace(platform), "telegram") {
		if id, err := strconv.ParseInt(strings.TrimSpace(userID), 10, 64); err == nil && id != 0 {
			return id
		}
	}
	return 1
}
