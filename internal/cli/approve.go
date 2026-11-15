package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const maxPromptArgs = 300

// Approver asks y/N on the terminal. There is no timer: the only person who
// can answer is the one looking at it. The gateway gets its own approver with
// a timeout in Plan 2.
func Approver(in io.Reader, out io.Writer) func(ctx context.Context, name string, args json.RawMessage) (bool, error) {
	return func(ctx context.Context, name string, args json.RawMessage) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		preview := string(args)
		if len(preview) > maxPromptArgs {
			preview = preview[:maxPromptArgs] + "…"
		}
		fmt.Fprintf(out, "\njalankan %s? %s\n[y/N] ", name, preview)

		line, err := readLine(in)
		if err != nil {
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

// readLine reads one line byte-by-byte so it never buffers past the newline;
// the REPL scanner and the approver share the same stdin.
func readLine(in io.Reader) (string, error) {
	var b strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n > 0 {
			if buf[0] == '\n' {
				return strings.TrimRight(b.String(), "\r"), nil
			}
			b.WriteByte(buf[0])
			if b.Len() > 4096 {
				return b.String(), nil
			}
		}
		if err != nil {
			return b.String(), err
		}
	}
}
