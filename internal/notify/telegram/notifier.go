package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A company's Telegram bot (telegram-per-company): every person of the company binds their own chat and gets what
// is meant for their role; in the chat a reply goes to exactly that message, @name to that agent, and plain text
// is a reply to the last message from an agent. The bot says where every message went.

// Roles of the people a chat belongs to (as in the company).
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// OwnerKey is the person of a chat bound with the owner's CLI token (factory telegram bind): an owner.
const OwnerKey = "owner"

// Target is what a delivered message is about: a reply to it goes there.
type Target struct {
	// Question is the question the message carries (answered while it waits).
	Question string `json:"q,omitempty"`
	// Agent wrote the message; Project is where.
	Agent   string `json:"a,omitempty"`
	Project string `json:"p,omitempty"`
	// Label names the sender for the confirmation ("Thread PO · Product team").
	Label string `json:"l,omitempty"`
}

// Item is an attention feed row to send.
type Item struct {
	Seq  int64
	Text string
	// Target is set for a message from an agent (a question or an escalation).
	Target  Target
	Options []string
	// OwnersOnly: questions to the owner; other items (connectors, pauses, reports) go to owners and admins.
	OwnersOnly bool
	// Retire is a question replaced by a newer version (people-topics D10): its sent messages lose their buttons and
	// say what replaced them (RetireNote); nothing new is sent.
	Retire     string
	RetireNote string
}

// Question is a question waiting for an owner (reminders, /inbox).
type Question struct {
	Target    Target
	Text      string
	CreatedAt time.Time
}

// Bind is a one-time connect code.
type Bind struct {
	Who     string `json:"who"`
	Expires int64  `json:"expires"`
}

// Person is one bound chat: whose it is and what the bot keeps for it.
type Person struct {
	ChatID    int64 `json:"chat_id"`
	FeedAfter int64 `json:"feed_after"`
	// Sent maps a delivered message to what it is about, so a reply goes there; Last is the last message from an
	// agent (plain text answers it).
	Sent map[string]Target `json:"sent,omitempty"`
	Last *Target           `json:"last,omitempty"`
	// Reminded counts the reminders sent per pending question (4 h, then 24 h).
	Reminded  map[string]int `json:"reminded,omitempty"`
	DigestDay string         `json:"digest_day,omitempty"`
	NoDigest  bool           `json:"no_digest,omitempty"`
	StopAsked int64          `json:"stop_asked,omitempty"`
	BoundAt   int64          `json:"bound_at,omitempty"`
	// RepliesAfter is the feed cursor of agents' replies routed to this chat (telegram-reply-routing).
	RepliesAfter int64 `json:"replies_after,omitempty"`
}

// State is what the notifier keeps between ticks and restarts (Core stores it).
type State struct {
	UpdateOffset int64              `json:"update_offset"`
	Binds        map[string]Bind    `json:"binds,omitempty"`
	People       map[string]*Person `json:"people,omitempty"`
	// Before telegram-per-company one chat was bound for the owner; it becomes People[OwnerKey].
	ChatID    int64  `json:"chat_id,omitempty"`
	FeedAfter int64  `json:"feed_after,omitempty"`
	DigestDay string `json:"digest_day,omitempty"`
}

// Normalize fills the maps and moves a chat bound before telegram-per-company to the owner.
func (s *State) Normalize() {
	if s.Binds == nil {
		s.Binds = map[string]Bind{}
	}
	if s.People == nil {
		s.People = map[string]*Person{}
	}
	if s.ChatID != 0 {
		if s.People[OwnerKey] == nil {
			s.People[OwnerKey] = &Person{ChatID: s.ChatID, FeedAfter: s.FeedAfter, DigestDay: s.DigestDay}
		}
		s.ChatID, s.FeedAfter, s.DigestDay = 0, 0, ""
	}
	for _, p := range s.People {
		if p.Sent == nil {
			p.Sent = map[string]Target{}
		}
		if p.Reminded == nil {
			p.Reminded = map[string]int{}
		}
	}
}

// ByChat finds who a chat belongs to.
func (s *State) ByChat(chat int64) (string, *Person) {
	for who, p := range s.People {
		if p.ChatID == chat {
			return who, p
		}
	}
	return "", nil
}

