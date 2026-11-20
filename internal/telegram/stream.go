package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const draftRefreshInterval = 25 * time.Second

// StreamWriter streams incremental LLM deltas and tool execution status to
// one Telegram chat using Bot API 10.4 sendRichMessageDraft/sendRichMessage,
// automatically falling back to throttled SendMessage/EditMessage if needed.
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

// Finalize commits the final response permanently.
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
