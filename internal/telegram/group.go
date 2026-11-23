package telegram

import (
	"context"
	"errors"
	"strings"
)

func turnScopeKey(chatID, threadID int64) int64 {
	if threadID <= 0 {
		return chatID
	}
	// Mix chatID and threadID into a distinct non-colliding key so different
	// forum topics in the same supergroup run independently in parallel.
	return (chatID * 1_000_003) ^ (threadID * 97_531)
}

// RegisterCancel stores the cancel func for the active turn of a chat (thread 0).
func (b *Bot) RegisterCancel(chatID int64, cancel context.CancelFunc) int64 {
	return b.RegisterTopicCancel(chatID, 0, cancel)
}

// RegisterTopicCancel stores the cancel func for a specific (chatID, threadID) scope.
func (b *Bot) RegisterTopicCancel(chatID, threadID int64, cancel context.CancelFunc) int64 {
	key := turnScopeKey(chatID, threadID)
	b.mu.Lock()
	b.turnSeq++
	gen := b.turnSeq
	b.cancels[key] = turnHandle{cancel: cancel, gen: gen}
	b.mu.Unlock()
	return gen
}

func (b *Bot) clearTopicCancel(chatID, threadID, gen int64) {
	key := turnScopeKey(chatID, threadID)
	b.mu.Lock()
	if cur, ok := b.cancels[key]; ok && cur.gen == gen {
		delete(b.cancels, key)
	}
	b.mu.Unlock()
}

func (b *Bot) stopActiveTurn(chatID, threadID int64) bool {
	key := turnScopeKey(chatID, threadID)
	b.mu.Lock()
	delete(b.queued, key)
	ch, ok := b.cancels[key]
	if ok {
		delete(b.cancels, key)
	} else if threadID > 0 {
		delete(b.queued, chatID)
		ch, ok = b.cancels[chatID]
		if ok {
			delete(b.cancels, chatID)
		}
	}
	b.mu.Unlock()
	if !ok || ch.cancel == nil {
		return false
	}
	ch.cancel()
	return true
}

func (b *Bot) handleGenerationStopped(ev messageGenerationStopped) {
	if !b.isChatOrUserAllowed(ev.Chat.ID, nil) {
		return
	}
	b.stopActiveTurn(ev.Chat.ID, ev.MessageThreadID)
}

// enqueueOrAcquire queues incoming messages while a turn is actively running
// (Hermes-style coalesced follow-up instead of killing the active turn), then
// coalesces all queued messages once the lock is acquired.
func (b *Bot) enqueueOrAcquire(ctx context.Context, m message, text string) (string, int64, bool, func()) {
	key := turnScopeKey(m.Chat.ID, m.MessageThreadID)
	b.mu.Lock()
	l := b.locks[key]
	if l == nil {
		l = &chatLock{}
		b.locks[key] = l
	}
	l.refs++

	_, active := b.cancels[key]
	sendAck := false
	if active {
		if b.queued == nil {
			b.queued = map[int64]*queuedTurn{}
		}
		q := b.queued[key]
		if q == nil {
			q = &queuedTurn{}
			b.queued[key] = q
		}
		q.texts = append(q.texts, text)
		if m.MessageID > 0 {
			q.replyToID = m.MessageID
		}
		if !q.acked {
			q.acked = true
			sendAck = true
		}
	}
	b.mu.Unlock()

	if sendAck {
		go b.reply(ctx, m.Chat.ID, "⏳ Tugas sebelumnya masih berjalan — pesan kamu diantrekan dan akan diproses otomatis setelah selesai (ketik `/stop` untuk membatalkan).")
	}

	l.mu.Lock()
	unlock := func() {
		l.mu.Unlock()
		b.mu.Lock()
		l.refs--
		if l.refs <= 0 {
			delete(b.locks, key)
		}
		b.mu.Unlock()
	}

	if !active {
		return text, m.MessageID, true, unlock
	}

	b.mu.Lock()
	q := b.queued[key]
	delete(b.queued, key)
	b.mu.Unlock()

	if q == nil || len(q.texts) == 0 {
		unlock()
		return "", 0, false, nil
	}
	return strings.Join(q.texts, "\n"), q.replyToID, true, unlock
}

