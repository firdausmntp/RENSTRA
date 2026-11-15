package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"renstra/internal/store"
)

type Cron struct {
	store  *store.Store
	chatID int64
}

func NewCron(s *store.Store, chatID int64) *Cron {
	return &Cron{store: s, chatID: chatID}
}

func (t *Cron) Name() string { return "cron" }
func (t *Cron) Description() string {
	return "Mengatur tugas terjadwal otomatis (cron). action=list|add|remove|toggle. schedule memakai format cron 5-field (min hour dom month dow) atau @hourly, @daily, @weekly."
}
func (t *Cron) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["list","add","remove","toggle"]},"schedule":{"type":"string"},"prompt":{"type":"string"},"id":{"type":"integer"}},"required":["action"]}`)
}
func (t *Cron) Danger() bool { return false }

func (t *Cron) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	var in struct {
		Action   string `json:"action"`
		Schedule string `json:"schedule"`
		Prompt   string `json:"prompt"`
		ID       int64  `json:"id"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen cron tidak valid: %w", err)
	}

	chatID := t.chatID
	if chatID == 0 {
		chatID = ChatIDFrom(ctx)
	}

	switch in.Action {
	case "list":
		jobs, err := t.store.ListJobs(ctx, chatID)
		if err != nil {
			return "", err
		}
		if len(jobs) == 0 {
			return "(tidak ada jadwal cron aktif)", nil
		}
		var b strings.Builder
		for _, j := range jobs {
			status := "aktif"
			if !j.Enabled {
				status = "nonaktif"
			}
			fmt.Fprintf(&b, "- ID %d [%s]: %s -> %q (next: %s)\n", j.ID, status, j.Schedule, j.Prompt, j.NextRunAt)
		}
		return b.String(), nil

	case "add":
		if in.Schedule == "" || in.Prompt == "" {
			return "", fmt.Errorf("schedule dan prompt wajib diisi untuk add")
		}
		id, err := t.store.AddJob(ctx, chatID, in.Schedule, in.Prompt)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("jadwal berhasil ditambahkan dengan ID %d (%s)", id, in.Schedule), nil

	case "remove":
		if in.ID == 0 {
			return "", fmt.Errorf("id wajib diisi untuk remove")
		}
		if err := t.store.RemoveJob(ctx, in.ID); err != nil {
			return "", err
		}
		return fmt.Sprintf("jadwal ID %d berhasil dihapus", in.ID), nil

	case "toggle":
		if in.ID == 0 {
			return "", fmt.Errorf("id wajib diisi untuk toggle")
		}
		jobs, err := t.store.ListJobs(ctx, t.chatID)
		if err != nil {
			return "", err
		}
		var target *store.Job
		for i := range jobs {
			if jobs[i].ID == in.ID {
				target = &jobs[i]
				break
			}
		}
		if target == nil {
			return "", fmt.Errorf("jadwal ID %d tidak ditemukan", in.ID)
		}
		newState := !target.Enabled
		if err := t.store.SetJobEnabled(ctx, in.ID, newState); err != nil {
			return "", err
		}
		if newState {
			return fmt.Sprintf("jadwal ID %d diaktifkan", in.ID), nil
		}
		return fmt.Sprintf("jadwal ID %d dinonaktifkan", in.ID), nil

	default:
		return "", fmt.Errorf("action tidak dikenal: %q", in.Action)
	}
}
