package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"renstra/internal/memory"
	"renstra/internal/skill"
)

// ---- memory tool ----

type Memory struct{ store *memory.Store }

func NewMemory(s *memory.Store) *Memory { return &Memory{store: s} }

func (t *Memory) Name() string { return "memory" }
func (t *Memory) Description() string {
	return "Mengelola catatan permanen lintas sesi. target=memory untuk catatanmu (MEMORY.md), target=user untuk profil user (USER.md), target=soul untuk prinsip/identitas inti (SOUL.md). action=read|write. Batas karakter: 2200 (memory), 1375 (user), 4000 (soul)."
}
func (t *Memory) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"target":{"type":"string","enum":["memory","user","soul"]},"action":{"type":"string","enum":["read","write"]},"content":{"type":"string"}},"required":["target","action"]}`)
}
func (t *Memory) Danger() bool { return false }

func (t *Memory) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Target  string `json:"target"`
		Action  string `json:"action"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen memory tidak valid: %w", err)
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
