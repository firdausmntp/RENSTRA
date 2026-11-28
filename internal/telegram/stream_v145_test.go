package telegram

import (
	"strings"
	"testing"
)

func TestStreamWriterResetDedupeAndStatusPreservation(t *testing.T) {
	sw := NewStreamWriter(nil, 123)
	sw.SetStep(1, 80)
	_, _ = sw.Write([]byte("Langkah pertama.\nLangkah pertama.\nDetail awal."))
	sw.ResetStepText()
	_, _ = sw.Write([]byte("Percobaan ulang bersih."))

	sw.mu.Lock()
	got := sw.renderLocked()
	sw.mu.Unlock()
	if strings.Contains(got, "Langkah pertama") {
		t.Fatalf("expected ResetStepText to clear prior attempt text, got: %s", got)
	}
	if !strings.Contains(got, "Percobaan ulang bersih.") {
		t.Fatalf("expected retry text present, got: %s", got)
	}

	// Next iteration clears old narration and keeps status block visible even with long text.
	sw.SetStep(2, 80)
	sw.draftFailed = true
	_, _ = sw.Write([]byte(strings.Repeat("Baris panjang sekali untuk uji batas 4096 karakter.\n", 120)))
	sw.OnTool("terminal", `{"cmd":"go test ./..."}`)

	sw.mu.Lock()
	rendered := sw.renderLocked()
	sw.mu.Unlock()
	if len([]rune(rendered)) > MaxMessageLength {
		t.Fatalf("rendered exceeded MaxMessageLength: %d", len([]rune(rendered)))
	}
	if !strings.Contains(rendered, "🧭 Langkah 2/80") || !strings.Contains(rendered, "⏳ Menjalankan `terminal`") {
		t.Fatalf("expected status block preserved at bottom of truncated stream, got tail: %s", rendered[len(rendered)-200:])
	}

	deduped := dedupeConsecutiveLines("baris A\nbaris A\nbaris B\nbaris B\nbaris A")
	if deduped != "baris A\nbaris B\nbaris A" {
		t.Fatalf("unexpected dedupeConsecutiveLines result: %q", deduped)
	}
}
