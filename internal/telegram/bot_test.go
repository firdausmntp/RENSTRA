package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeAPI emulates the Bot API subset the bot uses.
type fakeAPI struct {
	mu           sync.Mutex
	sent         []string
	payloads     []map[string]any
	edits        []map[string]any
	richDrafts   []map[string]any
	richMessages []map[string]any
	disableRich  bool
	fail429      int32
	onSend       func(map[string]any)
	filePath     string
	fileBytes    []byte
}

func newAPI(t *testing.T) (*fakeAPI, *Bot) {
	t.Helper()
	api := &fakeAPI{filePath: "docs/sample.txt", fileBytes: []byte("isi file")}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var payload map[string]any
		json.Unmarshal(raw, &payload)

		switch {
		case strings.HasSuffix(r.URL.Path, "/sendRichMessageDraft"):
			api.mu.Lock()
			disabled := api.disableRich
			if !disabled {
				api.richDrafts = append(api.richDrafts, payload)
			}
			api.mu.Unlock()
			if disabled {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"ok":false,"description":"Not Found"}`)
				return
			}
			io.WriteString(w, `{"ok":true,"result":true}`)
		case strings.HasSuffix(r.URL.Path, "/sendRichMessage"):
			api.mu.Lock()
			disabled := api.disableRich
			if !disabled {
				api.richMessages = append(api.richMessages, payload)
			}
			api.mu.Unlock()
			if disabled {
				w.WriteHeader(http.StatusNotFound)
				io.WriteString(w, `{"ok":false,"description":"Not Found"}`)
				return
			}
			io.WriteString(w, `{"ok":true,"result":{"message_id":2}}`)
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

func TestGroupChatAllowedByChatOrFromUser(t *testing.T) {
	api, b := newAPI(t)
	h := &fakeHandler{reply: "ok"}

	// 1. Group chat whose chatID is in AllowedUsers (42) works directly
	b.handleUpdate(context.Background(), h, textUpdate(2, 42, "group", "halo grup"))
	if got := h.calls.Load(); got != 1 {
		t.Fatalf("handler dipanggil %d kali, mau 1", got)
	}
	if got := api.texts(); len(got) != 1 || got[0] != "ok" {
		t.Fatalf("balasan %q", got)
	}

	// 2. Supergroup (-100999) where sender From.ID (42) is in AllowedUsers also works
	uSuper := update{
		UpdateID: 3,
		Message: &message{
			MessageID:       10,
			MessageThreadID: 777,
			IsTopicMessage:  true,
			From:            &tgUser{ID: 42, FirstName: "Budi", Username: "budi"},
			Chat:            chat{ID: -100999, Type: "supergroup", Title: "Tim Dev", IsForum: true},
			Text:            "halo topik",
		},
	}
	b.handleUpdate(context.Background(), h, uSuper)
	if got := h.calls.Load(); got != 2 {
		t.Fatalf("supergroup dengan allowed From.ID seharusnya diproses, calls=%d", got)
	}

	// 3. Unauthorized user in unauthorized group is silently ignored on normal text
	uUnauth := update{
		UpdateID: 4,
		Message: &message{
			MessageID: 11,
			From:      &tgUser{ID: 999},
			Chat:      chat{ID: -100888, Type: "supergroup"},
			Text:      "pesan member lain",
		},
	}
	b.handleUpdate(context.Background(), h, uUnauth)
	if got := h.calls.Load(); got != 2 {
		t.Fatalf("unauthorized group message tidak boleh diproses, calls=%d", got)
	}
}

func TestSplitMessageMultibyte(t *testing.T) {
	s := strings.Repeat("日本語テキスト\n", 40)
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
