package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI emulates the Bot API subset the bot uses.
type fakeAPI struct {
	mu        sync.Mutex
	sent      []string
	payloads  []map[string]any
	edits     []map[string]any
	fail429   int32
	onSend    func(map[string]any)
	filePath  string
	fileBytes []byte
}

func newAPI(t *testing.T) (*fakeAPI, *Bot) {
	t.Helper()
	api := &fakeAPI{filePath: "docs/sample.txt", fileBytes: []byte("isi file")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		json.Unmarshal(raw, &payload)

		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			if atomic.AddInt32(&api.fail429, -1) >= 0 {
				w.WriteHeader(http.StatusTooManyRequests)
				io.WriteString(w, `{"ok":false,"parameters":{"retry_after":0}}`)
				return
			}
			api.mu.Lock()
			api.sent = append(api.sent, fmt.Sprint(payload["text"]))
			api.payloads = append(api.payloads, payload)
			hook := api.onSend
			api.mu.Unlock()
			if hook != nil {
				hook(payload)
			}
			io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
		case strings.HasSuffix(r.URL.Path, "/editMessageText"):
			api.mu.Lock()
			api.edits = append(api.edits, payload)
			api.mu.Unlock()
			io.WriteString(w, `{"ok":true,"result":{"message_id":1}}`)
		case strings.HasSuffix(r.URL.Path, "/getFile"):
			api.mu.Lock()
			fp := api.filePath
			api.mu.Unlock()
			io.WriteString(w, fmt.Sprintf(`{"ok":true,"result":{"file_path":%q}}`, fp))
		case strings.Contains(r.URL.Path, "/file/bot"):
			api.mu.Lock()
			data := api.fileBytes
			api.mu.Unlock()
			w.Write(data)
		case strings.HasSuffix(r.URL.Path, "/answerCallbackQuery"):
			io.WriteString(w, `{"ok":true,"result":true}`)
		default:
			io.WriteString(w, `{"ok":true,"result":[]}`)
		}
	}))
	t.Cleanup(srv.Close)

	b, err := New(Config{Token: "tok", AllowedUsers: []int64{42}, APIBase: srv.URL, PollTimeout: 50 * time.Millisecond, WorkspaceDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return api, b
}

func (a *fakeAPI) texts() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.sent...)
}

func (a *fakeAPI) last() map[string]any {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.payloads) == 0 {
		return nil
	}
	return a.payloads[len(a.payloads)-1]
}

type fakeHandler struct {
	calls atomic.Int32
	reply string
}

func (h *fakeHandler) Handle(ctx context.Context, chatID int64, text string) (string, error) {
	h.calls.Add(1)
	return h.reply, nil
}

func (h *fakeHandler) Approve(ctx context.Context, chatID int64, tool, args string) (bool, error) {
	return false, nil
}

func textUpdate(id, chatID int64, chatType, text string) update {
	return update{UpdateID: id, Message: &message{MessageID: id, Text: text, Chat: chat{ID: chatID, Type: chatType}}}
}

func TestDedupSkipsSecondUpdate(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{reply: "ok"}
	u := textUpdate(100, 42, "private", "halo")

	b.handleUpdate(context.Background(), h, u)
	b.handleUpdate(context.Background(), h, u)

	if got := h.calls.Load(); got != 1 {
		t.Fatalf("handler dipanggil %d kali, mau 1", got)
	}
	if got := api.texts(); len(got) != 1 || got[0] != "ok" {
		t.Fatalf("balasan %q", got)
	}
}

func TestMarkSeenSkipsUpdate(t *testing.T) {
	_, b := newAPI(t)
	h := &fakeHandler{reply: "ok"}
	b.MarkSeen(7)
	b.handleUpdate(context.Background(), h, textUpdate(7, 42, "private", "halo"))
	if got := h.calls.Load(); got != 0 {
		t.Fatalf("handler dipanggil %d kali, mau 0", got)
	}
}

func TestUnauthorizedChatDenied(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{reply: "ok"}
	b.handleUpdate(context.Background(), h, textUpdate(1, 99, "private", "halo"))

	if got := h.calls.Load(); got != 0 {
		t.Fatalf("handler dipanggil %d kali, mau 0", got)
	}
	if got := api.texts(); len(got) != 1 || got[0] != "tidak diizinkan" {
		t.Fatalf("balasan %q", got)
	}
}

