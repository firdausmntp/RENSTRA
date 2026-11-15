package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"unicode/utf8"

	"renstra/internal/provider"
)

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Danger() bool
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

type Registry struct {
	tools map[string]Tool
}

func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: map[string]Tool{}}
	for _, t := range tools {
		r.tools[t.Name()] = t
	}
	return r
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

func (r *Registry) Defs() []provider.ToolDef {
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)

	defs := make([]provider.ToolDef, 0, len(names))
	for _, n := range names {
		t := r.tools[n]
		defs = append(defs, provider.ToolDef{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	return defs
}

// Truncate caps tool output at limit bytes without splitting a rune; the rest
// goes to <spillDir>/.tool-output/<name>.txt and the path is reported back.
func Truncate(s string, limit int, spillDir, name string) (string, error) {
	if len(s) <= limit {
		return s, nil
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(s[:cut]) {
		cut--
	}
	rest := s[cut:]

	dir := filepath.Join(spillDir, ".tool-output")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".txt")
	if err := os.WriteFile(path, []byte(rest), 0o644); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n[truncated: %d bytes at %s]", s[:cut], len(rest), path), nil
}
