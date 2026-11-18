package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// FileSender delivers a local file to a chat user (e.g. Telegram sendDocument/sendPhoto).
type FileSender interface {
	SendFile(ctx context.Context, chatID int64, path, caption, kind string) error
}

type SendFile struct {
	cwd    string
	sender FileSender
}

func NewSendFile(cwd string, sender FileSender) *SendFile {
	return &SendFile{cwd: cwd, sender: sender}
}

func (t *SendFile) Name() string { return "send_file" }

func (t *SendFile) Description() string {
	return "Mengirim file atau gambar dari mesin ini ke chat Telegram user. kind=auto|document|photo (default auto)."
}

func (t *SendFile) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"path file relatif terhadap workspace atau absolut"},"caption":{"type":"string","description":"keterangan singkat opsional"},"kind":{"type":"string","enum":["auto","document","photo"],"description":"jenis pengiriman"}},"required":["path"]}`)
}

func (t *SendFile) Danger() bool { return false }

func (t *SendFile) Execute(ctx context.Context, raw json.RawMessage) (string, error) {
	if t.sender == nil {
		return "", errors.New("send_file: pengirim file tidak tersedia")
	}
	var in struct {
		Path    string `json:"path"`
		Caption string `json:"caption"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return "", fmt.Errorf("argumen send_file tidak valid: %w", err)
	}
	if in.Path == "" {
		return "", errors.New("path wajib diisi")
	}
	chatID := ChatIDFrom(ctx)
	if chatID <= 0 {
		return "", errors.New("send_file hanya tersedia di sesi chat Telegram")
	}

	path := resolve(t.cwd, in.Path)
	if err := checkSensitivePath(path); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s bukan file regular", path)
	}
	if err := t.sender.SendFile(ctx, chatID, path, in.Caption, in.Kind); err != nil {
		return "", err
	}
	return fmt.Sprintf("file terkirim ke chat: %s", path), nil
}
