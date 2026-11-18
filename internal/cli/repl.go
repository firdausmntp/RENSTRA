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
	"time"
	"unicode/utf8"

	"renstra/internal/agent"
)

const helpText = `/new      mulai sesi baru
/compact  ringkas riwayat percakapan
/usage    lihat pemakaian token & biaya hari ini
/model    lihat atau ganti model (/model <nama>)
/skills   daftar skill terdaftar (/skills reload)
/memory   lihat memori & profil tersimpan
/help     tampilkan bantuan ini
/exit     keluar
ctrl+c    batalkan turn yang sedang berjalan
selain itu dikirim ke agent`

// RunOptions holds optional hooks for interactive CLI commands.
type RunOptions struct {
	SwitchModel func(ref string) (string, error)
}

// Run reads lines from in until EOF or /exit. Ctrl+C cancels the running turn
// instead of killing the process.
func Run(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer) error {
	return RunWithOptions(ctx, r, chatID, in, out, RunOptions{})
}

// RunWithOptions reads lines from in until EOF or /exit with optional command hooks.
func RunWithOptions(ctx context.Context, r *agent.Runner, chatID int64, in io.Reader, out io.Writer, opts RunOptions) error {
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
			if handleCommand(ctx, r, chatID, line, out, opts) {
				if line == "/exit" || line == "/quit" {
					return nil
				}
			} else if !runTurn(ctx, r, chatID, line, sigCh, out) {
				return nil
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

func handleCommand(ctx context.Context, r *agent.Runner, chatID int64, line string, out io.Writer, opts RunOptions) bool {
	if !strings.HasPrefix(line, "/") {
		return false
	}
	parts := strings.SplitN(line, " ", 2)
	cmd := strings.ToLower(parts[0])
	arg := ""
	if len(parts) > 1 {
		arg = strings.TrimSpace(parts[1])
	}

	switch cmd {
	case "/exit", "/quit":
		return true
	case "/new", "/reset":
		if err := r.Store.SessionBreak(ctx, chatID); err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return true
		}
		r.InvalidatePrompt()
		fmt.Fprintln(out, "sesi baru")
		return true
	case "/help":
		fmt.Fprintln(out, helpText)
		return true
	case "/usage":
		today := time.Now().Format("2006-01-02")
		u, err := r.Store.UsageForDay(ctx, today)
		if err != nil {
			fmt.Fprintf(out, "error: %v\n", err)
			return true
		}
		fmt.Fprintf(out, "Penggunaan hari ini (%s): in=%d out=%d biaya=$%.4f\n", today, u.TokensIn, u.TokensOut, u.CostUSD)
		return true
	case "/compact":
		summary, err := r.ForceCompact(ctx, chatID)
		if err != nil {
			fmt.Fprintf(out, "gagal kompaksi: %v\n", err)
			return true
		}
		fmt.Fprintf(out, "Kompaksi selesai:\n%s\n", summary)
		return true
	case "/skills":
		if r.Skills == nil {
			fmt.Fprintln(out, "Skill registry tidak aktif.")
			return true
		}
		if strings.EqualFold(arg, "reload") {
			r.InvalidatePrompt()
			fmt.Fprintln(out, "Skill dimuat ulang dari disk.")
		}
		idx := strings.TrimSpace(r.Skills.Index())
		if idx == "" {
			fmt.Fprintln(out, "(belum ada skill terdaftar)")
		} else {
			fmt.Fprintln(out, idx)
		}
		return true
	case "/memory":
		if r.Memory == nil {
			fmt.Fprintln(out, "Memory store tidak aktif.")
			return true
		}
		snap := r.Memory.Snapshot()
		memLim, userLim := r.Memory.Limits()
		fmt.Fprintf(out, "MEMORY (%d/%d):\n%s\n\nUSER (%d/%d):\n%s\n",
			utf8.RuneCountInString(snap.Memory), memLim, strings.TrimSpace(snap.Memory),
			utf8.RuneCountInString(snap.User), userLim, strings.TrimSpace(snap.User))
		return true
	case "/model":
		if arg == "" {
			fmt.Fprintf(out, "Model aktif: %s\n", r.GetModel())
			return true
		}
		if opts.SwitchModel != nil {
			msg, err := opts.SwitchModel(arg)
			if err != nil {
				fmt.Fprintf(out, "gagal ganti model: %v\n", err)
				return true
			}
			fmt.Fprintln(out, msg)
			return true
		}
		r.SetModel(nil, arg, 0, 0, 0, 0)
		fmt.Fprintf(out, "Model aktif diganti ke: %s\n", arg)
		return true
	default:
		return false
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
