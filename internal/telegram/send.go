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
	"time"
)

func (b *Bot) reply(ctx context.Context, chatID int64, text string) {
	for _, chunk := range SplitMessage(text, MaxMessageLength) {
		if len([]rune(markdownToHTML(chunk))) > MaxMessageLength {
			for _, sub := range SplitMessage(chunk, 2400) {
				if err := b.SendMessage(ctx, chatID, sub); err != nil {
					return
				}
			}
			continue
		}
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
		payload := map[string]any{
			"chat_id": chatID,
			"rich_message": map[string]any{
				"markdown": chunk,
			},
		}
		applyReplyTarget(ctx, payload)
		_, err := b.post(ctx, "sendRichMessage", payload)
		if err != nil {
			b.reply(ctx, chatID, chunk)
		}
	}
	return nil
}

// SendRichMessageDraft streams a Bot API 10.4 rich message draft.
func (b *Bot) SendRichMessageDraft(ctx context.Context, chatID, draftID int64, markdown string, canStop, keepOnStop bool) error {
	if strings.TrimSpace(markdown) == "" {
		return errors.New("telegram: teks kosong")
	}
	payload := map[string]any{
		"chat_id":  chatID,
		"draft_id": draftID,
		"rich_message": map[string]any{
			"markdown": markdown,
		},
		"can_stop":     canStop,
		"keep_on_stop": keepOnStop,
	}
	applyThreadOnly(ctx, payload)
	_, err := b.post(ctx, "sendRichMessageDraft", payload)
	return err
}

// SendMessageDraft streams a partial Bot API 9.3-10.4 text draft to a private chat.
func (b *Bot) SendMessageDraft(ctx context.Context, chatID, draftID int64, text string, canStop, keepOnStop bool) error {
	if strings.TrimSpace(text) == "" {
		return errors.New("telegram: teks kosong")
	}
	payload := map[string]any{
		"chat_id":      chatID,
		"draft_id":     draftID,
		"text":         markdownToHTML(text),
		"parse_mode":   "HTML",
		"can_stop":     canStop,
		"keep_on_stop": keepOnStop,
	}
	applyThreadOnly(ctx, payload)
	_, err := b.post(ctx, "sendMessageDraft", payload)
	return err
}

// SendMessageReturningID sends one message and returns its Telegram message_id.
func (b *Bot) SendMessageReturningID(ctx context.Context, chatID int64, text string) (int64, error) {
	if strings.TrimSpace(text) == "" {
		return 0, errors.New("telegram: teks kosong")
	}
	rendered := markdownToHTML(text)
	if strings.TrimSpace(rendered) == "" {
		rendered = htmlEscapePlain(text)
	}
	payload := map[string]any{
		"chat_id":    chatID,
		"text":       rendered,
		"parse_mode": "HTML",
	}
	applyReplyTarget(ctx, payload)
	raw, err := b.post(ctx, "sendMessage", payload)
	if isHTTP400(err) {
		plain := map[string]any{"chat_id": chatID, "text": text}
		applyReplyTarget(ctx, plain)
		raw, err = b.post(ctx, "sendMessage", plain)
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
	payload := map[string]any{"chat_id": chatID, "action": action}
	applyThreadOnly(ctx, payload)
	_, err := b.post(ctx, "sendChatAction", payload)
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
	return dest, nil
}

func sanitizeFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	base = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, base)
	cleaned := strings.TrimLeft(base, ".")
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
	applyReplyFields(ctx, fields)
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
	applyReplyFields(ctx, fields)
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
