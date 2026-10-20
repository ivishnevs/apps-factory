package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Item is an attention feed row to send; a question waiting for the owner names it and its options.
type Item struct {
	Seq        int64
	Text       string
	QuestionID string
	Options    []string
}

// Question is a question waiting for the owner (for reminders).
type Question struct {
	ID        string
	Text      string
	CreatedAt time.Time
}

// State is what the notifier keeps between ticks and restarts (Core stores it).
type State struct {
	ChatID       int64  `json:"chat_id,omitempty"`
	BindCode     string `json:"bind_code,omitempty"`
	BindExpires  int64  `json:"bind_expires,omitempty"`
	FeedAfter    int64  `json:"feed_after"`
	UpdateOffset int64  `json:"update_offset"`
	// Sent maps a Telegram message to the question it carries, so a reply to it answers that question.
	Sent map[string]string `json:"sent,omitempty"`
	// Reminded counts the reminders sent per pending question (4 h, then 24 h).
	Reminded  map[string]int `json:"reminded,omitempty"`
	DigestDay string         `json:"digest_day,omitempty"`
	StopAsked int64          `json:"stop_asked,omitempty"`
}

// Backend is Core's side: the feed, questions, answers and owner actions.
type Backend interface {
	LoadState(ctx context.Context) (State, error)
	SaveState(ctx context.Context, s State) error
	// FeedTail is the latest feed sequence (a freshly bound chat starts after it).
	FeedTail(ctx context.Context) (int64, error)
	// Attention lists attention rows after the cursor, oldest first.
	Attention(ctx context.Context, after int64) ([]Item, error)
	PendingQuestions(ctx context.Context) ([]Question, error)
	// Answer answers the question with option index choice, or with text when choice < 0. key makes a
	// repeated update apply once (the answer's request_id).
	Answer(ctx context.Context, question string, choice int, text, key string) (string, error)
	Status(ctx context.Context) (string, error)
	Stop(ctx context.Context) (string, error)
	// OwnerInput gives text to a direct report ("" for the default one).
	OwnerInput(ctx context.Context, agent, text, key string) (string, error)
	Digest(ctx context.Context, now time.Time) (string, error)
	// Applied and MarkApplied deduplicate updates by update_id.
	Applied(ctx context.Context, update int64) (bool, error)
	MarkApplied(ctx context.Context, update int64) error
}

// Reminder ages of an unanswered owner question.
var Reminders = []time.Duration{4 * time.Hour, 24 * time.Hour}

// StopConfirmWindow is how long a /stop waits for its confirmation.
const StopConfirmWindow = 5 * time.Minute

