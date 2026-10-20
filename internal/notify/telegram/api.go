// Package telegram is the owner's Telegram channel (waits-events-cost-cli D4): attention items with option
// buttons, reminders for owner questions, one daily digest, and inbound answers, /status, /stop and owner
// messages. Nothing else is sent. The bot token comes from the environment and never appears in logs or
// errors.
package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// API is a minimal Bot API client.
type API struct {
	// BaseURL is https://api.telegram.org unless a test points it at a fake.
	BaseURL string
	Token   string
	HTTP    *http.Client
}

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username,omitempty"`
}

type Chat struct {
	ID int64 `json:"id"`
}

type Message struct {
	MessageID int64    `json:"message_id"`
	Chat      Chat     `json:"chat"`
	From      *User    `json:"from,omitempty"`
	Text      string   `json:"text,omitempty"`
	ReplyTo   *Message `json:"reply_to_message,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data"`
}

type Update struct {
	UpdateID int64          `json:"update_id"`
	Message  *Message       `json:"message,omitempty"`
	Callback *CallbackQuery `json:"callback_query,omitempty"`
}

// Button is one inline keyboard button.
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

func (a API) call(ctx context.Context, method string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/bot"+a.Token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("telegram %s: invalid request", method)
	}
	req.Header.Set("Content-Type", "application/json")
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		// A url.Error carries the URL, which holds the token: keep only the cause.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("telegram %s: %v", method, err)
	}
	defer resp.Body.Close()
	var env struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return fmt.Errorf("telegram %s: status %d", method, resp.StatusCode)
	}
	if !env.OK {
		return fmt.Errorf("telegram %s: %s", method, env.Description)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(env.Result, out)
}

// Send sends text to the chat, with option buttons (one row each) and as a reply when replyTo is set.
func (a API) Send(ctx context.Context, chat int64, text string, buttons []Button, replyTo int64) (int64, error) {
	in := map[string]any{"chat_id": chat, "text": text}
	if len(buttons) > 0 {
		rows := [][]Button{}
		for _, b := range buttons {
			rows = append(rows, []Button{b})
		}
		in["reply_markup"] = map[string]any{"inline_keyboard": rows}
	}
	if replyTo != 0 {
		in["reply_parameters"] = map[string]any{"message_id": replyTo}
	}
	var m Message
	err := a.call(ctx, "sendMessage", in, &m)
	return m.MessageID, err
}

// Updates long-polls updates from offset (timeout 0 returns at once).
func (a API) Updates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	var out []Update
	err := a.call(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": int(timeout.Seconds()), "allowed_updates": []string{"message", "callback_query"}}, &out)
	return out, err
}

// AnswerCallback stops the button's spinner with a short notice.
func (a API) AnswerCallback(ctx context.Context, id, text string) error {
	return a.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": id, "text": text}, nil)
}
