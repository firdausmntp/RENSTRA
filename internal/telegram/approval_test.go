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
	"testing"
	"time"
)

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

func TestAskApprovalConcurrentIDsAndTimeoutEdit(t *testing.T) {
	api, b := newAPI(t)

	ok, err := b.AskApproval(context.Background(), 42, "terminal", "ls", 30*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("timeout harus deny")
	}
	api.mu.Lock()
	if len(api.edits) == 0 || !strings.Contains(fmt.Sprint(api.edits[len(api.edits)-1]["text"]), "kedaluwarsa") {
		api.mu.Unlock()
		t.Fatalf("pesan timeout tidak diedit: %+v", api.edits)
	}
	api.mu.Unlock()

	var cbData []string
	var cbMu sync.Mutex
	api.mu.Lock()
	api.onSend = func(p map[string]any) {
		rm, _ := p["reply_markup"].(map[string]any)
		if rm == nil {
			return
		}
		kb, _ := rm["inline_keyboard"].([]any)
		if len(kb) > 0 {
			row, _ := kb[0].([]any)
			if len(row) > 0 {
				btn, _ := row[0].(map[string]any)
				cbMu.Lock()
				cbData = append(cbData, fmt.Sprint(btn["callback_data"]))
				cbMu.Unlock()
			}
		}
	}
	api.mu.Unlock()

	res1 := make(chan bool, 1)
	res2 := make(chan bool, 1)
	go func() {
		v, _ := b.AskApproval(context.Background(), 99, "t1", "a1", time.Second)
		res1 <- v
	}()
	go func() {
		v, _ := b.AskApproval(context.Background(), 99, "t2", "a2", time.Second)
		res2 <- v
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cbMu.Lock()
		n := len(cbData)
		cbMu.Unlock()
		if n == 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cbMu.Lock()
	if len(cbData) != 2 {
		cbMu.Unlock()
		t.Fatalf("expected 2 concurrent approval buttons, got %d", len(cbData))
	}
	c0, c1 := cbData[0], cbData[1]
	cbMu.Unlock()

	b.handleCallback(context.Background(), callbackQuery{ID: "cb1", Data: c0, Message: &message{Chat: chat{ID: 99}}})
	b.handleCallback(context.Background(), callbackQuery{ID: "cb2", Data: strings.Replace(c1, "approve:", "deny:", 1), Message: &message{Chat: chat{ID: 99}}})

	r1, r2 := <-res1, <-res2
	if r1 == r2 {
		t.Fatalf("satu approve dan satu deny tidak boleh tertukar: r1=%v r2=%v", r1, r2)
	}
}

func TestFormatApprovalMessageAndAutoApproveCallback(t *testing.T) {
	htmlText, plainText := formatApprovalMessage("terminal", `{"cmd":"rsvg-convert -w 1600 a.svg -o a.png"}`, 120*time.Second)
	if !strings.Contains(htmlText, `<pre><code class="language-bash">rsvg-convert -w 1600 a.svg -o a.png</code></pre>`) {
		t.Fatalf("format HTML tidak mengekstrak cmd: %s", htmlText)
	}
	if strings.Contains(htmlText, `{"cmd":`) || strings.Contains(plainText, `{"cmd":`) {
		t.Fatalf("format approval tidak boleh menampilkan JSON mentah untuk terminal: html=%q plain=%q", htmlText, plainText)
	}

	var autoChat int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true,"result":{"message_id":77}}`)
	}))
	defer srv.Close()

	b, err := New(Config{
		Token:   "tok",
		APIBase: srv.URL,
		OnAutoApprove: func(cid int64) {
			autoChat = cid
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan bool, 1)
	go func() {
		ok, err := b.AskApproval(context.Background(), 55, "terminal", `{"cmd":"echo hi"}`, 2*time.Second)
		if err != nil {
			t.Error(err)
		}
		done <- ok
	}()

	var id int64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		for k, p := range b.pending {
			if p.chatID == 55 {
				id = k
				break
			}
		}
		b.mu.Unlock()
		if id > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if id == 0 {
		t.Fatal("pending approval tidak ditemukan")
	}

	b.handleUpdate(context.Background(), nil, update{
		UpdateID: 400,
		CallbackQuery: &callbackQuery{
			ID:      "cb-auto",
			Data:    fmt.Sprintf("auto:%d", id),
			Message: &message{Chat: chat{ID: 55, Type: "private"}},
		},
	})

	select {
	case ok := <-done:
		if !ok {
			t.Fatal("auto callback harus menyetujui tool")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout menunggu hasil approval")
	}
	if autoChat != 55 {
		t.Fatalf("OnAutoApprove tidak terpanggil untuk chat 55, got %d", autoChat)
	}
}
