package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// maxPreviewBytes bounds the approval prompt. Hidden bytes are stated
// explicitly so a destructive tail cannot hide behind a benign prefix.
const maxPreviewBytes = 4000

type streamPauseKey struct{}

type streamPauser struct {
	pause  func()
	resume func()
}

func withStreamPause(ctx context.Context, pause, resume func()) context.Context {
	return context.WithValue(ctx, streamPauseKey{}, streamPauser{pause: pause, resume: resume})
}

// Approver asks y/N on the terminal. There is no timer: the only person who
// can answer is the one looking at it. The gateway gets its own approver with
// a timeout in Plan 2.
func Approver(in io.Reader, out io.Writer) func(ctx context.Context, name string, args json.RawMessage) (bool, error) {
	return ApproverFrom(bufio.NewReader(in), out)
}

// ApproverFrom shares the REPL's reader so a piped or pasted "y" is not eaten
// by the line scanner's read-ahead.
func ApproverFrom(reader *bufio.Reader, out io.Writer) func(ctx context.Context, name string, args json.RawMessage) (bool, error) {
	return func(ctx context.Context, name string, args json.RawMessage) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if p, ok := ctx.Value(streamPauseKey{}).(streamPauser); ok {
			if p.pause != nil {
				p.pause()
			}
			if p.resume != nil {
				defer p.resume()
			}
		}

		body := formatCLIApprovalArgs(name, args)
		fmt.Fprintf(out, "\n%s╭─ 🔐 Konfirmasi Eksekusi: %s%s%s\n", cYellow, cWhite, name, cReset)
		if body != "" {
			for _, ln := range strings.Split(body, "\n") {
				fmt.Fprintf(out, "%s│%s  %s\n", cYellow, cReset, ln)
			}
		}
		fmt.Fprintf(out, "%s╰─ Setujui? [y/N] %s", cYellow, cReset)

		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return false, nil
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "ya", "yes":
			return true, nil
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
}

func formatCLIApprovalArgs(name string, args json.RawMessage) string {
	raw := strings.TrimSpace(string(args))
	if raw == "" {
		return ""
	}
	if name == "terminal" {
		var payload struct {
			Cmd string `json:"cmd"`
		}
		if json.Unmarshal(args, &payload) == nil && strings.TrimSpace(payload.Cmd) != "" {
			return preview(strings.TrimSpace(payload.Cmd))
		}
	}
	return preview(raw)
}

// preview keeps the whole command visible; only absurdly long arguments are
// cut, and the cut is announced.
func preview(s string) string {
	if len(s) <= maxPreviewBytes {
		return s
	}
	cut := maxPreviewBytes
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("… [%d byte disembunyikan]", len(s)-cut)
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
