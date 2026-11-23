package telegram

import (
	"errors"
	"strings"
	"testing"
)

func TestHasMeaningfulStreamText(t *testing.T) {
	for _, raw := range []string{"", " ", ">", "**", "```", "#", "---"} {
		if hasMeaningfulStreamText(raw) {
			t.Fatalf("expected %q not to flush prematurely", raw)
		}
	}
	for _, raw := range []string{"> Halo", "OK", "**Done**"} {
		if !hasMeaningfulStreamText(raw) {
			t.Fatalf("expected %q to be meaningful", raw)
		}
	}
}

func TestFormatToolNoteAndTurnTimeoutError(t *testing.T) {
	note := formatToolNote("terminal", `{"cmd":"systemctl status renstra"}`)
	if !strings.Contains(note, "terminal") || !strings.Contains(note, "systemctl status renstra") {
		t.Fatalf("unexpected tool note: %q", note)
	}

	msg := FormatError(errors.New("turn timeout (batas 15m0s, iterasi 2) saat menjalankan tool terminal (cmd: sleep 999): context deadline exceeded"))
	if !strings.Contains(msg, "Turn / Tool Timeout") || !strings.Contains(msg, "sleep 999") {
		t.Fatalf("unexpected FormatError for turn timeout:\n%s", msg)
	}
}
