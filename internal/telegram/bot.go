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
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// MaxMessageLength is the Telegram limit for one standard text message, in runes.
	MaxMessageLength = 4096
	// MaxRichMessageLength is the Telegram Bot API 10.1+ limit for one rich message, in runes.
	MaxRichMessageLength = 32768
	defaultAPIBase       = "https://api.telegram.org"
	defaultPoll          = 30 * time.Second
	approvalTimeout      = 120 * time.Second
	maxDownloadBytes     = 20 * 1024 * 1024
)

type Config struct {
	Token        string
	AllowedUsers []int64
	PollTimeout  time.Duration
	APIBase      string
	WorkspaceDir string
	// OnSeen is called for every update id before it is dispatched, so the
	// caller can persist it and keep dedup working across restarts.
	OnSeen     func(updateID int64)
	Transcribe func(ctx context.Context, audioPath string) (string, error)
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
	transcribe   func(context.Context, string) (string, error)
	http         *http.Client

	mu          sync.Mutex
	seen        map[int64]struct{}
	seenOrder   []int64
	cancels     map[int64]turnHandle
	locks       map[int64]*chatLock
	pending     map[int64]*pendingApproval
	approvalSeq int64
	turnSeq     int64
	draftSeq    int64
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
	id     int64
	chatID int64
	msgID  int64
	tool   string
	ch     chan bool
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
		transcribe:   cfg.Transcribe,
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

func (b *Bot) nextDraftID() int64 {
	b.mu.Lock()
	b.draftSeq++
	id := b.draftSeq
	b.mu.Unlock()
	return id
}

func (b *Bot) getUpdates(ctx context.Context, offset int64) ([]update, error) {
	raw, err := b.post(ctx, "getUpdates", map[string]any{
		"offset":          offset,
		"timeout":         int(b.pollTimeout / time.Second),
		"allowed_updates": []string{"message", "callback_query", "stopped_message_generation"},
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

	if u.StoppedMessageGeneration != nil {
		b.handleGenerationStopped(*u.StoppedMessageGeneration)
		return
	}
	if u.CallbackQuery != nil {
		b.handleCallback(ctx, *u.CallbackQuery)
		return
	}
	if u.Message != nil {
		b.handleMessage(ctx, h, *u.Message)
	}
}

func (b *Bot) handleGenerationStopped(ev messageGenerationStopped) {
	if _, ok := b.allowed[ev.Chat.ID]; !ok {
		return
	}
	b.mu.Lock()
	h, ok := b.cancels[ev.Chat.ID]
	delete(b.cancels, ev.Chat.ID)
	b.mu.Unlock()
	if ok && h.cancel != nil {
		h.cancel()
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
	case "/start":
		b.reply(ctx, m.Chat.ID, startText)
		return
	case "/help":
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

	hasAttachment := m.Document != nil || len(m.Photo) > 0 || m.Voice != nil || m.Audio != nil
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
				b.reply(ctx, m.Chat.ID, FormatError(fmt.Errorf("gagal mengunduh dokumen: %w", err)))
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
				b.reply(ctx, m.Chat.ID, FormatError(fmt.Errorf("gagal mengunduh foto: %w", err)))
				return
			}
			notes = append(notes, fmt.Sprintf("[Foto diterima: %s]", saved))
		}
		if m.Voice != nil {
			saved, err := b.DownloadFile(turnCtx, m.Voice.FileID, inbox, "voice.ogg")
			if err != nil {
				b.reply(ctx, m.Chat.ID, FormatError(fmt.Errorf("gagal mengunduh voice note: %w", err)))
				return
			}
			notes = append(notes, fmt.Sprintf("[Voice diterima: %s (durasi: %ds, mime: %s)]", saved, m.Voice.Duration, m.Voice.MimeType))
			if b.transcribe != nil {
				if tr, terr := b.transcribe(turnCtx, saved); terr == nil && strings.TrimSpace(tr) != "" {
					notes = append(notes, fmt.Sprintf("[Transkripsi suara]: %s", strings.TrimSpace(tr)))
				}
			}
		}
		if m.Audio != nil {
			name := m.Audio.FileName
			if name == "" {
				name = "audio.mp3"
			}
			saved, err := b.DownloadFile(turnCtx, m.Audio.FileID, inbox, name)
			if err != nil {
				b.reply(ctx, m.Chat.ID, FormatError(fmt.Errorf("gagal mengunduh audio: %w", err)))
				return
			}
			notes = append(notes, fmt.Sprintf("[Audio diterima: %s (durasi: %ds, mime: %s)]", saved, m.Audio.Duration, m.Audio.MimeType))
			if b.transcribe != nil {
				if tr, terr := b.transcribe(turnCtx, saved); terr == nil && strings.TrimSpace(tr) != "" {
					notes = append(notes, fmt.Sprintf("[Transkripsi suara]: %s", strings.TrimSpace(tr)))
				}
			}
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
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(turnCtx.Err(), context.DeadlineExceeded) {
			b.reply(ctx, m.Chat.ID, FormatError(err))
			return
		}
		if turnCtx.Err() != nil {
			return
		}
		b.reply(ctx, m.Chat.ID, FormatError(err))
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
	if len(b.allowed) > 0 && cq.From.ID > 0 {
		if _, ok := b.allowed[cq.From.ID]; !ok {
			return
		}
	}

	b.mu.Lock()
	p := b.pending[id]
	var msgID int64
	var toolName string
	if p != nil && p.chatID == chatID {
		msgID = p.msgID
		toolName = p.tool
	}
	b.mu.Unlock()
	if p == nil || p.chatID != chatID {
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

// SendRichMessage sends a finalized Telegram Bot API 10.1+ Rich Message using
// native Markdown blocks (up to 32,768 chars per message), falling back to
// SendMessage if the endpoint rejects rich_message.
func (b *Bot) SendRichMessage(ctx context.Context, chatID int64, markdown string) error {
	if strings.TrimSpace(markdown) == "" {
		return errors.New("telegram: teks kosong")
	}
	for _, chunk := range SplitMessage(markdown, MaxRichMessageLength) {
		_, err := b.post(ctx, "sendRichMessage", map[string]any{
			"chat_id": chatID,
			"rich_message": map[string]any{
				"markdown": chunk,
			},
		})
		if err != nil {
			b.reply(ctx, chatID, chunk)
		}
	}
	return nil
}

// SendRichMessageDraft streams a partial Bot API 10.1-10.4 Rich Message draft
// to a private chat. Setting canStop displays a native Stop button that emits
// a stopped_message_generation update when pressed.
func (b *Bot) SendRichMessageDraft(ctx context.Context, chatID, draftID int64, markdown string, canStop, keepOnStop bool) error {
	if strings.TrimSpace(markdown) == "" {
		return errors.New("telegram: teks kosong")
	}
	_, err := b.post(ctx, "sendRichMessageDraft", map[string]any{
		"chat_id":  chatID,
		"draft_id": draftID,
		"rich_message": map[string]any{
			"markdown": markdown,
		},
		"can_stop":     canStop,
		"keep_on_stop": keepOnStop,
	})
	return err
}

// SendMessageDraft streams a partial Bot API 9.3-10.4 text draft to a private chat.
func (b *Bot) SendMessageDraft(ctx context.Context, chatID, draftID int64, text string, canStop, keepOnStop bool) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("telegram: teks kosong")
	}
	_, err := b.post(ctx, "sendMessageDraft", map[string]any{
		"chat_id":      chatID,
		"draft_id":     draftID,
		"text":         markdownToHTML(text),
		"parse_mode":   "HTML",
		"can_stop":     canStop,
		"keep_on_stop": keepOnStop,
	})
	return err
}

// SendMessageReturningID sends one message and returns its Telegram message_id.
// It formats common Markdown into Telegram HTML and falls back to plain text
// if Telegram rejects entity parsing.
func (b *Bot) SendMessageReturningID(ctx context.Context, chatID int64, text string) (int64, error) {
	if strings.TrimSpace(text) == "" {
		return 0, errors.New("telegram: teks kosong")
	}
	raw, err := b.post(ctx, "sendMessage", map[string]any{
		"chat_id":    chatID,
		"text":       markdownToHTML(text),
		"parse_mode": "HTML",
	})
	if isHTTP400(err) {
		raw, err = b.post(ctx, "sendMessage", map[string]any{"chat_id": chatID, "text": text})
	}
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

// BotCommand describes one Telegram slash command shown in the chat menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// DefaultCommands returns the slash command menu registered by renstra serve.
func DefaultCommands() []BotCommand {
	return []BotCommand{
		{Command: "start", Description: "Mulai & tampilkan sambutan"},
		{Command: "help", Description: "Panduan lengkap semua perintah"},
		{Command: "new", Description: "Mulai sesi baru (potong riwayat lama)"},
		{Command: "restart", Description: "Restart sesi & potong riwayat lama"},
		{Command: "stop", Description: "Hentikan tugas yang sedang berjalan"},
		{Command: "model", Description: "Cek atau ganti model aktif"},
		{Command: "skills", Description: "Daftar skill aktif (/skills reload)"},
		{Command: "memory", Description: "Lihat catatan memori & profil user"},
		{Command: "cron", Description: "Kelola jadwal otomatis"},
		{Command: "status", Description: "Info status sesi, model, konteks & biaya"},
		{Command: "usage", Description: "Cek token & estimasi biaya hari ini"},
		{Command: "insights", Description: "Ringkasan pemakaian harian, 7 & 30 hari"},
		{Command: "compact", Description: "Kompaksi ringkasan riwayat sekarang"},
		{Command: "version", Description: "Tampilkan versi binary RENSTRA"},
	}
}

// SetMyCommands registers the bot's slash command menu via Telegram setMyCommands.
func (b *Bot) SetMyCommands(ctx context.Context, cmds []BotCommand) error {
	_, err := b.post(ctx, "setMyCommands", map[string]any{"commands": cmds})
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
		"text":       markdownToHTML(text),
		"parse_mode": "HTML",
	})
	if err != nil {
		var ae *apiError
		if errors.As(err, &ae) {
			if strings.Contains(strings.ToLower(ae.description), "message is not modified") {
				return nil
			}
			if ae.status == http.StatusBadRequest {
				_, err = b.post(ctx, "editMessageText", map[string]any{
					"chat_id":    chatID,
					"message_id": messageID,
					"text":       text,
				})
				if err != nil && errors.As(err, &ae) && strings.Contains(strings.ToLower(ae.description), "message is not modified") {
					return nil
				}
			}
		}
	}
	return err
}

// SendFile sends a local file to chatID as a photo or document depending on kind/extension.
func (b *Bot) SendFile(ctx context.Context, chatID int64, path, caption, kind string) error {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "photo":
		return b.SendPhoto(ctx, chatID, path, caption)
	case "document":
		return b.SendDocument(ctx, chatID, path, caption)
	default:
		switch strings.ToLower(filepath.Ext(path)) {
		case ".jpg", ".jpeg", ".png", ".webp", ".gif":
			return b.SendPhoto(ctx, chatID, path, caption)
		default:
			return b.SendDocument(ctx, chatID, path, caption)
		}
	}
}

// SendDocument uploads and sends a document file to a Telegram chat.
func (b *Bot) SendDocument(ctx context.Context, chatID int64, filePath, caption string) error {
	fields := map[string]string{"chat_id": fmt.Sprintf("%d", chatID)}
	if strings.TrimSpace(caption) != "" {
		trimmed := truncate(caption, 1024)
		fields["caption"] = markdownToHTML(trimmed)
		fields["parse_mode"] = "HTML"
	}
	_, err := b.postMultipart(ctx, "sendDocument", fields, "document", filePath)
	if isHTTP400(err) && fields["parse_mode"] != "" {
		fields["caption"] = truncate(caption, 1024)
		delete(fields, "parse_mode")
		_, err = b.postMultipart(ctx, "sendDocument", fields, "document", filePath)
	}
	return err
}

// SendPhoto uploads and sends a photo file to a Telegram chat.
func (b *Bot) SendPhoto(ctx context.Context, chatID int64, filePath, caption string) error {
	fields := map[string]string{"chat_id": fmt.Sprintf("%d", chatID)}
	if strings.TrimSpace(caption) != "" {
		trimmed := truncate(caption, 1024)
		fields["caption"] = markdownToHTML(trimmed)
		fields["parse_mode"] = "HTML"
	}
	_, err := b.postMultipart(ctx, "sendPhoto", fields, "photo", filePath)
	if isHTTP400(err) && fields["parse_mode"] != "" {
		fields["caption"] = truncate(caption, 1024)
		delete(fields, "parse_mode")
		_, err = b.postMultipart(ctx, "sendPhoto", fields, "photo", filePath)
	}
	return err
}

func (b *Bot) postMultipart(ctx context.Context, method string, fields map[string]string, fileField, filePath string) (json.RawMessage, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, err
		}
	}
	part, err := mw.CreateFormFile(fileField, filepath.Base(filePath))
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	if err := mw.Close(); err != nil {
		return nil, err
	}

	endpoint := b.apiBase + "/bot" + b.token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

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
	if resp.StatusCode < 200 || resp.StatusCode > 299 || !ar.OK {
		return nil, fmt.Errorf("telegram %s: HTTP %d: %s", method, resp.StatusCode, ar.Description)
	}
	return ar.Result, nil
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
	p := &pendingApproval{id: id, chatID: chatID, tool: tool, ch: make(chan bool, 1)}
	b.pending[id] = p
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		if b.pending[id] == p {
			delete(b.pending, id)
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

	editOutcome := func(status string) {
		if sent.MessageID <= 0 {
			return
		}
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.EditMessage(saveCtx, chatID, sent.MessageID, status)
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case v := <-p.ch:
		return v, nil
	case <-timer.C:
		editOutcome("⌛ Approval kedaluwarsa: " + tool)
		return false, nil
	case <-ctx.Done():
		editOutcome("🛑 Approval dibatalkan: " + tool)
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

type apiError struct {
	method      string
	status      int
	description string
}

func (e *apiError) Error() string {
	return fmt.Sprintf("telegram %s: HTTP %d: %s", e.method, e.status, e.description)
}

func isHTTP400(err error) bool {
	var ae *apiError
	return errors.As(err, &ae) && ae.status == http.StatusBadRequest
}
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
			return nil, &apiError{method: method, status: resp.StatusCode, description: ar.Description}
		}
		return ar.Result, nil
	}
}