func (b *Bot) isChatOrUserAllowed(chatID int64, from *tgUser) bool {
	if _, ok := b.allowed[chatID]; ok {
		return true
	}
	if from != nil && from.ID != 0 {
		if _, ok := b.allowed[from.ID]; ok {
			return true
		}
	}
	return false
}

func (b *Bot) isRelevantTopic(m message) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.allowedTopics) == 0 || m.MessageThreadID <= 0 {
		return true
	}
	_, ok := b.allowedTopics[m.MessageThreadID]
	return ok
}

// GetGroupMode returns the current group trigger mode ("always" or "mention").
func (b *Bot) GetGroupMode() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.EqualFold(b.groupMode, "mention") {
		return "mention"
	}
	return "always"
}

// SetGroupMode updates the group trigger mode ("always" or "mention") at runtime.
func (b *Bot) SetGroupMode(mode string) {
	b.mu.Lock()
	if strings.EqualFold(strings.TrimSpace(mode), "mention") {
		b.groupMode = "mention"
	} else {
		b.groupMode = "always"
	}
	b.mu.Unlock()
}

// ToggleAllowedTopic adds or removes threadID from the allowed forum topics filter.
// Returns true if threadID is now in the allowlist.
func (b *Bot) ToggleAllowedTopic(threadID int64) bool {
	if threadID <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.allowedTopics == nil {
		b.allowedTopics = map[int64]struct{}{}
	}
	if _, ok := b.allowedTopics[threadID]; ok {
		delete(b.allowedTopics, threadID)
		return false
	}
	b.allowedTopics[threadID] = struct{}{}
	return true
}

// ClearAllowedTopics removes topic restrictions so all forum topics are accepted.
func (b *Bot) ClearAllowedTopics() {
	b.mu.Lock()
	clear(b.allowedTopics)
	b.mu.Unlock()
}

// AllowedTopicIDs returns the currently whitelisted forum topic IDs (empty = all allowed).
func (b *Bot) AllowedTopicIDs() []int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]int64, 0, len(b.allowedTopics))
	for id := range b.allowedTopics {
		out = append(out, id)
	}
	return out
}

// filterGroupMessage applies group trigger rules ("always" vs "mention") and
// strips a leading @bot_username mention when present.
func (b *Bot) filterGroupMessage(m message, text string) (string, bool) {
	isGroup := m.Chat.Type == "group" || m.Chat.Type == "supergroup"
	if !isGroup {
		return text, true
	}
	botUser := strings.ToLower(b.BotUsername())
	trimmed := strings.TrimSpace(text)
	mentioned := false
	if botUser != "" {
		tag := "@" + botUser
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, tag) {
			mentioned = true
			trimmed = strings.TrimSpace(trimmed[len(tag):])
		} else if strings.Contains(lower, tag) {
			mentioned = true
		}
	}
	if strings.EqualFold(b.GetGroupMode(), "mention") {
		isCmd := command(trimmed) != ""
		repliedToBot := m.ReplyToMessage != nil &&
			m.ReplyToMessage.ForumTopicCreated == nil &&
			m.ReplyToMessage.From != nil &&
			m.ReplyToMessage.From.IsBot
		if !isCmd && !mentioned && !repliedToBot {
			return "", false
		}
	}
	return trimmed, true
}

func (b *Bot) sendErrorLogIfAny(ctx context.Context, chatID int64, err error) {
	if b.onErrorLog == nil || err == nil || errors.Is(err, context.Canceled) {
		return
	}
	if logPath := b.onErrorLog(ctx, chatID, err); logPath != "" {
		_ = b.SendDocument(ctx, chatID, logPath, "🪵 Log error diagnostik")
	}
}
