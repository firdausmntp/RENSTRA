package cli

import (
	"io"
	"os"
	"regexp"

	"github.com/mattn/go-isatty"
)

// Exported ANSI color tokens for CLI subcommands.
const (
	ColorReset         = cReset
	ColorDim           = cDim
	ColorCyan          = cCyan
	ColorBrightCyan    = cBrightCyan
	ColorMagenta       = cMagenta
	ColorBrightMagenta = cBrightMagenta
	ColorYellow        = cYellow
	ColorGreen         = cGreen
	ColorBlue          = cBlue
	ColorRed           = cRed
	ColorWhite         = cWhite
)

// isTTY reports whether w is an interactive terminal. Pipes, files, and
// buffers are non-interactive: they get plain text without ANSI colors or
// live status lines, so redirected output stays readable.
func isTTY(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	if isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd()) {
		enableVT(f.Fd())
		return true
	}
	return os.Getenv("FORCE_COLOR") != ""
}

// Writer wraps w so ANSI color escapes pass through on interactive terminals
// (with Windows VT processing enabled) and are cleanly stripped when piped.
func Writer(w io.Writer) io.Writer {
	return plainIfNotTTY(w)
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
