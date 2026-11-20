package cli

import (
	"io"
	"os"
	"regexp"

	"github.com/mattn/go-isatty"
)

// isTTY reports whether w is an interactive terminal. Pipes, files, and
// buffers are non-interactive: they get plain text without ANSI colors or
// live status lines, so redirected output stays readable.
func isTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[A-Za-z]|\r")

// plainWriter strips ANSI escape sequences and carriage returns so colored
// output renders as clean text when piped or redirected.
type plainWriter struct{ w io.Writer }

func (p plainWriter) Write(b []byte) (int, error) {
	clean := ansiRE.ReplaceAll(b, nil)
	if len(clean) == 0 {
		return len(b), nil
	}
	if _, err := p.w.Write(clean); err != nil {
		return 0, err
	}
	return len(b), nil
}

// plainIfNotTTY wraps w for non-terminal destinations.
func plainIfNotTTY(w io.Writer) io.Writer {
	if isTTY(w) {
		return w
	}
	return plainWriter{w}
}