type update struct {
	UpdateID                 int64                     `json:"update_id"`
	Message                  *message                  `json:"message"`
	CallbackQuery            *callbackQuery            `json:"callback_query"`
	StoppedMessageGeneration *messageGenerationStopped `json:"stopped_message_generation"`
}

type messageGenerationStopped struct {
	Chat            chat  `json:"chat"`
	MessageThreadID int64 `json:"message_thread_id"`
	DraftID         int64 `json:"draft_id"`
}

type message struct {
	MessageID int64       `json:"message_id"`
	Text      string      `json:"text"`
	Caption   string      `json:"caption"`
	Chat      chat        `json:"chat"`
	Document  *tgDocument `json:"document"`
	Photo     []tgPhoto   `json:"photo"`
	Voice     *tgVoice    `json:"voice"`
	Audio     *tgAudio    `json:"audio"`
}

type tgVoice struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

type tgAudio struct {
	FileID   string `json:"file_id"`
	Duration int    `json:"duration"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
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
	From    tgUser   `json:"from"`
	Data    string   `json:"data"`
	Message *message `json:"message"`
}

type tgUser struct {
	ID int64 `json:"id"`
}

// StreamWriter streams incremental LLM deltas and tool execution status to
// one Telegram chat using Bot API 10.4 sendRichMessageDraft/sendRichMessage,
// automatically falling back to throttled SendMessage/EditMessage if needed.
const draftRefreshInterval = 25 * time.Second

type StreamWriter struct {
	bot     *Bot
	chatID  int64
	draftID int64
	turnCtx context.Context
	cancel  context.CancelFunc

	mu          sync.Mutex
	buf         strings.Builder
	toolNote    string
	lastEdit    time.Time
	lastSent    string
	closed      bool
	keepaliveOn bool

	sendMu      sync.Mutex
	msgID       int64
	usedDraft   bool
	draftFailed bool
}

func NewStreamWriter(bot *Bot, chatID int64) *StreamWriter {
	return NewStreamWriterWithContext(context.Background(), bot, chatID)
}

func NewStreamWriterWithContext(ctx context.Context, bot *Bot, chatID int64) *StreamWriter {
	if ctx == nil {
		ctx = context.Background()
	}
	wctx, cancel := context.WithCancel(ctx)
	var draftID int64 = 1
	if bot != nil {
		draftID = bot.nextDraftID()
	}
	return &StreamWriter{bot: bot, chatID: chatID, draftID: draftID, turnCtx: wctx, cancel: cancel}
}

// Cancel marks the stream writer closed and aborts any in-flight flush request.
func (s *StreamWriter) Cancel() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

func (s *StreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	if s.closed || s.turnCtx.Err() != nil {
		s.mu.Unlock()
		return len(p), nil
	}
	s.buf.Write(p)
	s.toolNote = ""
	preview := s.renderLocked()
	now := time.Now()
	staleDraft := s.usedDraft && !s.draftFailed && !s.lastEdit.IsZero() && now.Sub(s.lastEdit) >= draftRefreshInterval
	shouldFlush := preview != "" && ((preview != s.lastSent &&
		(s.lastEdit.IsZero() || now.Sub(s.lastEdit) >= time.Second)) || staleDraft)
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
	if s.closed || s.turnCtx.Err() != nil {
		s.mu.Unlock()
		return
	}
	s.toolNote = fmt.Sprintf("⏳ Menjalankan `%s`...", name)
	preview := s.renderLocked()
	now := time.Now()
	staleDraft := s.usedDraft && !s.draftFailed && !s.lastEdit.IsZero() && now.Sub(s.lastEdit) >= draftRefreshInterval
	shouldFlush := preview != "" && ((preview != s.lastSent &&
		(s.lastEdit.IsZero() || now.Sub(s.lastEdit) >= 800*time.Millisecond)) || staleDraft)
	if shouldFlush {
		s.lastEdit = now
		s.lastSent = preview
	}
	s.mu.Unlock()

	if shouldFlush {
		s.flush(preview)
	}
}

// Finalize commits the final response permanently (via sendRichMessage when
// draft streaming was used, or by editing the placeholder message). Returns true
// if the final response was already delivered, or false if the caller should send normally.
func (s *StreamWriter) Finalize(ctx context.Context, finalReply string) bool {
	s.mu.Lock()
	s.closed = true
	lastSent := s.lastSent
	s.mu.Unlock()
	if s.cancel != nil {
		defer s.cancel()
	}

	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	if s.bot == nil || ctx.Err() != nil {
		return false
	}
	if s.usedDraft {
		if strings.TrimSpace(finalReply) == "" {
			return true
		}
		_ = s.bot.SendRichMessage(ctx, s.chatID, finalReply)
		return true
	}
	if s.msgID == 0 {
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
	limit := MaxRichMessageLength
	if s.draftFailed {
		limit = MaxMessageLength
	}
	if len([]rune(combined)) > limit {
		return truncate(combined, limit-2)
	}
	return combined
}

func (s *StreamWriter) flush(text string) {
	if s.bot == nil || strings.TrimSpace(text) == "" {
		return
	}
	if s.turnCtx != nil && s.turnCtx.Err() != nil {
		return
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed || (s.turnCtx != nil && s.turnCtx.Err() != nil) {
		return
	}

	base := s.turnCtx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, 10*time.Second)
	defer cancel()

	if !s.draftFailed {
		if err := s.bot.SendRichMessageDraft(ctx, s.chatID, s.draftID, text, true, true); err == nil {
			s.usedDraft = true
			s.ensureKeepalive()
			return
		}
		if base.Err() != nil {
			return
		}
		s.draftFailed = true
		if len([]rune(text)) > MaxMessageLength {
			text = truncate(text, MaxMessageLength-2)
		}
	}

	if s.msgID == 0 {
		id, err := s.bot.SendMessageReturningID(ctx, s.chatID, text)
		if err == nil && id > 0 {
			s.msgID = id
		}
		return
	}
	_ = s.bot.EditMessage(ctx, s.chatID, s.msgID, text)
}

func (s *StreamWriter) ensureKeepalive() {
	s.mu.Lock()
	if s.keepaliveOn || s.closed || s.turnCtx == nil || s.turnCtx.Done() == nil {
		s.mu.Unlock()
		return
	}
	s.keepaliveOn = true
	s.mu.Unlock()

	go func() {
		t := time.NewTicker(draftRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-s.turnCtx.Done():
				return
			case <-t.C:
				s.mu.Lock()
				if s.closed || s.draftFailed || !s.usedDraft {
					s.mu.Unlock()
					return
				}
				since := time.Since(s.lastEdit)
				preview := s.renderLocked()
				if since >= draftRefreshInterval && preview != "" {
					s.lastEdit = time.Now()
					s.lastSent = preview
					s.mu.Unlock()
					s.flush(preview)
					continue
				}
				s.mu.Unlock()
			}
		}
	}()
}
