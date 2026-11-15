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
		fmt.Fprintf(out, "\njalankan %s? %s\n[y/N] ", name, preview(string(args)))

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
