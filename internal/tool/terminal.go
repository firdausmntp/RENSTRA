package tool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// capOutput bounds how much a command may write before it is killed, so a
// runaway `yes` cannot exhaust memory. Truncate() runs on top of this.
const (
	capOutput        = 64 * 1024
	maxBlockingSleep = 30 * time.Second
)

var sleepCmdRe = regexp.MustCompile(`(?i)(?:^|[;&|(\s])sleep\s+(\d+(?:\.\d+)?)([smhd])?(?:\s|$|[;&|)])`)

// defaultEnvKeys are the only variables a child process inherits. Provider
// keys and RENSTRA_* never reach it.
var defaultEnvKeys = []string{
	"PATH", "HOME", "USERPROFILE", "LANG", "LC_ALL", "TZ",
	"SYSTEMROOT", "COMSPEC", "PATHEXT", "WINDIR", "TEMP", "TMP",
}

type Terminal struct {
	cwd      string
	timeout  time.Duration
	extraEnv []string
}

func NewTerminal(cwd string, timeout time.Duration) *Terminal {
	if timeout == 0 {
		timeout = 2 * time.Minute
	}
	return &Terminal{cwd: cwd, timeout: timeout}
}

// WithExtraEnv adds variable names (not values) to the allowlist.
func (t *Terminal) WithExtraEnv(names []string) *Terminal {
	t.extraEnv = append(t.extraEnv, names...)
	return t
}

func (t *Terminal) Name() string { return "terminal" }

func (t *Terminal) Description() string {
	return "Menjalankan perintah shell di mesin ini. cwd default adalah workspace. Keluaran stdout+stderr digabung dan dipotong bila terlalu besar; exit code non-nol dilaporkan sebagai teks, bukan error."
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
	if dur, blocked := detectBlockingSleep(in.Cmd); blocked {
		return fmt.Sprintf("[ditolak: perintah mengandung blocking sleep %s (> %s) yang menghabiskan waktu turn dan memicu timeout. Jangan polling dengan sleep di terminal; jalankan proses lama di background (`nohup ... > tmp/out.log 2>&1 &`) lalu laporkan PID/statusnya, atau gunakan tool `cron` untuk cek berkala.]", dur, maxBlockingSleep), nil
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
	cmd.Env = t.env()
	cmd.WaitDelay = 500 * time.Millisecond

	buf := &cappedBuffer{limit: capOutput}
	cmd.Stdout = buf
	cmd.Stderr = buf

	err := cmd.Run()
	text := strings.TrimRight(buf.String(), "\n")

	if buf.truncated {
		text += fmt.Sprintf("\n[output dipotong di %d byte; perintah dihentikan]", capOutput)
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return text + fmt.Sprintf("\n[timeout setelah %s]", t.timeout), nil
		}
		return text, ctx.Err()
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

func (t *Terminal) env() []string {
	keys := append(append([]string{}, defaultEnvKeys...), t.extraEnv...)
	env := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, k := range keys {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// cappedBuffer stops accepting bytes past limit and remembers that it did.
type cappedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := c.limit - c.buf.Len()
	if room <= 0 {
		c.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		c.buf.Write(p[:room])
		c.truncated = true
		return len(p), nil
	}
	return c.buf.Write(p)
}

func (c *cappedBuffer) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

var _ io.Writer = (*cappedBuffer)(nil)

// detectBlockingSleep catches foreground `sleep <N>` calls exceeding maxBlockingSleep
// (e.g. `sleep 180; pgrep ...`) that starve the turn timeout and bloat token loops.
// Backgrounded commands (e.g. `nohup ... &` or `(...) &`) are allowed.
func detectBlockingSleep(cmd string) (time.Duration, bool) {
	trimmed := strings.TrimSpace(cmd)
	if strings.HasSuffix(trimmed, "&") && !strings.HasSuffix(trimmed, "&&") {
		return 0, false
	}
	var maxFound time.Duration
	for _, m := range sleepCmdRe.FindAllStringSubmatch(trimmed, -1) {
		if len(m) < 2 {
			continue
		}
		val, err := strconv.ParseFloat(m[1], 64)
		if err != nil || val <= 0 {
			continue
		}
		mult := time.Second
		if len(m) >= 3 {
			switch strings.ToLower(m[2]) {
			case "m":
				mult = time.Minute
			case "h":
				mult = time.Hour
			case "d":
				mult = 24 * time.Hour
			}
		}
		d := time.Duration(val * float64(mult))
		if d > maxFound {
			maxFound = d
		}
	}
	if maxFound > maxBlockingSleep {
		return maxFound.Truncate(time.Second), true
	}
	return 0, false
}
