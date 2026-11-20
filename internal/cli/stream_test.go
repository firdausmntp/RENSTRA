package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"renstra/internal/agent"
)

func TestBuildStatsFooterFull(t *testing.T) {
	s := &agent.TurnStats{
		TokensIn:      1200,
		TokensOut:     340,
		ToolCalls:     2,
		ContextUsed:   4800,
		ContextWindow: 32000,
		CostUSD:       0.0012,
		Elapsed:       4 * time.Second,
	}
	got := buildStatsFooter(s, 32000)
	for _, want := range []string{"⏱ 4.0s", "↑1.2k ↓340 tok", "85.0 tok/s", "2 tool", "ctx 15% (4.8k/32.0k)", "$0.0012"} {
		if !strings.Contains(got, want) {
			t.Errorf("footer missing %q: %s", want, got)
		}
	}
}

func TestBuildStatsFooterNoUsage(t *testing.T) {
	s := &agent.TurnStats{Elapsed: 250 * time.Millisecond, ContextUsed: 500, ContextWindow: 32000}
	got := buildStatsFooter(s, 32000)
	if strings.Contains(got, "tok/s") {
		t.Errorf("tanpa usage provider tidak boleh ada tok/s: %s", got)
	}
	if !strings.Contains(got, "250ms") || !strings.Contains(got, "ctx 2%") {
		t.Errorf("footer tanpa usage tidak lengkap: %s", got)
	}
}

func TestCLIStreamWriterTextAndTool(t *testing.T) {
	var buf bytes.Buffer
	w := newCLIStreamWriter(&buf, 32000, true)
	w.SetContextUsage(16000)

	if _, err := w.Write([]byte("halo")); err != nil {
		t.Fatal(err)
	}
	w.OnTool("terminal", `{"cmd":"ls"}`)
	if _, err := w.Write([]byte(" dunia\n")); err != nil {
		t.Fatal(err)
	}
	w.Finalize()

	got := buf.String()
	if !strings.Contains(got, "halo") || !strings.Contains(got, " dunia\n") {
		t.Fatalf("teks stream hilang: %q", got)
	}
	if !strings.Contains(got, "⚙ terminal") {
		t.Fatalf("status tool tidak tampil: %q", got)
	}
	if !strings.Contains(got, "ctx 50%") {
		t.Fatalf("context usage tidak tampil: %q", got)
	}
	if !strings.Contains(got, "\033[u\033[K") {
		t.Fatalf("status line tidak pernah dibersihkan: %q", got)
	}
	// Setelah Finalize, tulisan tambahan harus diabaikan.
	if _, err := w.Write([]byte("bocor")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "bocor") {
		t.Fatal("write setelah Finalize seharusnya diabaikan")
	}
}

func TestHumanTokens(t *testing.T) {
	cases := map[int]string{0: "0", 999: "999", 1000: "1.0k", 1234: "1.2k", 32000: "32.0k"}
	for n, want := range cases {
		if got := humanTokens(n); got != want {
			t.Errorf("humanTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestCLIStreamWriterNonInteractive(t *testing.T) {
	var buf bytes.Buffer
	w := newCLIStreamWriter(&buf, 32000, false)
	w.SetContextUsage(16000)
	w.Write([]byte("halo"))
	w.OnTool("terminal", `{"cmd":"ls"}`)
	w.Write([]byte(" dunia"))
	w.Finalize()
	got := buf.String()
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\r") {
		t.Fatalf("output non-interaktif harus polos tanpa ANSI/CR: %q", got)
	}
	for _, want := range []string{"halo", "⚙ terminal", " dunia"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in non-interactive output: %q", want, got)
		}
	}
	if strings.Contains(got, "ctx") || strings.Contains(got, "⏱") {
		t.Fatalf("status line tidak boleh muncul saat non-interaktif: %q", got)
	}
}

func TestPlainWriterStripsANSI(t *testing.T) {
	var buf bytes.Buffer
	p := plainWriter{&buf}
	if _, err := p.Write([]byte(cCyan + "halo" + cReset + "\r\x1b[K")); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "halo" {
		t.Fatalf("plainWriter = %q, want %q", buf.String(), "halo")
	}
}

func TestPrintBannerAlignment(t *testing.T) {
	r := &agent.Runner{Model: "gpt-4o-mini", ContextWindow: 128000, Cwd: `C:\Users\Lenovo\.renstra\workspace`}
	var buf bytes.Buffer
	PrintBanner(&buf, r, RunOptions{Version: "v1.2.0", Provider: "openai"})
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*m`)
	var width int
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		plain := ansi.ReplaceAllString(line, "")
		if plain == "" || strings.HasPrefix(plain, "💡") {
			continue
		}
		w := utf8.RuneCountInString(plain)
		if width == 0 {
			width = w
		}
		if w != width {
			t.Fatalf("lebar baris banner tidak rata (%d vs %d): %q", w, width, plain)
		}
	}
	if width == 0 {
		t.Fatal("banner kosong")
	}
}

func TestCLIStreamWriterLoadingPauseResume(t *testing.T) {
	var buf bytes.Buffer
	w := newCLIStreamWriter(&buf, 32000, true)
	w.StartLoading()
	time.Sleep(150 * time.Millisecond)
	w.Pause()
	w.Resume()
	w.Write([]byte("jawaban"))
	w.Finalize()

	got := buf.String()
	if !strings.Contains(got, "Menghubungi model...") {
		t.Fatalf("loading message tidak tampil: %q", got)
	}
	if !strings.Contains(got, "jawaban") {
		t.Fatalf("teks jawaban hilang: %q", got)
	}
}
