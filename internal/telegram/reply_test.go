package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type captureReplyHandler struct {
	gotText string
	reply   string
}

func (c *captureReplyHandler) Handle(_ context.Context, _ int64, text string) (string, error) {
	c.gotText = text
	return c.reply, nil
}

func (c *captureReplyHandler) Approve(context.Context, int64, string, string) (bool, error) {
	return false, nil
}

func TestReplyAndQuoteContextAndThreadParameters(t *testing.T) {
	api, b := newAPI(t)
	h := &captureReplyHandler{reply: "sip"}

	u := update{
		UpdateID: 301,
		Message: &message{
			MessageID:       77,
			MessageThreadID: 555,
			Chat:            chat{ID: 42, Type: "private"},
			Text:            "lanjutkan bagian ini",
			ReplyToMessage: &message{
				MessageID: 12,
				Text:      "tolong cek server A",
			},
		},
	}

	b.handleUpdate(context.Background(), h, u)

	if !strings.Contains(h.gotText, `[Membalas pesan: "tolong cek server A"]`) ||
		!strings.Contains(h.gotText, "lanjutkan bagian ini") {
		t.Fatalf("konteks reply tidak masuk: %q", h.gotText)
	}

	last := api.last()
	if last == nil {
		t.Fatal("diharapkan sendMessage dipanggil")
	}
	if tid, ok := last["message_thread_id"].(float64); !ok || int64(tid) != 555 {
		t.Fatalf("sendMessage message_thread_id = %v, want 555", last["message_thread_id"])
	}
	rp, ok := last["reply_parameters"].(map[string]any)
	if !ok || rp["message_id"] != float64(77) {
		t.Fatalf("sendMessage reply_parameters = %v, want message_id 77", last["reply_parameters"])
	}

	// Verify SendRichMessage also propagates message_thread_id & reply_parameters
	if err := b.SendRichMessage(withReplyTarget(context.Background(), 77, 555), 42, "rich reply"); err != nil {
		t.Fatal(err)
	}
	if len(api.richMessages) == 0 {
		t.Fatal("diharapkan sendRichMessage dipanggil")
	}
	lastRich := api.richMessages[len(api.richMessages)-1]
	if tid, ok := lastRich["message_thread_id"].(float64); !ok || int64(tid) != 555 {
		t.Fatalf("sendRichMessage message_thread_id = %v, want 555", lastRich["message_thread_id"])
	}
	if rpr, ok := lastRich["reply_parameters"].(map[string]any); !ok || rpr["message_id"] != float64(77) {
		t.Fatalf("sendRichMessage reply_parameters = %v, want message_id 77", lastRich["reply_parameters"])
	}

	// Test Quote overrides ReplyToMessage text snippet
	uq := update{
		UpdateID: 303,
		Message: &message{
			MessageID: 78,
			Chat:      chat{ID: 42, Type: "private"},
			Text:      "jelaskan ini",
			ReplyToMessage: &message{
				MessageID: 12,
				Text:      "teks panjang banget",
			},
			Quote: &tgTextQuote{Text: "bagian kutipan"},
		},
	}
	b.handleUpdate(context.Background(), h, uq)
	if !strings.Contains(h.gotText, `[Membalas pesan: "bagian kutipan"]`) {
		t.Fatalf("konteks quote tidak masuk: %q", h.gotText)
	}
}

func TestRememberReplyShortcut(t *testing.T) {
	_, b := newAPI(t)
	h := &captureReplyHandler{reply: "disimpan"}

	u := update{
		UpdateID: 302,
		Message: &message{
			MessageID: 88,
			Chat:      chat{ID: 42, Type: "private"},
			Text:      "/remember",
			ReplyToMessage: &message{
				MessageID: 80,
				Text:      "Server prod IP 10.0.0.5",
			},
		},
	}
	b.handleUpdate(context.Background(), h, u)
	if h.gotText != "/remember Server prod IP 10.0.0.5" {
		t.Fatalf("shortcut /remember = %q", h.gotText)
	}
}

