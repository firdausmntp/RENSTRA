package telegram

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// BotCommand describes one Telegram slash command shown in the chat menu.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
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
	MessageID          int64                 `json:"message_id"`
	MessageThreadID    int64                 `json:"message_thread_id"`
	IsTopicMessage     bool                  `json:"is_topic_message"`
	From               *tgUser               `json:"from"`
	Text               string                `json:"text"`
	Caption            string                `json:"caption"`
	Chat               chat                  `json:"chat"`
	ReplyToMessage     *message              `json:"reply_to_message"`
	Quote              *tgTextQuote          `json:"quote"`
	ForumTopicCreated  *tgForumTopic         `json:"forum_topic_created"`
	ForumTopicEdited   *tgForumTopic         `json:"forum_topic_edited"`
	WriteAccessAllowed *tgWriteAccessAllowed `json:"write_access_allowed"`
	Document           *tgDocument           `json:"document"`
	Photo              []tgPhoto             `json:"photo"`
	Voice              *tgVoice              `json:"voice"`
	Audio              *tgAudio              `json:"audio"`
}

type tgForumTopic struct {
	Name string `json:"name"`
}

// tgWriteAccessAllowed represents Bot API 10.5 WriteAccessAllowed service message
// (including inviter user when joined via https://t.me/botinvite/<bot_username>).
type tgWriteAccessAllowed struct {
	FromRequest        bool    `json:"from_request,omitempty"`
	WebAppName         string  `json:"web_app_name,omitempty"`
	FromAttachmentMenu bool    `json:"from_attachment_menu,omitempty"`
	Inviter            *tgUser `json:"inviter,omitempty"`
}

type tgTextQuote struct {
	Text string `json:"text"`
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
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Title   string `json:"title,omitempty"`
	IsForum bool   `json:"is_forum,omitempty"`
}

type callbackQuery struct {
	ID              string   `json:"id"`
	From            tgUser   `json:"from"`
	Message         *message `json:"message"`
	InlineMessageID string   `json:"inline_message_id,omitempty"`
	ChatInstance    string   `json:"chat_instance,omitempty"`
	Data            string   `json:"data"`
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	Username  string `json:"username,omitempty"`
}
