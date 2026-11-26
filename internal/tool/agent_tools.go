package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"renstra/internal/memory"
	"renstra/internal/skill"
	"renstra/internal/store"
)

// ---- memory tool ----

type Memory struct {
	store *memory.Store
	facts *store.Store
}

func NewMemory(s *memory.Store) *Memory { return &Memory{store: s} }

// SetFactStore attaches SQLite episodic fact storage for unlimited smart recall.
func (t *Memory) SetFactStore(fs *store.Store) { t.facts = fs }

func (t *Memory) Name() string { return "memory" }
func (t *Memory) Description() string {
	return "Mengelola catatan permanen & smart episodic memory. target=memory|user|soul. action=read|write|append|delete|remember|recall. Gunakan action=remember (tags, content, scope=global|chat) untuk fakta teknis tanpa batas ukuran, dan action=recall (query) untuk mencari pelajaran masa lalu."
}
func (t *Memory) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","enum":["memory","user","soul"]},"action":{"type":"string","enum":["read","write","append","delete","remember","recall"]},"content":{"type":"string"},"query":{"type":"string"},"tags":{"type":"string"},"scope":{"type":"string","enum":["global","chat"]}},"required":["action"]}`)
}
func (t *Memory) Danger() bool { return false }

func (t *Memory) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Target  string `json:"target"`
		Action  string `json:"action"`
		Content string `json:"content"`
		Query   string `json:"query"`
		Tags    string `json:"tags"`
		Scope   string `json:"scope"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen memory tidak valid: %w", err)
	}
	if in.Target == "" {
		in.Target = "memory"
	}

	var kind memory.Kind
	switch in.Target {
	case "memory":
		kind = memory.KindMemory
	case "user":
		kind = memory.KindUser
	case "soul":
		kind = memory.KindSoul
	default:
		return "", fmt.Errorf("target tidak dikenal: %q", in.Target)
	}

	scope := "global"
	if in.Scope == "chat" {
		if cid := ChatIDFrom(ctx); cid != 0 {
			scope = fmt.Sprintf("chat:%d", cid)
		}
	}

	switch in.Action {
	case "read":
		snap := t.store.Snapshot()
		switch kind {
		case memory.KindMemory:
			return snap.Memory, nil
		case memory.KindSoul:
			return snap.Soul, nil
		default:
			return snap.User, nil
		}
	case "write":
		if err := t.store.Write(kind, in.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("berhasil menyimpan %s (%d karakter)", in.Target, len([]rune(in.Content))), nil
	case "append":
		if err := t.store.Append(kind, in.Content); err != nil {
			return "", err
		}
		return fmt.Sprintf("berhasil menambahkan catatan ke %s", in.Target), nil
	case "delete":
		if id, err := strconv.ParseInt(strings.TrimSpace(in.Query), 10, 64); err == nil && id > 0 && t.facts != nil {
			if derr := t.facts.DeleteMemoryFact(ctx, id); derr == nil {
				return fmt.Sprintf("fakta memori #%d dihapus", id), nil
			}
		}
		q := in.Query
		if q == "" {
			q = in.Content
		}
		if _, err := t.store.DeleteEntry(kind, q); err != nil {
			return "", err
		}
		return fmt.Sprintf("berhasil menghapus entri %q dari %s", q, in.Target), nil
	case "remember":
		txt := strings.TrimSpace(in.Content)
		if txt == "" {
			txt = strings.TrimSpace(in.Query)
		}
		if t.facts != nil {
			id, err := t.facts.SaveMemoryFact(ctx, scope, in.Tags, txt)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("tersimpan ke smart memory #%d (%s)", id, scope), nil
		}
		if err := t.store.Append(kind, txt); err != nil {
			return "", err
		}
		return fmt.Sprintf("berhasil menambahkan catatan ke %s", in.Target), nil
	case "recall":
		q := strings.TrimSpace(in.Query)
		if q == "" {
			q = strings.TrimSpace(in.Content)
		}
		if t.facts == nil {
			snap := t.store.Snapshot()
			return snap.Memory, nil
		}
		facts, err := t.facts.SearchMemoryFacts(ctx, scope, q, 8)
		if err != nil {
			return "", err
		}
		if len(facts) == 0 {
			return fmt.Sprintf("tidak ada fakta smart memory yang cocok dengan %q", q), nil
		}
		var b strings.Builder
		for _, f := range facts {
			tagStr := ""
			if f.Tags != "" {
				tagStr = " [" + f.Tags + "]"
			}
			fmt.Fprintf(&b, "#%d (%s)%s: %s\n", f.ID, f.Scope, tagStr, f.Content)
		}
		return strings.TrimRight(b.String(), "\n"), nil
	default:
		return "", fmt.Errorf("action tidak dikenal: %q", in.Action)
	}
}

// ---- skill tool ----

type Skill struct{ reg *skill.Registry }

func NewSkill(r *skill.Registry) *Skill { return &Skill{reg: r} }

func (t *Skill) Name() string { return "skill" }
func (t *Skill) Description() string {
	return "Memuat, membuat, atau memperbarui skill (pengetahuan prosedural dan alur kerja berulang). action=list|load|save|delete. Gunakan action=save (name, description, body) setiap kali user mengajarkan cara baru, meminta kamu mengingat alur kerja, atau ketika kamu baru saja menyelesaikan prosedur kompleks yang kemungkinan dipakai lagi di masa depan."
}
func (t *Skill) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","load","save","create","update","delete"]},"name":{"type":"string"},"description":{"type":"string"},"body":{"type":"string"}},"required":["action"]}`)
}
func (t *Skill) Danger() bool { return false }