// Backend is Core's side: the feed, questions, answers and owner actions of one company.
type Backend interface {
	LoadState(ctx context.Context) (State, error)
	SaveState(ctx context.Context, s State) error
	// Company is the company's display name.
	Company(ctx context.Context) string
	// Role is the person's role in the company; ErrNotFound-like errors (Gone) unbind the chat.
	Role(ctx context.Context, who string) (string, error)
	// FeedTail is the latest feed sequence (a freshly bound chat starts after it).
	FeedTail(ctx context.Context) (int64, error)
	// Attention lists attention rows after the cursor, oldest first.
	Attention(ctx context.Context, after int64) ([]Item, error)
	PendingQuestions(ctx context.Context) ([]Question, error)
	// Choose answers a question with option index choice (a button). key makes a repeated update apply once.
	Choose(ctx context.Context, question string, choice int, key string) (string, error)
	// Reply sends text to what target is about: the question while it waits, else its sender's conversation.
	Reply(ctx context.Context, target Target, text, key string) (string, error)
	// Message sends text to agent ("" for the only team lead), in project when given.
	Message(ctx context.Context, agent, project, text, key string) (string, error)
	Status(ctx context.Context) (string, error)
	Stop(ctx context.Context) (string, error)
	Digest(ctx context.Context, now time.Time) (string, error)
	// Applied and MarkApplied deduplicate updates by update_id.
	Applied(ctx context.Context, update int64) (bool, error)
	MarkApplied(ctx context.Context, update int64) error
}

// Doctorer is a Backend with the company doctor (setup-assistant 3.1): /doctor answers with the whole report, and
// the daily digest carries its problems, each with its fix.
type Doctorer interface {
	Doctor(ctx context.Context) (report, problems string, err error)
}

// Gone is the error Role returns for a person who left the company: their chat is unbound.
type Gone struct{}

func (Gone) Error() string { return "no longer in the company" }

// Reminder ages of an unanswered owner question.
var Reminders = []time.Duration{4 * time.Hour, 24 * time.Hour}

// StopConfirmWindow is how long a /stop waits for its confirmation.
const StopConfirmWindow = 5 * time.Minute

// sentKept bounds the remembered messages per chat (a reply to an older one is plain text then).
const sentKept = 200

const help = "Reply to a message to answer it. @name text writes to that agent. Plain text answers the last message " +
	"from an agent. Commands: /inbox, /status, /doctor and /stop (owners and admins), /digest on|off, /disconnect."

type Notifier struct {
	API     API
	Backend Backend
	Now     func() time.Time
	// Location and DigestAt ("18:00") set when the daily digest is sent; empty DigestAt sends none.
	Location *time.Location
	DigestAt string
	// PollTimeout is the getUpdates long-poll timeout (0 in tests).
	PollTimeout time.Duration
	// Webhook: updates arrive through Handle; Tick only sends.
	Webhook bool
	mu      sync.Mutex
}

func (n *Notifier) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

