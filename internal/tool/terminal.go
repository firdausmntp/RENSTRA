package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type Terminal struct {
	cwd     string
	timeout time.Duration
}

func NewTerminal(cwd string, timeout time.Duration) *Terminal {
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	return &Terminal{cwd: cwd, timeout: timeout}
}

func (t *Terminal) Name() string { return "terminal" }

func (t *Terminal) Description() string {
	return "Menjalankan perintah shell di mesin ini. cwd default adalah workspace. Keluaran stdout+stderr digabung; exit code non-nol dilaporkan sebagai teks, bukan error."
}

func (t *Terminal) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"cmd":{"type":"string","description":"perintah shell"}},"required":["cmd"]}`)
}

func (t *Terminal) Danger() bool { return true }

func (t *Terminal) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Cmd string `json:"cmd"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", fmt.Errorf("argumen terminal tidak valid: %w", err)
	}
	if strings.TrimSpace(in.Cmd) == "" {
		return "", errors.New("cmd kosong")
	}

	ctx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", in.Cmd)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", in.Cmd)
	}
	cmd.Dir = t.cwd

	out, err := cmd.CombinedOutput()
	text := strings.TrimRight(string(out), "\n")

	if ctx.Err() != nil {
		return text + fmt.Sprintf("\n[timeout setelah %s]", t.timeout), nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return text + fmt.Sprintf("\n[exit code %d]", exitErr.ExitCode()), nil
	}
	if err != nil {
		return text, fmt.Errorf("gagal menjalankan perintah: %w", err)
	}
	return text, nil
}