func (t *Skill) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Action      string `json:"action"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Body        string `json:"body"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen skill tidak valid: %w", err)
	}

	switch in.Action {
	case "list":
		return t.reg.Index(), nil
	case "load":
		s, ok := t.reg.Get(in.Name)
		if !ok {
			return "", fmt.Errorf("skill %q tidak ditemukan", in.Name)
		}
		return fmt.Sprintf("# %s\n\n%s", s.Name, s.Body), nil
	case "create":
		if in.Name == "" {
			return "", fmt.Errorf("name wajib untuk create")
		}
		if err := t.reg.Create(in.Name, in.Description, in.Body); err != nil {
			return "", err
		}
		return fmt.Sprintf("skill %q dibuat", in.Name), nil
	case "update":
		if in.Name == "" {
			return "", fmt.Errorf("name wajib untuk update")
		}
		if err := t.reg.Update(in.Name, in.Description, in.Body); err != nil {
			return "", err
		}
		return fmt.Sprintf("skill %q diperbarui", in.Name), nil
	case "save":
		// Upsert: perbarui bila sudah ada, buat baru bila belum. Ini yang
		// dipanggil agent ketika diminta mengingat cara baru.
		if in.Name == "" {
			return "", fmt.Errorf("name wajib untuk save")
		}
		if _, exists := t.reg.Get(in.Name); exists {
			if err := t.reg.Update(in.Name, in.Description, in.Body); err != nil {
				return "", err
			}
			return fmt.Sprintf("skill %q diperbarui", in.Name), nil
		}
		if err := t.reg.Create(in.Name, in.Description, in.Body); err != nil {
			return "", err
		}
		return fmt.Sprintf("skill baru %q berhasil disimpan", in.Name), nil
	case "delete":
		if in.Name == "" {
			return "", fmt.Errorf("name wajib untuk delete")
		}
		if err := t.reg.Delete(in.Name); err != nil {
			return "", err
		}
		return fmt.Sprintf("skill %q dihapus", in.Name), nil
	default:
		return "", fmt.Errorf("action tidak dikenal: %q", in.Action)
	}
}