type Notifier struct {
	API     API
	Backend Backend
	Now     func() time.Time
	// Location and DigestAt ("18:00") set when the daily digest is sent; empty DigestAt sends none.
	Location *time.Location
	DigestAt string
	// PollTimeout is the getUpdates long-poll timeout (0 in tests).
	PollTimeout time.Duration
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// Run ticks until ctx ends; a failed tick (Telegram or Core unreachable) is retried with a backoff.
func (n *Notifier) Run(ctx context.Context) {
	wait := time.Second
	for ctx.Err() == nil {
		if err := n.Tick(ctx); err != nil {
			slog.Warn("telegram tick", "error", err)
			wait = min(wait*2, time.Minute)
		} else {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// Tick applies new updates, then (once a chat is bound) sends attention items, reminders and the digest.
func (n *Notifier) Tick(ctx context.Context) error {
	st, err := n.Backend.LoadState(ctx)
	if err != nil {
		return err
	}
	if st.Sent == nil {
		st.Sent = map[string]string{}
	}
	if st.Reminded == nil {
		st.Reminded = map[string]int{}
	}
	err = n.inbound(ctx, &st)
	if err == nil && st.ChatID != 0 {
		err = n.outbound(ctx, &st)
	}
	if serr := n.Backend.SaveState(ctx, st); err == nil {
		err = serr
	}
	return err
}

func (n *Notifier) inbound(ctx context.Context, st *State) error {
	updates, err := n.API.Updates(ctx, st.UpdateOffset, n.PollTimeout)
	if err != nil {
		return err
	}
	for _, u := range updates {
		st.UpdateOffset = max(st.UpdateOffset, u.UpdateID+1)
		done, err := n.Backend.Applied(ctx, u.UpdateID)
		if err != nil {
			return err
		}
		if done {
			continue
		}
		if err = n.apply(ctx, st, u); err != nil {
			return err
		}
		if err = n.Backend.MarkApplied(ctx, u.UpdateID); err != nil {
			return err
		}
	}
	return nil
}

func (n *Notifier) say(ctx context.Context, st *State, text string, replyTo int64) error {
	_, err := n.API.Send(ctx, st.ChatID, text, nil, replyTo)
	return err
}

func (n *Notifier) apply(ctx context.Context, st *State, u Update) error {
	key := "telegram-" + strconv.FormatInt(u.UpdateID, 10)
	if c := u.Callback; c != nil {
		if c.Message == nil || c.Message.Chat.ID != st.ChatID || st.ChatID == 0 {
			return nil
		}
		notice := "ok"
		parts := strings.SplitN(c.Data, "|", 3)
		switch {
		case len(parts) == 3 && parts[0] == "a":
			choice, _ := strconv.Atoi(parts[2])
			out, err := n.Backend.Answer(ctx, parts[1], choice, "", key)
			if err != nil {
				notice = "not applied: " + err.Error()
			} else if err = n.say(ctx, st, out, c.Message.MessageID); err != nil {
				return err
			}
		case c.Data == "stop|confirm":
			if err := n.stop(ctx, st, c.Message.MessageID); err != nil {
				return err
			}
		}
		return n.API.AnswerCallback(ctx, c.ID, shorten(notice, 190))
	}
	m := u.Message
	if m == nil {
		return nil
	}
	text := strings.TrimSpace(m.Text)
	if code, ok := strings.CutPrefix(text, "/start "); ok {
		// Binding: the one-time code from factory telegram bind, before it expires.
		if st.BindCode != "" && strings.TrimSpace(code) == st.BindCode && n.now().Unix() < st.BindExpires {
			tail, err := n.Backend.FeedTail(ctx)
			if err != nil {
				return err
			}
			st.ChatID, st.BindCode, st.BindExpires, st.FeedAfter = m.Chat.ID, "", 0, tail
			return n.say(ctx, st, "Bound: this chat gets the factory's attention items and the daily digest.", 0)
		}
		return nil
	}
	if st.ChatID == 0 || m.Chat.ID != st.ChatID {
		return nil // only the bound owner chat is heard
	}
	switch {
	case text == "/status":
		out, err := n.Backend.Status(ctx)
		if err != nil {
			out = "status unavailable: " + err.Error()
		}
		return n.say(ctx, st, out, m.MessageID)
	case text == "/stop":
		st.StopAsked = n.now().Unix()
		_, err := n.API.Send(ctx, st.ChatID, "Stop the whole company? Every live run is revoked and nothing new starts until factory start. Confirm within 5 minutes.", []Button{{Text: "Confirm stop", Data: "stop|confirm"}}, m.MessageID)
		return err
	case text == "/stop confirm":
		return n.stop(ctx, st, m.MessageID)
	case m.ReplyTo != nil && st.Sent[strconv.FormatInt(m.ReplyTo.MessageID, 10)] != "":
		out, err := n.Backend.Answer(ctx, st.Sent[strconv.FormatInt(m.ReplyTo.MessageID, 10)], -1, text, key)
		if err != nil {
			out = "not applied: " + err.Error()
		}
		return n.say(ctx, st, out, m.MessageID)
	case text == "" || strings.HasPrefix(text, "/"):
		return n.say(ctx, st, "Commands: /status, /stop. Reply to a question to answer it; any other message goes to your default direct report, or @name to address one.", m.MessageID)
	}
	agent, body := "", text
	if strings.HasPrefix(text, "@") {
		name, rest, _ := strings.Cut(text[1:], " ")
		agent, body = name, strings.TrimSpace(rest)
	}
	out, err := n.Backend.OwnerInput(ctx, agent, body, key)
	if err != nil {
		out = "not delivered: " + err.Error()
	}
	return n.say(ctx, st, out, m.MessageID)
}

// stop runs the kill switch only within the confirmation window of a /stop.
func (n *Notifier) stop(ctx context.Context, st *State, replyTo int64) error {
	if st.StopAsked == 0 || n.now().Unix()-st.StopAsked > int64(StopConfirmWindow.Seconds()) {
		return n.say(ctx, st, "No /stop to confirm: send /stop first.", replyTo)
	}
	st.StopAsked = 0
	out, err := n.Backend.Stop(ctx)
	if err != nil {
		out = "stop failed: " + err.Error()
	}
	return n.say(ctx, st, out, replyTo)
}

func (n *Notifier) outbound(ctx context.Context, st *State) error {
	items, err := n.Backend.Attention(ctx, st.FeedAfter)
	if err != nil {
		return err
	}
	for _, it := range items {
		if it.Text == "" {
			st.FeedAfter = it.Seq
			continue
		}
		buttons := []Button{}
		for i, o := range it.Options {
			buttons = append(buttons, Button{Text: o, Data: fmt.Sprintf("a|%s|%d", it.QuestionID, i)})
		}
		text := it.Text
		if it.QuestionID != "" {
			text += "\n\nReply to this message to answer."
		}
		id, err := n.API.Send(ctx, st.ChatID, shorten(text, 4000), buttons, 0)
		if err != nil {
			return err // the cursor stays: the item is sent again on the next tick
		}
		if it.QuestionID != "" {
			st.Sent[strconv.FormatInt(id, 10)] = it.QuestionID
		}
		st.FeedAfter = it.Seq
	}
	pending, err := n.Backend.PendingQuestions(ctx)
	if err != nil {
		return err
	}
	open := map[string]bool{}
	for _, q := range pending {
		open[q.ID] = true
		age := n.now().Sub(q.CreatedAt)
		due := 0
		for _, r := range Reminders {
			if age >= r {
				due++
			}
		}
		if due <= st.Reminded[q.ID] {
			continue
		}
		// One reminder covers every age already passed (a Core gap sends one, not two).
		id, err := n.API.Send(ctx, st.ChatID, shorten(fmt.Sprintf("Reminder: waiting for your answer for %s.\n%s\n\nReply to this message to answer.", age.Round(time.Minute), q.Text), 4000), nil, 0)
		if err != nil {
			return err
		}
		st.Sent[strconv.FormatInt(id, 10)] = q.ID
		st.Reminded[q.ID] = due
	}
	for id := range st.Reminded {
		if !open[id] {
			delete(st.Reminded, id)
		}
	}
	for k, q := range st.Sent {
		if !open[q] {
			delete(st.Sent, k)
		}
	}
	return n.digest(ctx, st)
}

func (n *Notifier) digest(ctx context.Context, st *State) error {
	if n.DigestAt == "" {
		return nil
	}
	loc := n.Location
	if loc == nil {
		loc = time.UTC
	}
	now := n.now().In(loc)
	at, err := time.ParseInLocation("15:04", n.DigestAt, loc)
	if err != nil {
		return fmt.Errorf("telegram digest_at %q: want HH:MM", n.DigestAt)
	}
	day := now.Format("2006-01-02")
	if st.DigestDay == day || now.Hour()*60+now.Minute() < at.Hour()*60+at.Minute() {
		return nil
	}
	text, err := n.Backend.Digest(ctx, n.now())
	if err != nil {
		return err
	}
	if err = n.say(ctx, st, shorten(text, 4000), 0); err != nil {
		return err
	}
	st.DigestDay = day
	return nil
}

func shorten(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
