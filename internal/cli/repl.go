package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"renstra/internal/agent"
)

const helpText = `/new      mulai sesi baru
/help     tampilkan bantuan ini
/exit     keluar
selain itu dikirim ke agent`

// Run reads lines from in until EOF or /exit.
func Run(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer) error {
	if r.Out == nil {
		r.Out = out
	}
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch line {
		case "/exit", "/quit":
			return nil
		case "/new", "/reset":
			if err := r.Store.SessionBreak(ctx, chatID); err != nil {
				return err
			}
			fmt.Fprintln(out, "sesi baru")
			continue
		case "/help":
			fmt.Fprintln(out, helpText)
			continue
		}

		if _, err := r.Run(ctx, chatID, line); err != nil {
			if errors.Is(err, context.Canceled) {
				fmt.Fprintln(out, "\n[dibatalkan]")
				continue
			}
			fmt.Fprintf(out, "\nerror: %v\n", err)
			continue
		}
		fmt.Fprintln(out)
	}
	return scanner.Err()
}