func TestGroupChatRejected(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{reply: "ok"}
	b.handleUpdate(context.Background(), h, textUpdate(2, 42, "group", "halo"))

	if got := h.calls.Load(); got != 0 {
		t.Fatalf("handler dipanggil %d kali, mau 0", got)
	}
	if got := api.texts(); len(got) != 1 || got[0] != "DM saya langsung" {
		t.Fatalf("balasan %q", got)
	}
}

func TestSplitMessageMultibyte(t *testing.T) {
	s := strings.Repeat("日本語テキスト\n", 40) // 8 runes per line
	chunks := SplitMessage(s, 16)
	if len(chunks) < 2 {
		t.Fatalf("mau >1 chunk, dapat %d", len(chunks))
	}
	for i, c := range chunks {
		if n := len([]rune(c)); n > 16 || n == 0 {
			t.Fatalf("chunk %d panjang %d", i, n)
		}
	}
	if joined := strings.Join(chunks, ""); strings.ReplaceAll(joined, "\n", "") != strings.ReplaceAll(s, "\n", "") {
		t.Fatal("isi berubah")
	}
}

func TestSplitMessageNoNewlines(t *testing.T) {
	s := strings.Repeat("a", 10000)
	chunks := SplitMessage(s, MaxMessageLength)
	if len(chunks) != 3 {
		t.Fatalf("mau 3 chunk, dapat %d", len(chunks))
	}
	for _, c := range chunks {
		if n := len([]rune(c)); n == 0 || n > MaxMessageLength {
			t.Fatalf("panjang chunk %d", n)
		}
	}
	if strings.Join(chunks, "") != s {
		t.Fatal("isi berubah")
	}
}

func TestRateLimitRetriedOnce(t *testing.T) {
	api, b := newAPI(t)
	atomic.StoreInt32(&api.fail429, 1)

	if err := b.SendMessage(context.Background(), 42, "halo"); err != nil {
		t.Fatal(err)
	}
	if got := api.texts(); len(got) != 1 || got[0] != "halo" {
		t.Fatalf("balasan %q", got)
	}
}

func TestSendMessageRefusesEmpty(t *testing.T) {
	api, b := newAPI(t)
	if err := b.SendMessage(context.Background(), 42, "   "); err == nil {
		t.Fatal("mau error untuk teks kosong")
	}
	if got := api.texts(); len(got) != 0 {
		t.Fatalf("tidak boleh kirim, dapat %q", got)
	}
}

func TestCommandsAnsweredByBot(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{reply: "ok"}
	for i, cmd := range []string{"/start", "/help"} {
		b.handleUpdate(context.Background(), h, textUpdate(int64(10+i), 42, "private", cmd))
	}
	if got := h.calls.Load(); got != 0 {
		t.Fatalf("handler dipanggil %d kali, mau 0", got)
	}
	if got := api.texts(); len(got) != 2 || !strings.Contains(got[0], "/stop") {
		t.Fatalf("balasan %q", got)
	}
}

func TestStopWithoutTurn(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{}
	b.handleUpdate(context.Background(), h, textUpdate(20, 42, "private", "/stop"))
	if got := api.texts(); len(got) != 1 || got[0] != "tidak ada yang berjalan" {
		t.Fatalf("balasan %q", got)
	}
}

func TestStopCancelsTurn(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	b.RegisterCancel(42, cancel)

	b.handleUpdate(context.Background(), h, textUpdate(21, 42, "private", "/stop"))

	if ctx.Err() == nil {
		t.Fatal("cancel tidak dipanggil")
	}
	if got := api.texts(); len(got) != 1 || got[0] != "dihentikan" {
		t.Fatalf("balasan %q", got)
	}
	b.mu.Lock()
	_, still := b.cancels[42]
	b.mu.Unlock()
	if still {
		t.Fatal("cancel func tidak dibersihkan")
	}
}

