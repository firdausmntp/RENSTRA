package telegram

import (
	"context"
	"strings"
	"testing"
)

// TestStartHelpMarkdownConversion memastikan teks sambutan & bantuan hanya
// memakai markdown yang didukung markdownToHTML dan terkonversi jadi HTML
// Telegram yang valid (tanpa sisa ** atau * mentah).
func TestStartHelpMarkdownConversion(t *testing.T) {
	startHTML := markdownToHTML(startText)
	helpHTML := markdownToHTML(helpText)

	for name, html := range map[string]string{"start": startHTML, "help": helpHTML} {
		if strings.Contains(html, "**") {
			t.Errorf("%s: masih ada ** mentah setelah konversi:\n%s", name, html)
		}
		if !strings.Contains(html, "<b>") {
			t.Errorf("%s: bold tidak terkonversi:\n%s", name, html)
		}
		if !strings.Contains(html, "<code>") {
			t.Errorf("%s: inline code tidak terkonversi:\n%s", name, html)
		}
		if !strings.Contains(html, "<blockquote>") {
			t.Errorf("%s: blockquote tidak terkonversi:\n%s", name, html)
		}
	}

	if !strings.Contains(startHTML, "<code>/stop</code>") {
		t.Errorf("start harus menyebut /stop (menu cepat):\n%s", startHTML)
	}
	if !strings.Contains(startHTML, "<i>") {
		t.Errorf("start: italic tidak terkonversi:\n%s", startHTML)
	}
	if !strings.Contains(helpHTML, "<pre>") {
		t.Errorf("help: contoh fenced block tidak jadi <pre>:\n%s", helpHTML)
	}
	if !strings.Contains(helpHTML, "<code>/cron add") {
		t.Errorf("help: perintah cron tidak terkonversi:\n%s", helpHTML)
	}
	if !strings.Contains(helpHTML, "/insights") {
		t.Errorf("help harus memuat /insights:\n%s", helpHTML)
	}
	// Jadwal cron di dalam fenced block tidak boleh diitalic-kan oleh *.
	if strings.Contains(helpHTML, "<i>") && strings.Contains(helpHTML, "0 9 <i>") {
		t.Errorf("bintang jadwal cron bocor jadi italic:\n%s", helpHTML)
	}
}

// TestStartAndHelpDistinct memastikan /start dan /help mengirim pesan berbeda.
func TestStartAndHelpDistinct(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{reply: "ok"}
	b.handleUpdate(context.Background(), h, textUpdate(100, 42, "private", "/start"))
	b.handleUpdate(context.Background(), h, textUpdate(101, 42, "private", "/help"))
	if got := h.calls.Load(); got != 0 {
		t.Fatalf("handler dipanggil %d kali, mau 0", got)
	}
	texts := api.texts()
	if len(texts) != 2 {
		t.Fatalf("mau 2 balasan, dapat %d", len(texts))
	}
	if texts[0] == texts[1] {
		t.Fatal("/start dan /help tidak boleh identik")
	}
	if !strings.Contains(texts[0], "Kapabilitas") || !strings.Contains(texts[0], "/stop") {
		t.Fatalf("balasan /start kurang lengkap:\n%s", texts[0])
	}
	if !strings.Contains(texts[1], "Panduan Lengkap") || !strings.Contains(texts[1], "/insights") {
		t.Fatalf("balasan /help kurang lengkap:\n%s", texts[1])
	}
}

func TestSplitMessagePreservesCodeBlocks(t *testing.T) {
	input := "Header penjelasan:\n```python\nline 1\nline 2\nline 3\n```\nFooter"
	// Split with a small limit so it cuts inside the python block
	chunks := SplitMessage(input, 35)
	if len(chunks) < 2 {
		t.Fatalf("seharusnya terpotong minimal 2 chunk, dapat %d", len(chunks))
	}
	// Chunk pertama harus ditutup ```
	if !strings.HasSuffix(strings.TrimSpace(chunks[0]), "```") {
		t.Errorf("chunk 1 tidak ditutup code fence: %q", chunks[0])
	}
	// Chunk kedua harus dibuka ```python
	if !strings.HasPrefix(strings.TrimSpace(chunks[1]), "```python") {
		t.Errorf("chunk 2 tidak dibuka kembali dengan ```python: %q", chunks[1])
	}
	// Keduanya harus terkonversi menjadi <pre> valid oleh markdownToHTML
	for i, chunk := range chunks {
		html := markdownToHTML(chunk)
		if strings.Contains(chunk, "```") && !strings.Contains(html, "<pre>") {
			t.Errorf("chunk %d tidak terkonversi ke <pre>: %s", i+1, html)
		}
	}
}
