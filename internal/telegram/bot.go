// Package telegram implements the Telegram Bot API client used by the renstra
// gateway: long polling, per-chat turns, dedup, and inline approval.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxMessageLength is the Telegram limit for one text message, in runes.
	MaxMessageLength = 4096
	defaultAPIBase   = "https://api.telegram.org"
	defaultPoll      = 30 * time.Second
	approvalTimeout  = 120 * time.Second
)

type Config struct {
	Token        string
	AllowedUsers []int64
	PollTimeout  time.Duration
	APIBase      string
	// OnSeen is called for every update id before it is dispatched, so the
	// caller can persist it and keep dedup working across restarts.
	OnSeen func(updateID int64)
}

type Handler interface {
	Handle(ctx context.Context, chatID int64, text string) (string, error)
	Approve(ctx context.Context, chatID int64, tool string, args string) (bool, error)
}

type Bot struct {
	token       string
	apiBase     string
	pollTimeout time.Duration
	allowed     map[int64]struct{}
	onSeen      func(int64)
	http        *http.Client

	mu          sync.Mutex
	seen        map[int64]struct{}
	cancels     map[int64]turnHandle
	locks       map[int64]*chatLock
	pending     map[int64]*pendingApproval
	approvalSeq int64
	turnSeq     int64
}

// turnHandle identifies one running turn, so a finishing turn cannot clear a
// successor's cancel func.
type turnHandle struct {
	cancel context.CancelFunc
	gen    int64
}

// chatLock serializes turns per chat: a new message interrupts the running
// turn and then runs after it, never concurrently.
type chatLock struct {
	mu   sync.Mutex
	refs int
}

type pendingApproval struct {
	id int64
	ch chan bool
}

func New(cfg Config) (*Bot, error) {
	if strings.TrimSpace(cfg.Token) == "" {
		return nil, errors.New("telegram: token kosong")
	}
	base := strings.TrimRight(cfg.APIBase, "/")
	if base == "" {
		base = defaultAPIBase
	}
	poll := cfg.PollTimeout
	if poll <= 0 {
		poll = defaultPoll
	}
	allowed := make(map[int64]struct{}, len(cfg.AllowedUsers))
	for _, id := range cfg.AllowedUsers {
		allowed[id] = struct{}{}
	}
	return &Bot{
		token:       cfg.Token,
		apiBase:     base,
		pollTimeout: poll,
		allowed:     allowed,
		onSeen:      cfg.OnSeen,
		// The client must outlive one long poll.
		http:    &http.Client{Timeout: poll + 30*time.Second},
		seen:    map[int64]struct{}{},
		cancels: map[int64]turnHandle{},
		locks:   map[int64]*chatLock{},
		pending: map[int64]*pendingApproval{},
	}, nil
}

// MarkSeen seeds the dedup set, so a gateway can reload update ids after a
// restart before polling resumes.
func (b *Bot) MarkSeen(id int64) {
	b.mu.Lock()
	b.seen[id] = struct{}{}
	b.mu.Unlock()
}

// RegisterCancel stores the cancel func for the active turn of a chat.
func (b *Bot) RegisterCancel(chatID int64, cancel context.CancelFunc) int64 {
	b.mu.Lock()
	b.turnSeq++
	gen := b.turnSeq
	b.cancels[chatID] = turnHandle{cancel: cancel, gen: gen}
	b.mu.Unlock()
	return gen
}

func (b *Bot) clearCancel(chatID int64, gen int64) {
	b.mu.Lock()
	if cur, ok := b.cancels[chatID]; ok && cur.gen == gen {
		delete(b.cancels, chatID)
	}
	b.mu.Unlock()
}

// Run polls getUpdates until ctx is cancelled, then returns ctx.Err().
func (b *Bot) Run(ctx context.Context, h Handler) error {
	var offset int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		ups, err := b.getUpdates(ctx, offset)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
			continue
		}
		for _, u := range ups {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			// Dispatch off the polling goroutine so /stop can reach a busy chat.
			// shortcut: no per-chat turn serialization or interrupt, add when
			// the gateway wires one active turn per chat.
			go b.handleUpdate(ctx, h, u)
		}
	}
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	raw, err := b.post(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(b.pollTimeout / time.Second),
		"allowed_updates": []string{"message", "callback_query"},
	})
	if err != nil {
		return nil, err
	}
	var ups []update
	if err := json.Unmarshal(raw, &ups); err != nil {
		return nil, fmt.Errorf("telegram: getUpdates tidak valid: %w", err)
	}
	return ups, nil
}