func TestAskApprovalApproveCallback(t *testing.T) {
	api, b := newAPI(t)
	done := make(chan bool, 1)
	go func() {
		ok, err := b.AskApproval(context.Background(), 42, "terminal", `{"cmd":"rm -rf /"}`, 2*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- ok
	}()

	var id int64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := api.last(); p != nil {
			kb, _ := json.Marshal(p["reply_markup"])
			if strings.Contains(string(kb), "approve:") {
				var markup struct {
					InlineKeyboard [][]struct {
						CallbackData string `json:"callback_data"`
					} `json:"inline_keyboard"`
				}
				json.Unmarshal(kb, &markup)
				_, raw, _ := strings.Cut(markup.InlineKeyboard[0][0].CallbackData, ":")
				fmt.Sscan(raw, &id)
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == 0 {
		t.Fatal("keyboard approval tidak terkirim")
	}

	b.handleUpdate(context.Background(), nil, update{UpdateID: 30, CallbackQuery: &callbackQuery{
		ID:      "cb1",
		Data:    fmt.Sprintf("approve:%d", id),
		Message: &message{Chat: chat{ID: 42, Type: "private"}},
	}})

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("mau true")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("approval tidak selesai")
	}
}

func TestAskApprovalTimeoutDenies(t *testing.T) {
	_, b := newAPI(t)
	ok, err := b.AskApproval(context.Background(), 42, "terminal", "", 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("timeout harus false")
	}
}

func TestRunReturnsCtxErr(t *testing.T) {
	_, b := newAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.Run(ctx, &fakeHandler{}); err != context.Canceled {
		t.Fatalf("err %v", err)
	}
}

func TestNewRequiresToken(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("mau error tanpa token")
	}
}

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

	// 1. Document with caption
	b.handleUpdate(context.Background(), h, update{
		UpdateID: 601,
		Message: &message{
			MessageID: 601,
			Caption:   "tolong ringkas",
			Chat:      chat{ID: 42, Type: "private"},
			Document:  &tgDocument{FileID: "doc123", FileName: "laporan.pdf", MimeType: "application/pdf"},
		},
	})

	// 2. Photo without caption (picks largest size)
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

func TestAskApprovalEditsMessageOnDecision(t *testing.T) {
	api, b := newAPI(t)
	done := make(chan bool, 1)
	go func() {
		ok, err := b.AskApproval(context.Background(), 42, "terminal", `{"cmd":"ls"}`, 2*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- ok
	}()

	var id int64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if p := api.last(); p != nil {
			kb, _ := json.Marshal(p["reply_markup"])
			if strings.Contains(string(kb), "deny:") {
				var markup struct {
					InlineKeyboard [][]struct {
						CallbackData string `json:"callback_data"`
					} `json:"inline_keyboard"`
				}
				json.Unmarshal(kb, &markup)
				_, raw, _ := strings.Cut(markup.InlineKeyboard[0][1].CallbackData, ":")
				fmt.Sscan(raw, &id)
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == 0 {
		t.Fatal("keyboard approval tidak terkirim")
	}

	b.handleUpdate(context.Background(), nil, update{UpdateID: 701, CallbackQuery: &callbackQuery{
		ID:      "cb-deny",
		Data:    fmt.Sprintf("deny:%d", id),
		Message: &message{Chat: chat{ID: 42, Type: "private"}},
	}})

	if got := <-done; got {
		t.Fatal("expected false on deny")
	}

	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.edits) == 0 {
		t.Fatal("expected EditMessage after callback decision")
	}
	editText := fmt.Sprint(api.edits[len(api.edits)-1]["text"])
	if !strings.Contains(editText, "Ditolak") || !strings.Contains(editText, "terminal") {
		t.Fatalf("editText = %q", editText)
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
		t.Fatal("Finalize harus mengembalikan true setelah pesan awal terkirim")
	}

	if got := api.texts(); len(got) != 1 {
		t.Fatalf("expected 1 initial sendMessage, got %d (%v)", len(got), got)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if len(api.edits) == 0 {
		t.Fatal("expected EditMessage during Finalize")
	}
	lastEdit := fmt.Sprint(api.edits[len(api.edits)-1]["text"])
	if lastEdit != "halo dunia selesai" {
		t.Fatalf("lastEdit = %q, want 'halo dunia selesai'", lastEdit)
	}
}
