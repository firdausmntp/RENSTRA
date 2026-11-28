package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
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

// chatIDKey lets per-chat tools (cron) know which chat they are running for.
type chatIDKey struct{}

func WithChatID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, chatIDKey{}, id)
}

func ChatIDFrom(ctx context.Context) int64 {
	id, _ := ctx.Value(chatIDKey{}).(int64)
	return id
}

type deliveryKey struct{}

type deliveryTracker struct {
	mu    sync.Mutex
	chats map[int64]bool
	texts map[int64][]string
}

// WithDeliveryTracker attaches a per-turn map tracking which chatIDs already
// received a direct message via tools like send_telegram.
func WithDeliveryTracker(ctx context.Context) context.Context {
	return context.WithValue(ctx, deliveryKey{}, &deliveryTracker{
		chats: map[int64]bool{},
		texts: map[int64][]string{},
	})
}

// MarkChatDelivered records that chatID received a tool-delivered message in this turn.
func MarkChatDelivered(ctx context.Context, chatID int64) {
	MarkChatDeliveredText(ctx, chatID, "")
}

// MarkChatDeliveredText records that chatID received a direct message along with
// its text so the runner can suppress duplicate final replies with identical content.
func MarkChatDeliveredText(ctx context.Context, chatID int64, text string) {
	if dt, ok := ctx.Value(deliveryKey{}).(*deliveryTracker); ok && dt != nil && chatID != 0 {
		dt.mu.Lock()
		dt.chats[chatID] = true
		if norm := normalizeDeliveredText(text); norm != "" {
			dt.texts[chatID] = append(dt.texts[chatID], norm)
		}
		dt.mu.Unlock()
	}
}

// WasChatDelivered reports whether chatID already received a direct message via tool in this turn.
func WasChatDelivered(ctx context.Context, chatID int64) bool {
	if dt, ok := ctx.Value(deliveryKey{}).(*deliveryTracker); ok && dt != nil && chatID != 0 {
		dt.mu.Lock()
		defer dt.mu.Unlock()
		return dt.chats[chatID]
	}
	return false
}

// IsDuplicateDelivery reports whether candidate text substantially duplicates a
// message already sent to chatID via a direct delivery tool in the same turn.
func IsDuplicateDelivery(ctx context.Context, chatID int64, candidate string) bool {
	if chatID == 0 {
		return false
	}
	cand := normalizeDeliveredText(candidate)
	if cand == "" {
		return false
	}
	dt, ok := ctx.Value(deliveryKey{}).(*deliveryTracker)
	if !ok || dt == nil {
		return false
	}
	dt.mu.Lock()
	defer dt.mu.Unlock()
	for _, sent := range dt.texts[chatID] {
		if sent == cand {
			return true
		}
		if len(cand) >= 40 && len(sent) >= 40 {
			if strings.Contains(sent, cand) || strings.Contains(cand, sent) {
				shorter, longer := len(cand), len(sent)
				if shorter > longer {
					shorter, longer = longer, shorter
				}
				if float64(shorter)/float64(longer) >= 0.72 {
					return true
				}
			}
			if wordOverlapRatio(sent, cand) >= 0.80 {
				return true
			}
		}
	}
	return false
}

func normalizeDeliveredText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

func wordOverlapRatio(a, b string) float64 {
	wa := strings.Fields(a)
	wb := strings.Fields(b)
	if len(wa) == 0 || len(wb) == 0 {
		return 0
	}
	setA := make(map[string]struct{}, len(wa))
	for _, w := range wa {
		setA[w] = struct{}{}
	}
	setB := make(map[string]struct{}, len(wb))
	for _, w := range wb {
		setB[w] = struct{}{}
	}
	inter := 0
	for w := range setA {
		if _, ok := setB[w]; ok {
			inter++
		}
	}
	union := len(setA) + len(setB) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
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

func (r *Registry) Add(t Tool) {
	r.tools[t.Name()] = t
}

// RemovePrefix removes all registered tools whose name starts with prefix (e.g. "mcp__").
func (r *Registry) RemovePrefix(prefix string) {
	for k := range r.tools {
		if strings.HasPrefix(k, prefix) {
			delete(r.tools, k)
		}
	}
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Clone returns a shallow copy of the registry so a subagent can override or
// restrict tools (such as delegate depth) without mutating the parent.
func (r *Registry) Clone() *Registry {
	cp := &Registry{tools: make(map[string]Tool, len(r.tools))}
	for k, v := range r.tools {
		cp.tools[k] = v
	}
	return cp
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
// goes to <spillDir>/.tool-output/<sanitized name>.txt and the path is reported
// back. Invalid UTF-8 in the kept prefix is replaced, not used as a cut point.
func Truncate(s string, limit int, spillDir, name string) (string, error) {
	if len(s) <= limit {
		return s, nil
	}
	cut := limit
	for i := 0; i < utf8.UTFMax && cut > 0 && !utf8.ValidString(s[:cut]); i++ {
		cut--
	}
	kept := strings.ToValidUTF8(s[:cut], "\uFFFD")
	rest := s[cut:]

	dir := filepath.Join(spillDir, ".tool-output")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return kept, nil
	}
	path := filepath.Join(dir, sanitize(name)+".txt")
	if err := os.WriteFile(path, []byte(rest), 0o644); err != nil {
		return kept, nil
	}
	return fmt.Sprintf("%s\n[truncated: %d bytes at %s]", kept, len(rest), path), nil
}

// sanitize keeps spill file names inside .tool-output on every OS.
func sanitize(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '_'
		}
		if r < 0x20 {
			return '_'
		}
		return r
	}, name)
	cleaned = strings.Trim(cleaned, ".")
	if cleaned == "" {
		cleaned = "output"
	}
	if len(cleaned) > 64 {
		cleaned = cleaned[:64]
	}
	return cleaned
}

// PruneOldFiles menghapus file reguler di bawah dir yang mtime-nya lebih tua dari maxAge.
func PruneOldFiles(dir string, maxAge time.Duration) (int, error) {
	if maxAge <= 0 || dir == "" {
		return 0, nil
	}
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if rerr := os.Remove(path); rerr == nil {
				removed++
			}
		}
		return nil
	})
	if os.IsNotExist(err) {
		return 0, nil
	}
	return removed, err
}
