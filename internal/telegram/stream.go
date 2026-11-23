package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	draftRefreshInterval = 25 * time.Second
	toolProgressInterval = 15 * time.Second
)

// StreamWriter streams incremental LLM deltas and tool execution status to
// one Telegram chat using Bot API 10.4 sendRichMessageDraft/sendRichMessage,
// automatically falling back to throttled SendMessage/EditMessage if needed.
type StreamWriter struct {
	bot     *Bot
	chatID  int64
	draftID int64
	turnCtx context.Context
	cancel  context.CancelFunc

	mu            sync.Mutex
	buf           strings.Builder
	toolNote      string
	toolStartedAt time.Time
	lastEdit      time.Time
	lastSent      string
	closed        bool
	keepaliveOn   bool

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
	s.toolStartedAt = time.Time{}
	if !hasMeaningfulStreamText(s.buf.String()) {
		s.mu.Unlock()
		return len(p), nil
	}
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

// OnTool updates the streaming message with the name and brief target of the running tool.
func (s *StreamWriter) OnTool(name, args string) {
	s.mu.Lock()
	if s.closed || s.turnCtx.Err() != nil {
		s.mu.Unlock()
		return
	}
	s.toolNote = formatToolNote(name, args)
	s.toolStartedAt = time.Now()
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
	startIdx := 1
	if chunks[0] != lastSent {
		if err := s.bot.EditMessage(ctx, s.chatID, s.msgID, chunks[0]); err != nil &&
			!strings.Contains(strings.ToLower(err.Error()), "message is not modified") {
			startIdx = 0
		}
	}
	for _, chunk := range chunks[startIdx:] {
		if err := s.bot.SendMessage(ctx, s.chatID, chunk); err != nil {
			break
		}
	}
	return true
}

func (s *StreamWriter) renderLocked() string {
	txt := strings.TrimSpace(s.buf.String())
	note := strings.TrimSpace(s.toolNote)
	if note != "" && !s.toolStartedAt.IsZero() {
		if elapsed := time.Since(s.toolStartedAt).Truncate(time.Second); elapsed >= 5*time.Second {
			note = fmt.Sprintf("%s (%s)", note, elapsed)
		}
	}
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
			s.ensureKeepalive()
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
		t := time.NewTicker(toolProgressInterval)
		defer t.Stop()
		for {
			select {
			case <-s.turnCtx.Done():
				return
			case <-t.C:
				s.mu.Lock()
				if s.closed || (!s.usedDraft && s.msgID == 0) {
					s.mu.Unlock()
					return
				}
				interval := draftRefreshInterval
				if s.toolNote != "" && !s.toolStartedAt.IsZero() {
					interval = toolProgressInterval
				}
				since := time.Since(s.lastEdit)
				preview := s.renderLocked()
				if since >= interval && preview != "" && (preview != s.lastSent || (s.usedDraft && !s.draftFailed)) {
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

// hasMeaningfulStreamText avoids flushing lone markdown prefixes like ">" or "**"
// before actual alphanumeric content arrives.
func hasMeaningfulStreamText(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) >= 16 {
		return true
	}
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func formatToolNote(name, args string) string {
	detail := summarizeToolArg(name, args)
	if detail != "" {
		return fmt.Sprintf("⏳ Menjalankan `%s`: `%s`...", name, detail)
	}
	return fmt.Sprintf("⏳ Menjalankan `%s`...", name)
}

func summarizeToolArg(name, rawArgs string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(rawArgs), &m); err != nil {
		return ""
	}
	var val string
	switch name {
	case "terminal":
		val, _ = m["cmd"].(string)
	case "read_file", "write_file", "patch", "send_file":
		val, _ = m["path"].(string)
	case "web_fetch":
		val, _ = m["url"].(string)
	case "web_search":
		val, _ = m["query"].(string)
	case "delegate":
		val, _ = m["task"].(string)
	case "send_telegram":
		if tid, ok := m["topic_id"].(float64); ok && tid > 0 {
			val = fmt.Sprintf("topik %d", int64(tid))
		}
	}
	val = strings.Join(strings.Fields(val), " ")
	if val == "" {
		return ""
	}
	val = strings.ReplaceAll(val, "`", "'")
	return truncate(val, 60)
}
