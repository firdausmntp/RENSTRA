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
	"os"
	"path/filepath"
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
	maxDownloadBytes = 20 * 1024 * 1024
)

type Config struct {
	Token        string
	AllowedUsers []int64
	PollTimeout  time.Duration
	APIBase      string
	WorkspaceDir string
	// OnSeen is called for every update id before it is dispatched, so the
	// caller can persist it and keep dedup working across restarts.
	OnSeen func(updateID int64)
}

type Handler interface {
	Handle(ctx context.Context, chatID int64, text string) (string, error)
	Approve(ctx context.Context, chatID int64, tool string, args string) (bool, error)
}

type Bot struct {
	token        string
	apiBase      string
	pollTimeout  time.Duration
	workspaceDir string
	allowed      map[int64]struct{}
	onSeen       func(int64)
	http         *http.Client

	mu          sync.Mutex
	seen        map[int64]struct{}
	seenOrder   []int64
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
	id    int64
	msgID int64
	tool  string
	ch    chan bool
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
		token:        cfg.Token,
		apiBase:      base,
		pollTimeout:  poll,
		workspaceDir: cfg.WorkspaceDir,
		allowed:      allowed,
		onSeen:       cfg.OnSeen,
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
	b.markSeenLocked(id)
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

// seenCap bounds the in-memory dedup set. Updates older than the Telegram
// retention window are already pruned from the store, so dropping the oldest
// in-memory ids is safe.
const seenCap = 2000

func (b *Bot) markSeenLocked(id int64) {
	b.seen[id] = struct{}{}
	b.seenOrder = append(b.seenOrder, id)
	if len(b.seenOrder) > seenCap {
		drop := b.seenOrder[0]
		b.seenOrder = b.seenOrder[1:]
		delete(b.seen, drop)
	}
}

func (b *Bot) handleUpdate(ctx context.Context, h Handler, u update) {
	b.mu.Lock()
	if _, dup := b.seen[u.UpdateID]; dup {
		b.mu.Unlock()
		return
	}
	b.markSeenLocked(u.UpdateID)
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

	text := m.Text
	if text == "" {
		text = m.Caption
	}

	switch command(text) {
	case "/start", "/help":
		b.reply(ctx, m.Chat.ID, helpText)
		return
	case "/stop":
		b.mu.Lock()
		ch, ok := b.cancels[m.Chat.ID]
		delete(b.cancels, m.Chat.ID)
		b.mu.Unlock()
		if !ok || ch.cancel == nil {
			b.reply(ctx, m.Chat.ID, "tidak ada yang berjalan")
			return
		}
		ch.cancel()
		b.reply(ctx, m.Chat.ID, "dihentikan")
		return
	}

	hasAttachment := m.Document != nil || len(m.Photo) > 0
	if strings.TrimSpace(text) == "" && !hasAttachment {
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

	typingDone := make(chan struct{})
	go func() {
		_ = b.SendChatAction(turnCtx, m.Chat.ID, "typing")
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-typingDone:
				return
			case <-turnCtx.Done():
				return
			case <-ticker.C:
				_ = b.SendChatAction(turnCtx, m.Chat.ID, "typing")
			}
		}
	}()
	defer close(typingDone)

	if hasAttachment {
		inbox := b.workspaceDir
		if inbox == "" {
			inbox = os.TempDir()
		} else {
			inbox = filepath.Join(inbox, "inbox")
		}
		var notes []string
		if m.Document != nil {
			saved, err := b.DownloadFile(turnCtx, m.Document.FileID, inbox, m.Document.FileName)
			if err != nil {
				b.reply(ctx, m.Chat.ID, "error: gagal mengunduh dokumen: "+err.Error())
				return
			}
			if m.Document.MimeType != "" {
				notes = append(notes, fmt.Sprintf("[File diterima: %s (mime: %s)]", saved, m.Document.MimeType))
			} else {
				notes = append(notes, fmt.Sprintf("[File diterima: %s]", saved))
			}
		}
		if len(m.Photo) > 0 {
			best := m.Photo[len(m.Photo)-1]
			for _, ph := range m.Photo {
				if ph.FileSize > best.FileSize || (ph.Width*ph.Height > best.Width*best.Height && ph.FileSize == best.FileSize) {
					best = ph
				}
			}
			saved, err := b.DownloadFile(turnCtx, best.FileID, inbox, "")
			if err != nil {
				b.reply(ctx, m.Chat.ID, "error: gagal mengunduh foto: "+err.Error())
				return
			}
			notes = append(notes, fmt.Sprintf("[Foto diterima: %s]", saved))
		}
		prefix := strings.Join(notes, "\n")
		if strings.TrimSpace(text) != "" {
			text = prefix + "\n" + text
		} else {
			text = prefix + "\nUser mengirim file terlampir."
		}
	}

	out, err := h.Handle(turnCtx, m.Chat.ID, text)
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
	var msgID int64
	var toolName string
	if p != nil && p.id == id {
		msgID = p.msgID
		toolName = p.tool
	}
	b.mu.Unlock()
	if p == nil || p.id != id {
		return
	}
	approved := action == "approve"
	select {
	case p.ch <- approved:
	default:
	}
	if msgID > 0 {
		status := "❌ Ditolak: " + toolName
		if approved {
			status = "✅ Disetujui: " + toolName
		}
		_ = b.EditMessage(ctx, chatID, msgID, status)
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
	_, err := b.SendMessageReturningID(ctx, chatID, text)
	return err
}

// SendMessageReturningID sends one message and returns its Telegram message_id.
func (b *Bot) SendMessageReturningID(ctx context.Context, chatID int64, text string) (int64, error) {
	if strings.TrimSpace(text) == "" {
		return 0, errors.New("telegram: teks kosong")
	}
	raw, err := b.post(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text})
	if err != nil {
		return 0, err
	}
	var res struct {
		MessageID int64 `json:"message_id"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &res)
	}
	return res.MessageID, nil
}

// SendChatAction sends a chat action such as "typing" to Telegram.
func (b *Bot) SendChatAction(ctx context.Context, chatID int64, action string) error {
	if action == "" {
		action = "typing"
	}
	_, err := b.post(ctx, "sendChatAction", map[string]any{"chat_id": chatID, "action": action})
	return err
}

// DownloadFile fetches a Telegram file via getFile and saves it inside destDir.
func (b *Bot) DownloadFile(ctx context.Context, fileID, destDir, preferredName string) (string, error) {
	if strings.TrimSpace(fileID) == "" {
		return "", errors.New("telegram: file_id kosong")
	}
	raw, err := b.post(ctx, "getFile", map[string]any{"file_id": fileID})
	if err != nil {
		return "", err
	}
	var gf struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal(raw, &gf); err != nil || strings.TrimSpace(gf.FilePath) == "" {
		return "", errors.New("telegram: file_path tidak ditemukan")
	}

	fileURL := b.apiBase + "/file/bot" + b.token + "/" + strings.TrimLeft(gf.FilePath, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := b.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("telegram download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownloadBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxDownloadBytes {
		return "", fmt.Errorf("file melebihi batas %d MB", maxDownloadBytes/(1024*1024))
	}

	name := sanitizeFilename(preferredName)
	if strings.TrimSpace(preferredName) == "" {
		name = sanitizeFilename(gf.FilePath)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(destDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), name))
	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", err
	}
	if abs, err := filepath.Abs(dest); err == nil {
		return abs, nil
	}
	return dest, nil
}

func sanitizeFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '_'
		}
		if r < 0x20 {
			return '_'
		}
		return r
	}, base)
	cleaned = strings.Trim(cleaned, ". ")
	if cleaned == "" {
		return "file"
	}
	return cleaned
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
	p := &pendingApproval{id: id, tool: tool, ch: make(chan bool, 1)}
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
		text += "\n" + truncate(args, 1000)
	}
	raw, err := b.post(ctx, "sendMessage", map[string]any{
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
	var sent struct {
		MessageID int64 `json:"message_id"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &sent) == nil && sent.MessageID > 0 {
		b.mu.Lock()
		p.msgID = sent.MessageID
		b.mu.Unlock()
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

type update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *message       `json:"message"`
	CallbackQuery *callbackQuery `json:"callback_query"`
}

type message struct {
	MessageID int64       `json:"message_id"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Chat      chat        `json:"chat"`
	Document  *tgDocument `json:"document"`
	Photo     []tgPhoto   `json:"photo"`
}

type tgDocument struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type tgPhoto struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
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

// StreamWriter streams incremental LLM deltas and tool execution status to
// one Telegram chat using throttled message edits.
type StreamWriter struct {
	bot    *Bot
	chatID int64

	mu       sync.Mutex
	buf      strings.Builder
	toolNote string
	lastEdit time.Time
	lastSent string
	closed   bool

	sendMu sync.Mutex
	msgID  int64
}

func NewStreamWriter(bot *Bot, chatID int64) *StreamWriter {
	return &StreamWriter{bot: bot, chatID: chatID}
}

func (s *StreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return len(p), nil
	}
	s.buf.Write(p)
	s.toolNote = ""
	preview := s.renderLocked()
	now := time.Now()
	shouldFlush := preview != "" && preview != s.lastSent &&
		(s.lastEdit.IsZero() || now.Sub(s.lastEdit) >= time.Second)
	if shouldFlush {
		s.lastEdit = now
		s.lastSent = preview
	}
	s.mu.Unlock()

	if shouldFlush {
		s.flush(preview)
	}
	return len(p), nil
}

// OnTool updates the streaming message with the name of the tool currently running.
func (s *StreamWriter) OnTool(name, args string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.toolNote = fmt.Sprintf("⏳ Menjalankan `%s`...", name)
	preview := s.renderLocked()
	now := time.Now()
	shouldFlush := preview != "" && preview != s.lastSent &&
		(s.lastEdit.IsZero() || now.Sub(s.lastEdit) >= 800*time.Millisecond)
	if shouldFlush {
		s.lastEdit = now
		s.lastSent = preview
	}
	s.mu.Unlock()

	if shouldFlush {
		s.flush(preview)
	}
}

// Finalize updates the streamed message with the complete finalReply (splitting
// into extra messages if needed). Returns true if a streamed message was
// already sent and finalized, or false if the caller should send normally.
func (s *StreamWriter) Finalize(ctx context.Context, finalReply string) bool {
	s.mu.Lock()
	s.closed = true
	lastSent := s.lastSent
	s.mu.Unlock()

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if s.msgID == 0 || s.bot == nil {
		return false
	}
	chunks := SplitMessage(finalReply, MaxMessageLength)
	if len(chunks) == 0 {
		return true
	}
	if chunks[0] != lastSent {
		_ = s.bot.EditMessage(ctx, s.chatID, s.msgID, chunks[0])
	}
	for _, chunk := range chunks[1:] {
		if err := s.bot.SendMessage(ctx, s.chatID, chunk); err != nil {
			break
		}
	}
	return true
}

func (s *StreamWriter) renderLocked() string {
	txt := strings.TrimSpace(s.buf.String())
	note := strings.TrimSpace(s.toolNote)
	var combined string
	switch {
	case txt != "" && note != "":
		combined = txt + "\n\n" + note
	case txt != "":
		combined = txt
	case note != "":
		combined = note
	default:
		return ""
	}
	if len([]rune(combined)) > MaxMessageLength {
		return truncate(combined, MaxMessageLength-2)
	}
	return combined
}

func (s *StreamWriter) flush(text string) {
	if s.bot == nil || strings.TrimSpace(text) == "" {
		return
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if s.msgID == 0 {
		id, err := s.bot.SendMessageReturningID(ctx, s.chatID, text)
		if err == nil && id > 0 {
			s.msgID = id
		}
		return
	}
	_ = s.bot.EditMessage(ctx, s.chatID, s.msgID, text)
}
