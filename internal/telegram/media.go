package telegram

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (b *Bot) prepareAttachmentText(ctx, turnCtx context.Context, m message, text string) (string, bool) {
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
			return "", false
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
			return "", false
		}
		notes = append(notes, fmt.Sprintf("[Foto diterima: %s]", saved))
	}
	if m.Voice != nil {
		saved, err := b.DownloadFile(turnCtx, m.Voice.FileID, inbox, "voice.ogg")
		if err != nil {
			b.reply(ctx, m.Chat.ID, FormatError(fmt.Errorf("gagal mengunduh voice note: %w", err)))
			return "", false
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
			return "", false
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
		return prefix + "\n" + text, true
	}
	return prefix + "\nUser mengirim file terlampir.", true
}
