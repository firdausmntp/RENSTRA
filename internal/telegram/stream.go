package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
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
	startedAt     time.Time
	iter          int
	maxIter       int
	recentSteps   []string
	currentTool   string
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

// ResetStepText clears the current step's streamed text buffer (used before a
// provider retry/fallback so partial output from a failed attempt is never duplicated).
func (s *StreamWriter) ResetStepText() {
	s.mu.Lock()
	s.buf.Reset()
	s.mu.Unlock()
}

// SetStep records the current agent iteration and resets the live step buffer
// on subsequent iterations so old intermediate narration does not pile up into
// a repetitive wall of text.
func (s *StreamWriter) SetStep(iter, maxIter int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.startedAt.IsZero() {
		s.startedAt = time.Now()
	}
	if iter > 1 && s.iter > 0 && iter != s.iter {
		s.buf.Reset()
	}
	s.iter = iter
	s.maxIter = maxIter
}

// OnTool updates the streaming message with the name and brief target of the running tool.
func (s *StreamWriter) OnTool(name, args string) {
	s.mu.Lock()
	if s.closed || s.turnCtx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if s.startedAt.IsZero() {
		s.startedAt = time.Now()
	}
	if s.currentTool != "" {
		doneStep := "✓ " + s.currentTool
		if !s.toolStartedAt.IsZero() {
			if el := time.Since(s.toolStartedAt).Truncate(time.Second); el >= time.Second {
				doneStep += fmt.Sprintf(" (%s)", el)
			}
		}
		s.recentSteps = append(s.recentSteps, doneStep)
		if len(s.recentSteps) > 4 {
			s.recentSteps = s.recentSteps[len(s.recentSteps)-4:]
		}
	}
	detail := summarizeToolArg(name, args)
	if detail != "" {
		s.currentTool = fmt.Sprintf("%s: %s", name, detail)
	} else {
		s.currentTool = name
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
	if strings.TrimSpace(finalReply) == "" {
		_ = s.bot.EditMessage(ctx, s.chatID, s.msgID, "✅ Selesai (laporan terkirim).")
		return true
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

// CleanupOnError deletes or replaces an in-flight status message when a turn fails,
// so stale "⏳ Menjalankan..." drafts are never left hanging in chat.
func (s *StreamWriter) CleanupOnError(ctx context.Context) {
	s.Cancel()
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.bot == nil || s.msgID == 0 || s.usedDraft {
		return
	}
	_ = s.bot.DeleteMessage(ctx, s.chatID, s.msgID)
}

func (s *StreamWriter) renderLocked() string {
	txt := dedupeConsecutiveLines(strings.TrimSpace(s.buf.String()))
	note := strings.TrimSpace(s.toolNote)
	if note != "" && !s.toolStartedAt.IsZero() {
		if elapsed := time.Since(s.toolStartedAt).Truncate(time.Second); elapsed >= 5*time.Second {
			note = fmt.Sprintf("%s (%s)", note, elapsed)
		}
	}
	var statusLines []string
	if s.iter > 0 && (note != "" || len(s.recentSteps) > 0) {
		el := ""
		if !s.startedAt.IsZero() {
			el = " · " + time.Since(s.startedAt).Truncate(time.Second).String()
		}
		if s.maxIter > 0 {
			statusLines = append(statusLines, fmt.Sprintf("🧭 Langkah %d/%d%s:", s.iter, s.maxIter, el))
		} else {
			statusLines = append(statusLines, fmt.Sprintf("🧭 Langkah %d%s:", s.iter, el))
		}
	}
	for _, st := range s.recentSteps {
		statusLines = append(statusLines, st)
	}
	if note != "" {
		statusLines = append(statusLines, note)
	}
	statusBlock := strings.Join(statusLines, "\n")

	limit := MaxRichMessageLength
	if s.draftFailed {
		limit = MaxMessageLength
	}
	switch {
	case txt != "" && statusBlock != "":
		// Always preserve the live status block at the bottom by trimming txt first.
		statusRunes := len([]rune(statusBlock))
		budget := limit - statusRunes - 6
		if budget <= 40 {
			return truncate(statusBlock, limit-2)
		}
		if len([]rune(txt)) > budget {
			r := []rune(txt)
			txt = "…\n" + strings.TrimSpace(string(r[len(r)-(budget-2):]))
		}
		return txt + "\n\n" + statusBlock
	case txt != "":
		if len([]rune(txt)) > limit {
			return truncate(txt, limit-2)
		}
		return txt
	case statusBlock != "":
		if len([]rune(statusBlock)) > limit {
			return truncate(statusBlock, limit-2)
		}
		return statusBlock
	default:
		return ""
	}
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
