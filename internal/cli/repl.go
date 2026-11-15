package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"renstra/internal/agent"
)

const helpText = `/new      mulai sesi baru
/help     tampilkan bantuan ini
/exit     keluar
ctrl+c    batalkan turn yang sedang berjalan
selain itu dikirim ke agent`

// Run reads lines from in until EOF or /exit. Ctrl+C cancels the running turn
// instead of killing the process.
func Run(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer) error {
	if r.Out == nil {
		r.Out = out
	}
	reader := bufio.NewReader(in)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	for {
		line, err := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line != "" {
			switch line {
			case "/exit", "/quit":
				return nil
			case "/new", "/reset":
				if err := r.Store.SessionBreak(ctx, chatID); err != nil {
					return err
				}
				r.InvalidatePrompt()
				fmt.Fprintln(out, "sesi baru")
			case "/help":
				fmt.Fprintln(out, helpText)
			default:
				if !runTurn(ctx, r, chatID, line, sigCh, out) {
					return nil
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// runTurn runs one turn in the background so Ctrl+C can cancel it. It returns
// false when the caller should stop the REPL.
func runTurn(ctx context.Context, r *agent.Runner, chatID int64, line string, sigCh <-chan os.Signal, out io.Writer) bool {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := r.Run(turnCtx, chatID, line)
		done <- err
	}()

	for {
		select {
		case err := <-done:
			switch {
			case errors.Is(err, context.Canceled):
				fmt.Fprintln(out, "\n[dibatalkan]")
			case err != nil:
				fmt.Fprintf(out, "\nerror: %v\n", err)
			default:
				fmt.Fprintln(out)
			}
			return true
		case <-sigCh:
			cancel()
			<-done
			fmt.Fprintln(out, "\n[dibatalkan]")
			return true
		case <-ctx.Done():
			cancel()
			<-done
			return false
		}
	}
}
