package telegram

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type captureHandler struct {
	mu   sync.Mutex
	seen []string
}

func (c *captureHandler) Handle(_ context.Context, _ int64, text string) (string, error) {
	c.mu.Lock()
	c.seen = append(c.seen, text)
	c.mu.Unlock()
	return "ok", nil
}

func (c *captureHandler) Approve(context.Context, int64, string, string) (bool, error) {
	return false, nil
}

func TestDocumentAndPhotoDownloadedToWorkspace(t *testing.T) {
	api, b := newAPI(t)
	api.filePath = "documents/laporan.pdf"
	api.fileBytes = []byte("%PDF-1.4 dummy")
	h := &captureHandler{}

	b.handleUpdate(context.Background(), h, update{
		UpdateID: 601,
		Message: &message{
			MessageID: 601,
			Caption:   "tolong ringkas",
			Chat:      chat{ID: 42, Type: "private"},
			Document:  &tgDocument{FileID: "doc123", FileName: "laporan.pdf", MimeType: "application/pdf"},
		},
	})

	api.mu.Lock()
	api.filePath = "photos/img_large.jpg"
	api.fileBytes = []byte("jpeg-bytes")
	api.mu.Unlock()
	b.handleUpdate(context.Background(), h, update{
		UpdateID: 602,
		Message: &message{
			MessageID: 602,
			Chat:      chat{ID: 42, Type: "private"},
			Photo: []tgPhoto{
				{FileID: "small", Width: 90, Height: 90, FileSize: 100},
				{FileID: "large", Width: 1280, Height: 720, FileSize: 50000},
			},
		},
	})

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.seen) != 2 {
		t.Fatalf("expected 2 messages handled, got %d", len(h.seen))
	}
	if !strings.Contains(h.seen[0], "[File diterima:") || !strings.Contains(h.seen[0], "laporan.pdf") || !strings.Contains(h.seen[0], "tolong ringkas") {
		t.Fatalf("document text = %q", h.seen[0])
	}
	if !strings.Contains(h.seen[1], "[Foto diterima:") || !strings.Contains(h.seen[1], "img_large.jpg") {
		t.Fatalf("photo text = %q", h.seen[1])
	}

	inbox := filepath.Join(b.workspaceDir, "inbox")
	entries, err := os.ReadDir(inbox)
	if err != nil || len(entries) != 2 {
		t.Fatalf("expected 2 downloaded files in %s, err=%v entries=%d", inbox, err, len(entries))
	}
}

func TestStreamWriterEditsAndFinalizes(t *testing.T) {
	api, b := newAPI(t)
	sw := NewStreamWriter(b, 42)
	if _, err := sw.Write([]byte("halo ")); err != nil {
		t.Fatal(err)
	}
	sw.OnTool("terminal", `{"cmd":"pwd"}`)
	if !sw.Finalize(context.Background(), "halo dunia selesai") {
		t.Fatal("Finalize harus mengembalikan true setelah draft terkirim")
	}

	api.mu.Lock()
	if len(api.richDrafts) == 0 {
		api.mu.Unlock()
		t.Fatal("expected sendRichMessageDraft calls")
	}
	if len(api.richMessages) != 1 {
		api.mu.Unlock()
		t.Fatalf("expected 1 sendRichMessage on Finalize, got %d", len(api.richMessages))
	}
	rm, _ := api.richMessages[0]["rich_message"].(map[string]any)
	if fmt.Sprint(rm["markdown"]) != "halo dunia selesai" {
		api.mu.Unlock()
		t.Fatalf("rich_message markdown = %v", rm["markdown"])
	}
	api.mu.Unlock()

	api2, b2 := newAPI(t)
	api2.disableRich = true
	sw2 := NewStreamWriter(b2, 42)
	if _, err := sw2.Write([]byte("halo ")); err != nil {
		t.Fatal(err)
	}
	if !sw2.Finalize(context.Background(), "halo dunia fallback") {
		t.Fatal("Finalize harus mengembalikan true setelah pesan awal terkirim")
	}
	if got := api2.texts(); len(got) != 1 {
		t.Fatalf("expected 1 initial sendMessage, got %d (%v)", len(got), got)
	}
	api2.mu.Lock()
	defer api2.mu.Unlock()
	if len(api2.edits) == 0 {
		t.Fatal("expected EditMessage during Finalize fallback")
	}
	lastEdit := fmt.Sprint(api2.edits[len(api2.edits)-1]["text"])
	if lastEdit != "halo dunia fallback" {
		t.Fatalf("lastEdit = %q, want 'halo dunia fallback'", lastEdit)
	}
}

func TestStoppedMessageGenerationCancelsTurn(t *testing.T) {
	_, b := newAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	b.RegisterCancel(42, cancel)

	b.handleUpdate(context.Background(), &fakeHandler{}, update{
		UpdateID: 901,
		StoppedMessageGeneration: &messageGenerationStopped{
			Chat:    chat{ID: 42, Type: "private"},
			DraftID: 1,
		},
	})

	if ctx.Err() == nil {
		t.Fatal("stopped_message_generation harus membatalkan turn yang sedang berjalan")
	}
}

