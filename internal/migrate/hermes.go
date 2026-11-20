// Package migrate imports configuration, memory, skills, MCP servers, and
// conversation history from an existing Hermes Agent home (~/.hermes) into
// RENSTRA ($RENSTRA_HOME).
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"renstra/internal/memory"
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
