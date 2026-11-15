package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

type SubagentRunner interface {
	RunSubagent(ctx context.Context, task string) (string, error)
}

type Delegate struct {
	runner SubagentRunner
	depth  int
}

func NewDelegate(runner SubagentRunner, depth int) *Delegate {
	return &Delegate{runner: runner, depth: depth}
}

func (t *Delegate) Name() string { return "delegate" }
func (t *Delegate) Description() string {
	return "Mendelegasikan sub-tugas ke agent anak yang bekerja di konteks terisolasi. Hanya mengembalikan hasil akhir. Jangan gunakan bila tugas bisa diselesaikan sendiri dalam 1-2 tool."
}
func (t *Delegate) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task":{"type":"string","description":"instruksi lengkap dan spesifik untuk subagent"}},"required":["task"]}`)
}
func (t *Delegate) Danger() bool { return false }

func (t *Delegate) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if t.depth >= 2 {
		return "", errors.New("batas kedalaman subagent (2) tercapai; selesaikan langsung")
	}
	var in struct {
		Task string `json:"task"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen delegate tidak valid: %w", err)
	}
	if in.Task == "" {
		return "", errors.New("task tidak boleh kosong")
	}
	res, err := t.runner.RunSubagent(ctx, in.Task)
	if err != nil {
		return "", fmt.Errorf("subagent gagal: %w", err)
	}
	return res, nil
}