func TestVoiceAudioAndSetMyCommands(t *testing.T) {
	api, b := newAPI(t)
	api.filePath = "voice/msg.ogg"
	api.fileBytes = []byte("ogg-bytes")
	h := &captureHandler{}

	b.handleUpdate(context.Background(), h, update{
		UpdateID: 950,
		Message: &message{
			MessageID: 950,
			Chat:      chat{ID: 42, Type: "private"},
			Voice:     &tgVoice{FileID: "v1", Duration: 4, MimeType: "audio/ogg"},
		},
	})

	h.mu.Lock()
	if len(h.seen) != 1 || !strings.Contains(h.seen[0], "[Voice diterima:") || !strings.Contains(h.seen[0], "4s") {
		h.mu.Unlock()
		t.Fatalf("voice text = %v", h.seen)
	}
	h.mu.Unlock()

	if err := b.SetMyCommands(context.Background(), DefaultCommands()); err != nil {
		t.Fatalf("SetMyCommands: %v", err)
	}
}

func TestMarkdownToHTMLAndSendFile(t *testing.T) {
	got := markdownToHTML("Halo **dunia**, cek `my_var_name` dan [repo](https://example.com)!\n```go\nfmt.Println(1 < 2)\n```")
	for _, want := range []string{"<b>dunia</b>", "<code>my_var_name</code>", `<a href="https://example.com">repo</a>`, `<pre><code class="language-go">fmt.Println(1 &lt; 2)</code></pre>`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}

	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.URL.Path
		io.WriteString(w, `{"ok":true,"result":{"message_id":99}}`)
	}))
	defer srv.Close()

	b, err := New(Config{Token: "tok", APIBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(t.TempDir(), "report.png")
	if err := os.WriteFile(tmp, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := b.SendFile(context.Background(), 42, tmp, "ini gambar", "auto"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotMethod, "/sendPhoto") {
		t.Fatalf("expected sendPhoto for .png, got %q", gotMethod)
	}
}

func TestSendMessageNoPlainFallbackOnHTTP500(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, `{"ok":false,"description":"internal error"}`)
	}))
	defer srv.Close()

	b, err := New(Config{Token: "tok", APIBase: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.SendMessage(context.Background(), 42, "halo"); err == nil {
		t.Fatal("seharusnya error pada HTTP 500")
	}
	if calls != 1 {
		t.Fatalf("tidak boleh retry plain text pada HTTP 500, calls = %d", calls)
	}

	calls = 0
	srv400 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if strings.Contains(string(raw), `"parse_mode"`) {
			w.WriteHeader(http.StatusBadRequest)
			io.WriteString(w, `{"ok":false,"description":"can't parse entities"}`)
			return
		}
		io.WriteString(w, `{"ok":true,"result":{"message_id":7}}`)
	}))
	defer srv400.Close()

	b400, err := New(Config{Token: "tok", APIBase: srv400.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := b400.SendMessage(context.Background(), 42, "halo"); err != nil {
		t.Fatalf("HTTP 400 seharusnya fallback ke plain text: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 calls on 400 fallback, got %d", calls)
	}
}

func TestStreamWriterRespectsTurnContextCancel(t *testing.T) {
	api, b := newAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	sw := NewStreamWriterWithContext(ctx, b, 42)
	cancel()
	if _, err := sw.Write([]byte("seharusnya tidak terkirim")); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.richDrafts) != 0 || len(api.sent) != 0 {
		t.Fatalf("flush tetap berjalan setelah context dibatalkan: drafts=%d sent=%d", len(api.richDrafts), len(api.sent))
	}
}

func TestVoiceTranscriptionIncluded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			io.WriteString(w, `{"ok":true,"result":{"file_path":"voice/v.ogg"}}`)
		case strings.Contains(r.URL.Path, "/file/bot"):
			w.Write([]byte("ogg"))
		default:
			io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
		}
	}))
	defer srv.Close()

	bot, err := New(Config{
		Token:        "tok",
		AllowedUsers: []int64{42},
		APIBase:      srv.URL,
		WorkspaceDir: t.TempDir(),
		Transcribe: func(_ context.Context, path string) (string, error) {
			return "halo dunia dari suara", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &captureHandler{}
	bot.handleUpdate(context.Background(), h, update{
		UpdateID: 1001,
		Message: &message{
			MessageID: 1001,
			Chat:      chat{ID: 42, Type: "private"},
			Voice:     &tgVoice{FileID: "vf1", Duration: 3, MimeType: "audio/ogg"},
		},
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.seen) != 1 || !strings.Contains(h.seen[0], "[Transkripsi suara]: halo dunia dari suara") {
		t.Fatalf("transkripsi tidak diteruskan ke handler: %v", h.seen)
	}
}