func TestMarkdownHeadingToHTML(t *testing.T) {
	got := markdownToHTML("# Judul Utama\n## **Sub Bab**\n**tebal *miring* oke**\n_miring_ dan __tebal__\n~~coret~~\nmy_snake_case\n* bullet *item*\n> **Quote:** _isi_ `cmd`\n---")
	for _, want := range []string{
		"<b>Judul Utama</b>",
		"<b>Sub Bab</b>",
		"<b>tebal <i>miring</i> oke</b>",
		"<i>miring</i> dan <b>tebal</b>",
		"<s>coret</s>",
		"my_snake_case",
		"• bullet <i>item</i>",
		"<blockquote><b>Quote:</b> <i>isi</i> <code>cmd</code></blockquote>",
		"────────",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdownToHTML missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<b><b>") || strings.Contains(got, "</b></b>") {
		t.Fatalf("markdownToHTML produced nested <b> tags:\n%s", got)
	}
}

type errHandler struct{}

func (errHandler) Handle(context.Context, int64, string) (string, error) {
	return "", errors.New("provider error: HTTP 500 internal failure")
}
func (errHandler) Approve(context.Context, int64, string, string) (bool, error) { return false, nil }

func TestErrorSendsDiagnosticLogDocument(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "error.log")
	if err := os.WriteFile(logFile, []byte("diagnostic log line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var sentDoc, sentMsg atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			sentMsg.Add(1)
		case strings.HasSuffix(r.URL.Path, "/sendDocument"):
			sentDoc.Add(1)
		}
		w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	}))
	defer srv.Close()

	b, err := New(Config{
		Token:        "tok",
		AllowedUsers: []int64{42},
		APIBase:      srv.URL,
		WorkspaceDir: t.TempDir(),
		OnErrorLog: func(context.Context, int64, error) string {
			return logFile
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	b.handleUpdate(context.Background(), errHandler{}, textUpdate(501, 42, "private", "cek"))
	if sentMsg.Load() == 0 || sentDoc.Load() != 1 {
		t.Fatalf("expected formatted error message + 1 log document, got msg=%d doc=%d", sentMsg.Load(), sentDoc.Load())
	}
}

func TestSendRichBlocksWithDividerAndFallback(t *testing.T) {
	api, b := newAPI(t)
	blocks := []RichBlock{
		{Type: "section_heading", Text: "Ringkasan"},
		{Type: "paragraph", Text: "Isi utama"},
		RichBlockDivider(),
		{Type: "footer", Text: "Selesai"},
	}
	if err := b.SendRichBlocks(context.Background(), 42, blocks); err != nil {
		t.Fatal(err)
	}
	if len(api.richMessages) != 1 {
		t.Fatalf("expected 1 richMessage call, got %d", len(api.richMessages))
	}
	rm, _ := api.richMessages[0]["rich_message"].(map[string]any)
	rawBlocks, _ := rm["blocks"].([]any)
	if len(rawBlocks) != 4 {
		t.Fatalf("expected 4 blocks including divider, got %v", rm["blocks"])
	}

	// When rich_message is rejected, falls back cleanly to HTML sendMessage
	api.disableRich = true
	if err := b.SendRichBlocks(context.Background(), 42, blocks); err != nil {
		t.Fatal(err)
	}
	if got := api.texts(); len(got) != 1 || !strings.Contains(got[0], "────────") {
		t.Fatalf("expected HTML fallback divider ────────, got %v", got)
	}
}

func TestGroupTopicFilteringMentionModeAndBotAPI105(t *testing.T) {
	var gotSettings map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			w.Write([]byte(`{"ok":true,"result":{"id":900,"is_bot":true,"first_name":"Renstra","username":"renstra_bot","active_usernames":["renstra_bot","renstra_ai"],"can_join_groups":true,"can_read_all_group_messages":true,"has_topics_enabled":true,"has_topic_sidebar":true,"inline_query_placeholder":"Tanya RENSTRA..."}}`))
		case strings.HasSuffix(r.URL.Path, "/setMySettings"):
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotSettings = body
			w.Write([]byte(`{"ok":true,"result":true}`))
		default:
			w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
		}
	}))
	defer srv.Close()

	b, err := New(Config{
		Token:         "tok",
		AllowedUsers:  []int64{42},
		AllowedTopics: []int64{101},
		GroupMode:     "mention",
		APIBase:       srv.URL,
		WorkspaceDir:  t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Bot API 10.5 GetMe & SetMySettings
	info, err := b.GetMe(context.Background())
	if err != nil {
		t.Fatalf("GetMe: %v", err)
	}
	if info.Username != "renstra_bot" || !info.HasTopicSidebar || len(info.ActiveUsernames) != 2 {
		t.Fatalf("unexpected BotFullInfo: %+v", info)
	}
	tTrue := true
	if err := b.SetMySettings(context.Background(), BotSettings{
		CanJoinGroups:    &tTrue,
		HasTopicsEnabled: &tTrue,
		HasTopicSidebar:  &tTrue,
		CallbackQueryMode: "inline",
	}); err != nil {
		t.Fatalf("SetMySettings: %v", err)
	}
	if gotSettings["has_topic_sidebar"] != true || gotSettings["callback_query_mode"] != "inline" {
		t.Fatalf("unexpected setMySettings payload: %+v", gotSettings)
	}

	// 2. Group topic filtering & mention mode
	h := &captureReplyHandler{reply: "oke"}

	// Unrelated topic (#202, not in AllowedTopics=[101]) is ignored
	b.handleUpdate(context.Background(), h, update{
		UpdateID: 701,
		Message: &message{
			MessageID:       1,
			MessageThreadID: 202,
			IsTopicMessage:  true,
			From:            &tgUser{ID: 42, FirstName: "Budi", Username: "budi"},
			Chat:            chat{ID: -100500, Type: "supergroup", Title: "Core Team", IsForum: true},
			Text:            "@renstra_bot cek topik 202",
		},
	})
	if h.gotText != "" {
		t.Fatalf("unrelated topic 202 should be ignored, got %q", h.gotText)
	}

	// Relevant topic (#101) without mention in "mention" mode is ignored
	b.handleUpdate(context.Background(), h, update{
		UpdateID: 702,
		Message: &message{
			MessageID:       2,
			MessageThreadID: 101,
			IsTopicMessage:  true,
			From:            &tgUser{ID: 42, FirstName: "Budi", Username: "budi"},
			Chat:            chat{ID: -100500, Type: "supergroup", Title: "Core Team", IsForum: true},
			Text:            "obrolan antar member",
		},
	})
	if h.gotText != "" {
		t.Fatalf("unmentioned group message in mention mode should be ignored, got %q", h.gotText)
	}

	// Relevant topic (#101) WITH @renstra_bot mention and forum topic creation context is processed
	b.handleUpdate(context.Background(), h, update{
		UpdateID: 703,
		Message: &message{
			MessageID:       3,
			MessageThreadID: 101,
			IsTopicMessage:  true,
			From:            &tgUser{ID: 42, FirstName: "Budi", Username: "budi"},
			Chat:            chat{ID: -100500, Type: "supergroup", Title: "Core Team", IsForum: true},
			Text:            "@renstra_bot deploy sekarang",
			ReplyToMessage: &message{
				MessageID:         101,
				ForumTopicCreated: &tgForumTopic{Name: "DevOps Infra"},
			},
		},
	})
	if !strings.Contains(h.gotText, `Grup: "Core Team"`) ||
		!strings.Contains(h.gotText, `Topik: "DevOps Infra" (#101)`) ||
		!strings.Contains(h.gotText, "Pengirim: Budi (@budi)") ||
		!strings.Contains(h.gotText, "deploy sekarang") {
		t.Fatalf("unexpected group/topic context: %q", h.gotText)
	}
}