func (b *Bot) handleUpdate(ctx context.Context, h Handler, u update) {
	b.mu.Lock()
	if _, dup := b.seen[u.UpdateID]; dup {
		b.mu.Unlock()
		return
	}
	b.seen[u.UpdateID] = struct{}{}
	onSeen := b.onSeen
	b.mu.Unlock()

	if onSeen != nil {
		onSeen(u.UpdateID)
	}

	if u.CallbackQuery != nil {
		b.handleCallback(ctx, *u.CallbackQuery)
		return
	}
	if u.Message != nil {
		b.handleMessage(ctx, h, *u.Message)
	}
}

func (b *Bot) lockChat(chatID int64) func() {
	b.mu.Lock()
	l := b.locks[chatID]
	if l == nil {
		l = &chatLock{}
		b.locks[chatID] = l
	}
	l.refs++
	// Interrupt running turn if any
	if h, ok := b.cancels[chatID]; ok && h.cancel != nil {
		h.cancel()
	}
	b.mu.Unlock()

	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		b.mu.Lock()
		l.refs--
		if l.refs <= 0 {
			delete(b.locks, chatID)
		}
		b.mu.Unlock()
	}
}

func (b *Bot) handleMessage(ctx context.Context, h Handler, m message) {
	// Group traffic is answered once and dropped before anything else.
	if m.Chat.Type != "private" {
		b.reply(ctx, m.Chat.ID, "DM saya langsung")
		return
	}
	if _, ok := b.allowed[m.Chat.ID]; !ok {
		b.reply(ctx, m.Chat.ID, "tidak diizinkan")
		return
	}

	switch command(m.Text) {
	case "/start", "/help":
		b.reply(ctx, m.Chat.ID, helpText)
		return
	case "/stop":
		b.mu.Lock()
		h, ok := b.cancels[m.Chat.ID]
		delete(b.cancels, m.Chat.ID)
		b.mu.Unlock()
		if !ok || h.cancel == nil {
			b.reply(ctx, m.Chat.ID, "tidak ada yang berjalan")
			return
		}
		h.cancel()
		b.reply(ctx, m.Chat.ID, "dihentikan")
		return
	}

	if h == nil {
		return
	}
	unlock := b.lockChat(m.Chat.ID)
	defer unlock()

	turnCtx, turnCancel := context.WithCancel(ctx)
	gen := b.RegisterCancel(m.Chat.ID, turnCancel)
	defer func() {
		turnCancel()
		b.clearCancel(m.Chat.ID, gen)
	}()

	out, err := h.Handle(turnCtx, m.Chat.ID, m.Text)
	if err != nil {
		if turnCtx.Err() != nil {
			return
		}
		b.reply(ctx, m.Chat.ID, "error: "+err.Error())
		return
	}
	b.reply(ctx, m.Chat.ID, out)
}

func (b *Bot) handleCallback(ctx context.Context, cq callbackQuery) {
	b.answerCallback(ctx, cq.ID)

	action, id, ok := parseCallback(cq.Data)
	if !ok || cq.Message == nil {
		return
	}
	chatID := cq.Message.Chat.ID

	b.mu.Lock()
	p := b.pending[chatID]
	b.mu.Unlock()
	if p == nil || p.id != id {
		return
	}
	select {
	case p.ch <- action == "approve":
	default:
	}
}

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	for _, chunk := range SplitMessage(text, MaxMessageLength) {
		if err := b.SendMessage(ctx, chatID, chunk); err != nil {
			return
		}
	}
}

// SendMessage sends one message. Empty text is refused rather than sent.
func (b *Bot) SendMessage(ctx context.Context, chatID int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("telegram: teks kosong")
	}
	_, err := b.post(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text})
	return err
}

