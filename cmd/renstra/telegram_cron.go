package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"renstra/internal/cron"
	"renstra/internal/store"
)

func (h *telegramHandler) handleCronCmd(ctx context.Context, chatID int64, arg string) (string, error) {
	sub := "list"
	rest := ""
	if arg != "" {
		parts := strings.SplitN(arg, " ", 2)
		sub = strings.ToLower(parts[0])
		if len(parts) > 1 {
			rest = strings.TrimSpace(parts[1])
		}
	}

	switch sub {
	case "list":
		jobs, err := h.store.ListJobs(ctx, chatID)
		if err != nil {
			return "", err
		}
		if len(jobs) == 0 {
			return "(tidak ada jadwal cron aktif)", nil
		}
		var b strings.Builder
		b.WriteString("Daftar jadwal cron:\n")
		for _, j := range jobs {
			status := "aktif"
			if !j.Enabled {
				status = "nonaktif"
			}
			fmt.Fprintf(&b, "- ID %d [%s]: %s -> %q (next: %s)\n", j.ID, status, j.Schedule, j.Prompt, j.NextRunAt)
		}
		return strings.TrimRight(b.String(), "\n"), nil

	case "add":
		parts := strings.SplitN(rest, "|", 2)
		if len(parts) != 2 {
			return "Format salah. Gunakan:\n`/cron add <schedule> | <prompt>`\nContoh: `/cron add 0 9 * * * | ringkas berita`", nil
		}
		sched := strings.TrimSpace(parts[0])
		prompt := strings.TrimSpace(parts[1])
		if sched == "" || prompt == "" {
			return "Jadwal dan prompt tidak boleh kosong.", nil
		}
		next, err := cron.NextAfter(sched, time.Now(), time.Local)
		if err != nil {
			return "", fmt.Errorf("jadwal cron tidak valid: %w", err)
		}
		id, err := h.store.AddJob(ctx, chatID, sched, prompt)
		if err != nil {
			return "", err
		}
		_ = h.store.UpdateJobRun(ctx, id, next.Format(time.RFC3339), "")
		return fmt.Sprintf("Jadwal cron ditambahkan (ID %d): `%s`\nBerikutnya: %s", id, sched, next.Format(time.RFC3339)), nil

	case "rm", "remove":
		id, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil || id <= 0 {
			return "Pemakaian: `/cron rm <id>`", nil
		}
		if err := h.store.RemoveJob(ctx, id); err != nil {
			return "", err
		}
		return fmt.Sprintf("Jadwal ID %d berhasil dihapus.", id), nil

	case "toggle", "pause":
		id, err := strconv.ParseInt(strings.TrimSpace(rest), 10, 64)
		if err != nil || id <= 0 {
			return "Pemakaian: `/cron toggle <id>`", nil
		}
		jobs, err := h.store.ListJobs(ctx, chatID)
		if err != nil {
			return "", err
		}
		var target *store.Job
		for i := range jobs {
			if jobs[i].ID == id {
				target = &jobs[i]
				break
			}
		}
		if target == nil {
			return "", fmt.Errorf("jadwal ID %d tidak ditemukan", id)
		}
		newState := !target.Enabled
		if err := h.store.SetJobEnabled(ctx, id, newState); err != nil {
			return "", err
		}
		if newState {
			return fmt.Sprintf("Jadwal ID %d diaktifkan.", id), nil
		}
		return fmt.Sprintf("Jadwal ID %d dinonaktifkan.", id), nil

	default:
		return "Subcommand cron: `/cron list`, `/cron add <schedule> | <prompt>`, `/cron toggle <id>`, `/cron rm <id>`", nil
	}
}

func (h *telegramHandler) Approve(ctx context.Context, chatID int64, toolName, args string) (bool, error) {
	timeout := h.approvalTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return h.bot.AskApproval(ctx, chatID, toolName, args, timeout)
}