// Run ticks until ctx ends; a failed tick (Telegram or Core unreachable) is retried with a backoff.
func (n *Notifier) Run(ctx context.Context) {
	base := time.Second
	if n.Webhook {
		base = 3 * time.Second
	}
	wait := base
	for ctx.Err() == nil {
		if err := n.Tick(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("telegram tick", "error", err)
			wait = min(wait*2, time.Minute)
		} else {
			wait = base
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

func (n *Notifier) load(ctx context.Context) (State, error) {
	st, err := n.Backend.LoadState(ctx)
	st.Normalize()
	return st, err
}

// Tick applies new updates (unless they come by webhook), then sends every bound person their items, reminders
// and digest. The long poll runs outside the lock, so the website's changes (Update) never wait for it.
func (n *Notifier) Tick(ctx context.Context) error {
	var updates []Update
	if !n.Webhook {
		st, err := n.Backend.LoadState(ctx)
		if err != nil {
			return err
		}
		if updates, err = n.API.Updates(ctx, st.UpdateOffset, n.PollTimeout); err != nil {
			return err
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	st, err := n.load(ctx)
	if err != nil {
		return err
	}
	for _, u := range updates {
		if u.UpdateID < st.UpdateOffset {
			continue
		}
		st.UpdateOffset = u.UpdateID + 1
		if err = n.once(ctx, &st, u); err != nil {
			break
		}
	}
	if err == nil {
		err = n.outbound(ctx, &st)
	}
	if serr := n.Backend.SaveState(ctx, st); err == nil {
		err = serr
	}
	return err
}

// Handle applies one update that came by webhook.
func (n *Notifier) Handle(ctx context.Context, u Update) error {
	return n.Update(ctx, func(st *State) error { return n.once(ctx, st, u) })
}

// Update changes the state under the notifier's lock (the website binding or unbinding a chat).
func (n *Notifier) Update(ctx context.Context, change func(*State) error) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	st, err := n.load(ctx)
	if err != nil {
		return err
	}
	if err = change(&st); err != nil {
		return err
	}
	return n.Backend.SaveState(ctx, st)
}

func (n *Notifier) once(ctx context.Context, st *State, u Update) error {
	done, err := n.Backend.Applied(ctx, u.UpdateID)
	if err != nil || done {
		return err
	}
	if err = n.apply(ctx, st, u); err != nil {
		return err
	}
	return n.Backend.MarkApplied(ctx, u.UpdateID)
}

func (n *Notifier) say(ctx context.Context, chat int64, text string, replyTo int64) error {
	_, err := n.API.Send(ctx, chat, shorten(text, 4000), nil, replyTo)
	return err
}

// Replier is an optional Backend: agents' replies in chats where this person last wrote from Telegram
// (telegram-reply-routing).
type Replier interface {
	Replies(ctx context.Context, who, role string, after int64) ([]Item, error)
}

// PersonContexter is an optional Backend: it attributes what a bound person does from Telegram to them
// (people-attribution).
type PersonContexter interface {
	PersonContext(ctx context.Context, who string) context.Context
}

// Imager is an optional Backend: the images a question or a decision proposal carries (inbox-visual-previews),
// sent as an album right before it, so the person judges them where they answer. More than MaxAlbum is
// noted in the message (the rest are in the inbox).
type Imager interface {
	Images(ctx context.Context, question string) (photos []Photo, more int, err error)
}

// album sends the images of the item's question before the item; a failure leaves the text to go alone (the
// images stay in the inbox) and is logged.
func (n *Notifier) album(ctx context.Context, p *Person, t Target) string {
	im, ok := n.Backend.(Imager)
	if !ok || t.Question == "" {
		return ""
	}
	photos, more, err := im.Images(ctx, t.Question)
	if err == nil && len(photos) > 0 {
		err = n.API.SendPhotos(ctx, p.ChatID, photos)
	}
	if err != nil {
		slog.Warn("telegram images not sent", "question", t.Question, "error", err)
		return "\n\nImages are in the App Factory inbox."
	}
	if more > 0 {
		return fmt.Sprintf("\n\n%d more attached in the App Factory inbox.", more)
	}
	return ""
}

func (n *Notifier) as(ctx context.Context, who string) context.Context {
	if pc, ok := n.Backend.(PersonContexter); ok {
		return pc.PersonContext(ctx, who)
	}
	return ctx
}

func manager(role string) bool { return role == RoleOwner || role == RoleAdmin }

func (n *Notifier) apply(ctx context.Context, st *State, u Update) error {
	key := "telegram-" + strconv.FormatInt(u.UpdateID, 10)
	if c := u.Callback; c != nil {
		if c.Message == nil {
			return nil
		}
		who, p := st.ByChat(c.Message.Chat.ID)
		if p == nil {
			return nil
		}
		ctx = n.as(ctx, who)
		notice := "ok"
		parts := strings.SplitN(c.Data, "|", 3)
		switch {
		case len(parts) == 3 && parts[0] == "a":
			choice, _ := strconv.Atoi(parts[2])
			out, err := n.Backend.Choose(ctx, parts[1], choice, key)
			if err != nil {
				notice = "not applied: " + err.Error()
			} else if err = n.say(ctx, p.ChatID, out, c.Message.MessageID); err != nil {
				return err
			}
		case c.Data == "stop|confirm":
			if err := n.stop(ctx, who, p, c.Message.MessageID); err != nil {
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
	if code, ok := strings.CutPrefix(text, "/start"); ok {
		return n.bind(ctx, st, m.Chat.ID, strings.TrimSpace(code))
	}
	who, p := st.ByChat(m.Chat.ID)
	if p != nil {
		ctx = n.as(ctx, who)
	}
	if p == nil {
		return n.say(ctx, m.Chat.ID, "This chat is not connected. In App Factory open Settings → Integrations → Connect Telegram.", 0)
	}
	role, err := n.Backend.Role(ctx, who)
	if _, gone := err.(Gone); gone {
		delete(st.People, who)
		return n.say(ctx, m.Chat.ID, "You are no longer in "+n.Backend.Company(ctx)+": this chat is disconnected.", 0)
	}
	if err != nil {
		return err
	}
	reply := func(out string, err error, prefix string) error {
		if err != nil {
			out = prefix + err.Error()
		}
		return n.say(ctx, p.ChatID, out, m.MessageID)
	}
	switch {
	case text == "/help":
		return n.say(ctx, p.ChatID, help, m.MessageID)
	case text == "/status":
		out, err := n.Backend.Status(ctx)
		return reply(out, err, "status unavailable: ")
	case text == "/doctor":
		d, ok := n.Backend.(Doctorer)
		if !ok || !manager(role) {
			return n.say(ctx, p.ChatID, "Only owners and admins can run the company doctor.", m.MessageID)
		}
		report, _, err := d.Doctor(ctx)
		return reply(report, err, "doctor unavailable: ")
	case text == "/disconnect":
		delete(st.People, who)
		return n.say(ctx, p.ChatID, "Disconnected from "+n.Backend.Company(ctx)+". Connect again from Settings → Integrations.", m.MessageID)
	case text == "/digest on" || text == "/digest off":
		p.NoDigest = text == "/digest off"
		return n.say(ctx, p.ChatID, map[bool]string{true: "Daily digest off.", false: "Daily digest on."}[p.NoDigest], m.MessageID)
	case text == "/inbox":
		return n.inbox(ctx, p, role, m.MessageID)
	case text == "/stop":
		if !manager(role) {
			return n.say(ctx, p.ChatID, "Only owners and admins can stop the company.", m.MessageID)
		}
		p.StopAsked = n.now().Unix()
		_, err := n.API.Send(ctx, p.ChatID, "Stop the whole company? Every live run is revoked and nothing new starts until it is started again. Confirm within 5 minutes.", []Button{{Text: "Confirm stop", Data: "stop|confirm"}}, m.MessageID)
		return err
	case text == "/stop confirm":
		return n.stop(ctx, who, p, m.MessageID)
	case text == "" || strings.HasPrefix(text, "/"):
		return n.say(ctx, p.ChatID, help, m.MessageID)
	case m.ReplyTo != nil && p.Sent[strconv.FormatInt(m.ReplyTo.MessageID, 10)] != (Target{}):
		t := p.Sent[strconv.FormatInt(m.ReplyTo.MessageID, 10)]
		out, err := n.Backend.Reply(ctx, t, text, key)
		return reply(out, err, "not delivered: ")
	case strings.HasPrefix(text, "@"):
		name, body, _ := strings.Cut(text[1:], " ")
		project := ""
		if p.Last != nil {
			project = p.Last.Project
		}
		out, err := n.Backend.Message(ctx, strings.TrimRight(name, ",:"), project, strings.TrimSpace(body), key)
		return reply(out, err, "not delivered: ")
	case p.Last != nil:
		out, err := n.Backend.Reply(ctx, *p.Last, text, key)
		return reply(out, err, "not delivered: ")
	}
	out, err := n.Backend.Message(ctx, "", "", text, key)
	return reply(out, err, "not delivered: ")
}

// bind connects the chat with a one-time code from Settings (or factory telegram bind).
func (n *Notifier) bind(ctx context.Context, st *State, chat int64, code string) error {
	b, ok := st.Binds[code]
	now := n.now().Unix()
	for c, x := range st.Binds {
		if x.Expires <= now {
			delete(st.Binds, c)
		}
	}
	if code == "" || !ok || b.Expires <= now {
		if _, p := st.ByChat(chat); p != nil {
			return n.say(ctx, chat, "This chat is connected. "+help, 0)
		}
		return n.say(ctx, chat, "This connect link has expired or was used. Open Settings → Integrations → Connect Telegram for a new one.", 0)
	}
	delete(st.Binds, code)
	tail, err := n.Backend.FeedTail(ctx)
	if err != nil {
		return err
	}
	if who, p := st.ByChat(chat); p != nil && who != b.Who {
		delete(st.People, who) // one chat, one person
	}
	st.People[b.Who] = &Person{ChatID: chat, FeedAfter: tail, Sent: map[string]Target{}, Reminded: map[string]int{}, BoundAt: now}
	return n.say(ctx, chat, "Connected to "+n.Backend.Company(ctx)+". You get what needs you and a daily digest.\n\n"+help, 0)
}

// stop runs the kill switch only within the confirmation window of a /stop.
func (n *Notifier) stop(ctx context.Context, who string, p *Person, replyTo int64) error {
	if role, err := n.Backend.Role(ctx, who); err != nil || !manager(role) {
		return n.say(ctx, p.ChatID, "Only owners and admins can stop the company.", replyTo)
	}
	if p.StopAsked == 0 || n.now().Unix()-p.StopAsked > int64(StopConfirmWindow.Seconds()) {
		return n.say(ctx, p.ChatID, "No /stop to confirm: send /stop first.", replyTo)
	}
	p.StopAsked = 0
	out, err := n.Backend.Stop(ctx)
	if err != nil {
		out = "stop failed: " + err.Error()
	}
	return n.say(ctx, p.ChatID, out, replyTo)
}

// send delivers an item to a person and remembers what it is about.
func (n *Notifier) send(ctx context.Context, p *Person, text string, t Target, options []string) error {
	buttons := []Button{}
	for i, o := range options {
		buttons = append(buttons, Button{Text: optionLabel(o), Data: fmt.Sprintf("a|%s|%d", t.Question, i)})
	}
	id, err := n.API.Send(ctx, p.ChatID, shorten(text, 4000), buttons, 0)
	if err != nil {
		return err
	}
	if t != (Target{}) {
		p.Sent[strconv.FormatInt(id, 10)] = t
		last := t
		p.Last = &last
		if len(p.Sent) > sentKept {
			ids := make([]int64, 0, len(p.Sent))
			for k := range p.Sent {
				v, _ := strconv.ParseInt(k, 10, 64)
				ids = append(ids, v)
			}
			slices.Sort(ids)
			for _, v := range ids[:len(ids)-sentKept] {
				delete(p.Sent, strconv.FormatInt(v, 10))
			}
		}
	}
	return nil
}

func (n *Notifier) inbox(ctx context.Context, p *Person, role string, replyTo int64) error {
	if role != RoleOwner {
		return n.say(ctx, p.ChatID, "Questions to the owner wait for owners; nothing waits for you here.", replyTo)
	}
	qs, err := n.Backend.PendingQuestions(ctx)
	if err != nil {
		return err
	}
	if len(qs) == 0 {
		return n.say(ctx, p.ChatID, "Nothing waits for you.", replyTo)
	}
	if err = n.say(ctx, p.ChatID, fmt.Sprintf("%d waiting for you. Reply to one to answer it.", len(qs)), replyTo); err != nil {
		return err
	}
	for _, q := range qs[:min(len(qs), 10)] {
		if err = n.send(ctx, p, q.Text, q.Target, nil); err != nil {
			return err
		}
	}
	return nil
}

func (n *Notifier) outbound(ctx context.Context, st *State) error {
	var pending []Question
	loaded := false
	for who, p := range st.People {
		role, err := n.Backend.Role(ctx, who)
		if _, gone := err.(Gone); gone {
			delete(st.People, who)
			continue
		}
		if err != nil {
			return err
		}
		items, err := n.Backend.Attention(ctx, p.FeedAfter)
		if err != nil {
			return err
		}
		for _, it := range items {
			if it.Retire != "" {
				n.retire(ctx, p, it.Retire, it.RetireNote)
			}
			if it.Text != "" && (role == RoleOwner || !it.OwnersOnly && role == RoleAdmin) {
				text := it.Text + n.album(ctx, p, it.Target)
				if it.Target.Question != "" && !strings.Contains(text, "Reply to this message") {
					text += "\n\nReply to this message to answer."
				}
				if err = n.send(ctx, p, text, it.Target, it.Options); err != nil {
					return err // the cursor stays: the item is sent again on the next tick
				}
			}
			p.FeedAfter = it.Seq
		}
		if rp, ok := n.Backend.(Replier); ok {
			if p.RepliesAfter == 0 {
				p.RepliesAfter = p.FeedAfter // from binding on: no replay of older chats
			}
			replies, err := rp.Replies(ctx, who, role, p.RepliesAfter)
			if err != nil {
				return err
			}
			for _, it := range replies {
				if it.Text != "" {
					if err = n.send(ctx, p, it.Text+"\n\nReply to this message to answer.", it.Target, it.Options); err != nil {
						return err
					}
				}
				p.RepliesAfter = it.Seq
			}
		}
		if role == RoleOwner {
			if !loaded {
				if pending, err = n.Backend.PendingQuestions(ctx); err != nil {
					return err
				}
				loaded = true
			}
			if err = n.remind(ctx, p, pending); err != nil {
				return err
			}
		}
		if err = n.digest(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (n *Notifier) remind(ctx context.Context, p *Person, pending []Question) error {
	open := map[string]bool{}
	for _, q := range pending {
		id := q.Target.Question
		open[id] = true
		age := n.now().Sub(q.CreatedAt)
		due := 0
		for _, r := range Reminders {
			if age >= r {
				due++
			}
		}
		if due <= p.Reminded[id] {
			continue
		}
		// One reminder covers every age already passed (a Core gap sends one, not two).
		if err := n.send(ctx, p, fmt.Sprintf("Reminder: waiting for your answer for %s.\n%s\n\nReply to this message to answer.", age.Round(time.Minute), q.Text), q.Target, nil); err != nil {
			return err
		}
		p.Reminded[id] = due
	}
	for id := range p.Reminded {
		if !open[id] {
			delete(p.Reminded, id)
		}
	}
	return nil
}

func (n *Notifier) digest(ctx context.Context, p *Person) error {
	if n.DigestAt == "" || p.NoDigest {
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
	if p.DigestDay == day || now.Hour()*60+now.Minute() < at.Hour()*60+at.Minute() {
		return nil
	}
	text, err := n.Backend.Digest(ctx, n.now())
	if err != nil {
		return err
	}
	if d, ok := n.Backend.(Doctorer); ok {
		if _, problems, err := d.Doctor(ctx); err != nil {
			slog.Warn("telegram digest: company doctor", "error", err)
		} else if problems != "" {
			text += "\n\n" + problems
		}
	}
	if err = n.say(ctx, p.ChatID, text, 0); err != nil {
		return err
	}
	p.DigestDay = day
	return nil
}

func shorten(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// optionLabel shows a Factory escalation answer as what it does (dependency-wait-escalations 2.2); the button still
// answers with the option itself.
func optionLabel(o string) string {
	switch o {
	case "keep_waiting":
		return "Keep waiting"
	case "drop_dependency":
		return "Stop waiting for this task"
	case "new_session":
		return "Continue in a new session"
	case "spec":
		return "It writes the spec: run without the check"
	}
	return o
}

// retire takes the buttons off the messages sent about question and answers them with what replaced it
// (people-topics D10); a failure is logged, the person can still not decide it (Core refuses a replaced version).
func (n *Notifier) retire(ctx context.Context, p *Person, question, note string) {
	for k, t := range p.Sent {
		if t.Question != question {
			continue
		}
		id, _ := strconv.ParseInt(k, 10, 64)
		if err := n.API.EditButtons(ctx, p.ChatID, id, nil); err != nil {
			slog.Warn("telegram buttons not removed", "message", id, "error", err)
		}
		if err := n.say(ctx, p.ChatID, "↪ "+note, id); err != nil {
			slog.Warn("telegram retire note not sent", "message", id, "error", err)
		}
		delete(p.Sent, k)
	}
}