func (b *Bot) EditMessage(ctx context.Context, chatID int64, messageID int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("telegram: teks kosong")
	}
	_, err := b.post(ctx, "editMessageText", map[string]any{
		"chat_id":    chatID,
		"message_id": messageID,
		"text":       text,
	})
	return err
}

func (b *Bot) answerCallback(ctx context.Context, id string) {
	_, _ = b.post(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id})
}

// AskApproval sends an inline keyboard and waits for the matching callback.
// A timeout is a deny, not an error.
func (b *Bot) AskApproval(ctx context.Context, chatID int64, tool, args string, timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		timeout = approvalTimeout
	}

	b.mu.Lock()
	b.approvalSeq++
	id := b.approvalSeq
	p := &pendingApproval{id: id, ch: make(chan bool, 1)}
	b.pending[chatID] = p
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		if b.pending[chatID] == p {
			delete(b.pending, chatID)
		}
		b.mu.Unlock()
	}()

	text := "Setujui " + tool + "?"
	if args != "" {
		text += "\n" + truncate(args, 200)
	}
	_, err := b.post(ctx, "sendMessage", map[string]any{
		"chat_id": chatID,
		"text":    text,
		"reply_markup": map[string]any{
			"inline_keyboard": [][]map[string]string{{
				{"text": "Setuju", "callback_data": fmt.Sprintf("approve:%d", id)},
				{"text": "Tolak", "callback_data": fmt.Sprintf("deny:%d", id)},
			}},
		},
	})
	if err != nil {
		return false, err
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case v := <-p.ch:
		return v, nil
	case <-timer.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      json.RawMessage `json:"result"`
	Description string          `json:"description"`
	Parameters  *struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

// post calls one Bot API method and retries once on 429, honouring retry_after.
func (b *Bot) post(ctx context.Context, method string, payload any) (json.RawMessage, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := b.apiBase + "/bot" + b.token + "/" + method

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := b.http.Do(req)
		if err != nil {
			return nil, err
		}
		raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}

		var ar apiResponse
		if err := json.Unmarshal(raw, &ar); err != nil {
			return nil, fmt.Errorf("telegram %s: respons tidak valid (HTTP %d)", method, resp.StatusCode)
		}

		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			wait := time.Duration(0)
			if ar.Parameters != nil {
				wait = time.Duration(ar.Parameters.RetryAfter) * time.Second
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode > 299 || !ar.OK {
			return nil, fmt.Errorf("telegram %s: HTTP %d: %s", method, resp.StatusCode, ar.Description)
		}
		return ar.Result, nil
	}
}

// SplitMessage cuts s into chunks of at most limit runes, preferring newline
// boundaries. No chunk is empty and none exceeds limit.
func SplitMessage(s string, limit int) []string {
	if limit <= 0 {
		limit = MaxMessageLength
	}
	r := []rune(s)
	var out []string
	for len(r) > 0 {
		if len(r) <= limit {
			if chunk := strings.TrimRight(string(r), "\n"); chunk != "" {
				out = append(out, chunk)
			}
			break
		}
		cut := limit
		for i := limit - 1; i > 0; i-- {
			if r[i] == '\n' {
				cut = i + 1
				break
			}
		}
		if chunk := strings.TrimRight(string(r[:cut]), "\n"); chunk != "" {
			out = append(out, chunk)
		}
		r = r[cut:]
		for len(r) > 0 && r[0] == '\n' {
			r = r[1:]
		}
	}
	return out
}

const helpText = "Halo. Kirim pesan dan saya kerjakan.\n" +
	"/help - bantuan ini\n" +
	"/stop - hentikan tugas yang berjalan\n" +
	"Hanya lewat DM."

func command(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return ""
	}
	cmd, _, _ := strings.Cut(fields[0], "@")
	return cmd
}

func parseCallback(data string) (action string, id int64, ok bool) {
	action, raw, found := strings.Cut(data, ":")
	if !found {
		return "", 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return "", 0, false
	}
	return action, n, true
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type message struct {
	MessageID int64  `json:"message_id"`
	Text      string `json:"text"`
	Chat      chat   `json:"chat"`
}

type chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type callbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *message `json:"message"`
}
