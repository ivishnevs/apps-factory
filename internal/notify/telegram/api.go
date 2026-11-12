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
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
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
	return a.do(req, method, out)
}

// do sends a Bot API request and reads its envelope into out.
func (a API) do(req *http.Request, method string, out any) error {
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

// Photo is an image sent to a chat (inbox-visual-previews): its file name, caption and content.
type Photo struct {
	Name    string
	Caption string
	Data    []byte
}

// MaxAlbum is the most photos one album (sendMediaGroup) holds.
const MaxAlbum = 10

// SendPhotos sends photos to the chat in the order given: one photo alone, two to MaxAlbum as one album (a
// comparison shows side by side); more are cut off (the caller says so in its text).
func (a API) SendPhotos(ctx context.Context, chat int64, photos []Photo) error {
	if len(photos) == 0 {
		return nil
	}
	photos = photos[:min(len(photos), MaxAlbum)]
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("chat_id", strconv.FormatInt(chat, 10))
	method := "sendMediaGroup"
	media := []map[string]any{}
	for i, p := range photos {
		field := "photo" + strconv.Itoa(i)
		if len(photos) == 1 {
			method, field = "sendPhoto", "photo"
			if p.Caption != "" {
				_ = mw.WriteField("caption", shorten(p.Caption, 1000))
			}
		} else {
			item := map[string]any{"type": "photo", "media": "attach://" + field}
			if p.Caption != "" {
				item["caption"] = shorten(p.Caption, 1000)
			}
			media = append(media, item)
		}
		fw, err := mw.CreateFormFile(field, cmpOr(p.Name, field+".jpg"))
		if err != nil {
			return err
		}
		if _, err = fw.Write(p.Data); err != nil {
			return err
		}
	}
	if len(media) > 0 {
		b, _ := json.Marshal(media)
		_ = mw.WriteField("media", string(b))
	}
	if err := mw.Close(); err != nil {
		return err
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+"/bot"+a.Token+"/"+method, &body)
	if err != nil {
		return fmt.Errorf("telegram %s: invalid request", method)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return a.do(req, method, nil)
}

func cmpOr(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
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

// Me is GetMe: the bot's identity (validates a token).
func (a API) Me(ctx context.Context) (User, error) {
	var u User
	err := a.call(ctx, "getMe", map[string]any{}, &u)
	return u, err
}

// SetWebhook makes Telegram post updates to url with secret in the X-Telegram-Bot-Api-Secret-Token header.
func (a API) SetWebhook(ctx context.Context, url, secret string) error {
	return a.call(ctx, "setWebhook", map[string]any{"url": url, "secret_token": secret, "allowed_updates": []string{"message", "callback_query"}}, nil)
}

// DeleteWebhook switches the bot back to getUpdates.
func (a API) DeleteWebhook(ctx context.Context) error {
	return a.call(ctx, "deleteWebhook", map[string]any{}, nil)
}
