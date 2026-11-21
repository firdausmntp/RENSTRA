// Package telegram implements the Telegram Bot API client used by the renstra
// gateway: long polling, per-chat turns, dedup, and inline approval.
package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	seenCap              = 2000
)

type Config struct {
	Token         string
	AllowedUsers  []int64
	PollTimeout   time.Duration
	APIBase       string
	WorkspaceDir  string
	OnSeen        func(updateID int64)
	OnAutoApprove func(chatID int64)
	OnCallback    func(ctx context.Context, chatID, msgID int64, data string)
	OnErrorLog    func(ctx context.Context, chatID int64, err error) string
	Transcribe    func(ctx context.Context, audioPath string) (string, error)
}

type Handler interface {
	Handle(ctx context.Context, chatID int64, text string) (string, error)
	Approve(ctx context.Context, chatID int64, tool string, args string) (bool, error)
}

type Bot struct {
	token         string
	apiBase       string
	pollTimeout   time.Duration
	workspaceDir  string
	allowed       map[int64]struct{}
	onSeen        func(int64)
	onAutoApprove func(int64)
	onCallback    func(context.Context, int64, int64, string)
	onErrorLog    func(context.Context, int64, error) string
	transcribe    func(context.Context, string) (string, error)
	http          *http.Client

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

type turnHandle struct {
	cancel context.CancelFunc
	gen    int64
}

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
		token:         cfg.Token,
		apiBase:       base,
		pollTimeout:   poll,
		workspaceDir:  cfg.WorkspaceDir,
		allowed:       allowed,
		onSeen:        cfg.OnSeen,
		onAutoApprove: cfg.OnAutoApprove,
		onCallback:    cfg.OnCallback,
		onErrorLog:    cfg.OnErrorLog,
		transcribe:    cfg.Transcribe,
		http:          &http.Client{Timeout: poll + 30*time.Second},
		seen:          map[int64]struct{}{},
		cancels:       map[int64]turnHandle{},
		locks:         map[int64]*chatLock{},
		pending:       map[int64]*pendingApproval{},
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
	ctx = withReplyTarget(ctx, m.MessageID, m.MessageThreadID)

	switch command(text) {
	case "/start":
		_ = b.SendMessageWithKeyboard(ctx, m.Chat.ID, StartText, DefaultQuickMenu())
		return
	case "/help":
		_ = b.SendMessageWithKeyboard(ctx, m.Chat.ID, HelpText, HelpQuickMenu())
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

	hasAttachment := hasMsgAttachment(m)
	hasReplyMedia := m.ReplyToMessage != nil && hasMsgAttachment(*m.ReplyToMessage)
	if strings.TrimSpace(text) == "" && !hasAttachment && !hasReplyMedia {
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
		var ok bool
		text, ok = b.prepareAttachmentText(ctx, turnCtx, m, text)
		if !ok {
			return
		}
	}
	if m.ReplyToMessage != nil || m.Quote != nil {
		var ok bool
		text, ok = b.appendReplyContext(ctx, turnCtx, m, text)
		if !ok {
			return
		}
	}

	out, err := h.Handle(turnCtx, m.Chat.ID, text)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(turnCtx.Err(), context.DeadlineExceeded) {
			b.reply(ctx, m.Chat.ID, FormatError(err))
			b.sendErrorLogIfAny(ctx, m.Chat.ID, err)
			return
		}
		if turnCtx.Err() != nil {
			return
		}
		b.reply(ctx, m.Chat.ID, FormatError(err))
		b.sendErrorLogIfAny(ctx, m.Chat.ID, err)
		return
	}
	b.reply(ctx, m.Chat.ID, out)
}

func (b *Bot) sendErrorLogIfAny(ctx context.Context, chatID int64, err error) {
	if b.onErrorLog == nil || err == nil || errors.Is(err, context.Canceled) {
		return
	}
	if logPath := b.onErrorLog(ctx, chatID, err); logPath != "" {
		_ = b.SendDocument(ctx, chatID, logPath, "🪵 Log error diagnostik")
	}
}
