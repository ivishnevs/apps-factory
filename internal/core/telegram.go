package core

import (
	"appfactory/internal/decisions"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
)

// Telegram per company (telegram-per-company; before it waits-events-cost-cli D4): every company may have its own
// bot (its token sealed under the company key), every person binds their own chat from Settings, and Core is each
// bot's backend. With an https public address Telegram posts updates to /telegram/hook/<company>; otherwise Core
// polls. FACTORY_TELEGRAM_TOKEN stays the default company's bot when it has no stored one.

const telegramChannel = "telegram"

// TelegramConfig routes owner messages from Telegram.
type TelegramConfig struct {
	// Project receives messages from Telegram that name no project; empty: the agent's team's project.
	Project string
	// Token is FACTORY_TELEGRAM_TOKEN: the default company's bot when it stores none (polling).
	Token string
	// DigestAt is the daily digest time in the company's time zone ("18:00"; empty sends none).
	DigestAt string
	// Poll makes stored bots poll even with an https public address (FACTORY_TELEGRAM_POLL=1).
	Poll bool
	// APIBase points the Bot API at a fake in tests.
	APIBase string
}

// telegramBackend implements telegram.Backend for one company.
type telegramBackend struct {
	c       *Core
	company string
}

// TelegramBackend is the notifier's view of this Core's default company.
func (c *Core) TelegramBackend() telegram.Backend { return telegramBackend{c, c.company()} }

// TelegramBackendFor is the notifier's view of company.
func (c *Core) TelegramBackendFor(company string) telegram.Backend {
	return telegramBackend{c, company}
}

// telegramBots runs one notifier per company bot.
type telegramBots struct {
	mu      sync.Mutex
	workers map[string]*telegramWorker
	kick    chan struct{}
}

type telegramWorker struct {
	n      *telegram.Notifier
	token  string
	hook   string
	cancel context.CancelFunc
}

func (c *Core) telegramWebhooks() bool {
	return !c.Telegram.Poll && strings.HasPrefix(c.PublicURL, "https://")
}

func (c *Core) hookURL(company string) string {
	return strings.TrimRight(c.PublicURL, "/") + "/telegram/hook/" + company
}

// RunTelegram keeps a notifier running for every company bot until ctx ends.
func (c *Core) RunTelegram(ctx context.Context) {
	c.bots.mu.Lock()
	if c.bots.kick == nil {
		c.bots.kick = make(chan struct{}, 1)
	}
	kick := c.bots.kick
	c.bots.mu.Unlock()
	for ctx.Err() == nil {
		if err := c.syncTelegram(ctx); err != nil {
			slog.Warn("telegram bots", "error", err)
		}
		select {
		case <-ctx.Done():
		case <-kick:
		case <-time.After(30 * time.Second):
		}
	}
	c.bots.mu.Lock()
	for _, w := range c.bots.workers {
		w.cancel()
	}
	c.bots.workers = nil
	c.bots.mu.Unlock()
}

func (c *Core) kickTelegram() {
	c.bots.mu.Lock()
	defer c.bots.mu.Unlock()
	if c.bots.kick != nil {
		select {
		case c.bots.kick <- struct{}{}:
		default:
		}
	}
}

// syncTelegram starts notifiers for new bots, restarts changed ones and stops removed ones.
func (c *Core) syncTelegram(ctx context.Context) error {
	companies, err := c.Store.TelegramBotCompanies(ctx)
	if err != nil {
		return err
	}
	want := map[string]storage.TelegramBot{}
	for _, co := range companies {
		b, err := c.Store.TelegramBotOf(ctx, co)
		if err != nil {
			slog.Warn("telegram bot", "company", co, "error", err)
			continue
		}
		want[co] = b
	}
	if _, ok := want[c.company()]; !ok && c.Telegram.Token != "" {
		want[c.company()] = storage.TelegramBot{Company: c.company(), Token: c.Telegram.Token}
	}
	c.bots.mu.Lock()
	defer c.bots.mu.Unlock()
	if c.bots.workers == nil {
		c.bots.workers = map[string]*telegramWorker{}
	}
	for co, w := range c.bots.workers {
		if b, ok := want[co]; !ok || b.Token != w.token || b.HookSecret != w.hook {
			w.cancel()
			delete(c.bots.workers, co)
		}
	}
	for co, b := range want {
		if _, ok := c.bots.workers[co]; ok {
			continue
		}
		loc := time.UTC
		if zone, err := c.Store.CompanyTimeZone(ctx, co); err == nil {
			if l, err := time.LoadLocation(zone); err == nil {
				loc = l
			}
		}
		api := telegram.API{BaseURL: c.Telegram.APIBase, Token: b.Token}
		// The environment's bot polls as before; a stored bot takes webhooks when Core has an https address.
		webhook := b.HookSecret != "" && c.telegramWebhooks()
		n := &telegram.Notifier{API: api, Backend: c.TelegramBackendFor(co), Location: loc, DigestAt: c.Telegram.DigestAt, PollTimeout: 20 * time.Second, Webhook: webhook}
		wctx, cancel := context.WithCancel(ctx)
		c.bots.workers[co] = &telegramWorker{n: n, token: b.Token, hook: b.HookSecret, cancel: cancel}
		go func() {
			var err error
			if webhook {
				err = api.SetWebhook(wctx, c.hookURL(co), b.HookSecret)
			} else {
				err = api.DeleteWebhook(wctx)
			}
			if err != nil {
				slog.Warn("telegram webhook", "company", co, "error", err)
			}
			n.Run(wctx)
		}()
		slog.Info("telegram bot started", "company", co, "webhook", webhook)
	}
	return nil
}

// telegramHook is POST /telegram/hook/<company>: Telegram's update for a company's bot, checked by the secret
// the webhook was set with.
func (c *Core) telegramHook(w http.ResponseWriter, r *http.Request) {
	company := strings.TrimPrefix(r.URL.Path, "/telegram/hook/")
	c.bots.mu.Lock()
	wk := c.bots.workers[company]
	c.bots.mu.Unlock()
	if wk == nil || wk.hook == "" {
		http.Error(w, "not found", 404)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Telegram-Bot-Api-Secret-Token")), []byte(wk.hook)) != 1 {
		http.Error(w, "forbidden", 403)
		return
	}
	var u telegram.Update
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&u); err != nil {
		http.Error(w, "bad update", 400)
		return
	}
	if err := wk.n.Handle(r.Context(), u); err != nil {
		slog.Warn("telegram update", "company", company, "error", err)
		http.Error(w, "try again", 503) // Telegram retries
		return
	}
	write(w, map[string]bool{"ok": true})
}

// TelegramView is GET /telegram: the company's bot and the caller's chat.
type TelegramView struct {
	Bot *storage.TelegramBot `json:"bot"`
	// Environment: the default company's bot comes from FACTORY_TELEGRAM_TOKEN (not changeable here).
	Environment bool `json:"environment,omitempty"`
	// Me is the signed-in person's chat (nil for tokens).
	Me *TelegramMe `json:"me,omitempty"`
}

type TelegramMe struct {
	Connected bool `json:"connected"`
	Digest    bool `json:"digest"`
}

// telegramWho is the person a request binds: the signed-in user, else the owner (CLI tokens).
func telegramWho(r *http.Request) string {
	if p := principalOf(r); p.Kind == "session" {
		return p.User
	}
	return telegram.OwnerKey
}

func (c *Core) telegramRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /telegram", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		out := TelegramView{}
		if b, err := c.Store.TelegramBotOf(r.Context(), company); err == nil {
			out.Bot = &b
		} else if !errors.Is(err, storage.ErrNotFound) {
			reply(w, nil, err)
			return
		} else if company == c.company() && c.Telegram.Token != "" {
			out.Environment = true
		}
		if principalOf(r).Kind == "session" {
			st, err := telegramBackend{c, company}.LoadState(r.Context())
			if err != nil {
				reply(w, nil, err)
				return
			}
			p := st.People[telegramWho(r)]
			out.Me = &TelegramMe{Connected: p != nil, Digest: p == nil || !p.NoDigest}
		}
		write(w, out)
	})
	// The company's bot (owners and admins): a token from @BotFather, checked with getMe.
	m.HandleFunc("PUT /telegram/bot", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &p) {
			return
		}
		p.Token = strings.TrimSpace(p.Token)
		if p.Token == "" || strings.ContainsAny(p.Token, " /?#") {
			jsonStatus(w, 400, map[string]string{"error": "paste the token @BotFather gave you (like 123456:ABC...)", "field": "token"})
			return
		}
		me, err := telegram.API{BaseURL: c.Telegram.APIBase, Token: p.Token}.Me(r.Context())
		if err != nil || me.Username == "" {
			jsonStatus(w, 400, map[string]string{"error": "Telegram does not accept this token: check it with @BotFather", "field": "token"})
			return
		}
		hook := make([]byte, 24)
		_, _ = rand.Read(hook)
		bot := storage.TelegramBot{Token: p.Token, HookSecret: hex.EncodeToString(hook), Username: me.Username}
		if err = c.Store.SetTelegramBot(r.Context(), c.companyFrom(r), bot, author(r)); errors.Is(err, secrets.ErrNoMasterKey) {
			jsonStatus(w, 503, map[string]string{"error": "the server has no master key for secrets (FACTORY_SECRETS_KEY), so the bot token cannot be stored"})
			return
		} else if err != nil {
			reply(w, nil, err)
			return
		}
		c.kickTelegram()
		write(w, map[string]string{"username": me.Username})
	})
	m.HandleFunc("DELETE /telegram/bot", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		b, err := c.Store.TelegramBotOf(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if err = c.Store.DeleteTelegramBot(r.Context(), company); err != nil {
			reply(w, nil, err)
			return
		}
		if err = (telegram.API{BaseURL: c.Telegram.APIBase, Token: b.Token}).DeleteWebhook(r.Context()); err != nil {
			slog.Warn("telegram webhook removal", "company", company, "error", err)
		}
		c.kickTelegram()
		write(w, map[string]bool{"ok": true})
	})
	// Connect my Telegram: a one-time code (15 minutes) in a t.me link that starts the bot.
	connect := func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		username := ""
		if b, err := c.Store.TelegramBotOf(r.Context(), company); err == nil {
			username = b.Username
		} else if !errors.Is(err, storage.ErrNotFound) || company != c.company() || c.Telegram.Token == "" {
			jsonStatus(w, 409, map[string]string{"error": "this company has no Telegram bot yet: an owner or admin adds it in Settings → Integrations", "reason": "no_bot"})
			return
		}
		raw := make([]byte, 8)
		_, _ = rand.Read(raw)
		code := hex.EncodeToString(raw)
		expires := c.Store.Now().Add(15 * time.Minute).Unix()
		if err := c.updateTelegramPerson(r.Context(), company, telegramWho(r), func(st *telegram.State, who string) error {
			st.Binds[code] = telegram.Bind{Who: who, Expires: expires}
			return nil
		}); err != nil {
			reply(w, nil, err)
			return
		}
		out := map[string]any{"code": code, "expires_at": expires, "instruction": "send /start " + code + " to the company's bot"}
		if username != "" {
			out["url"] = "https://t.me/" + username + "?start=" + code
		}
		write(w, out)
	}
	m.HandleFunc("POST /telegram/connect", connect)
	m.HandleFunc("POST /telegram/bind", connect) // factory telegram bind
	m.HandleFunc("PATCH /telegram/me", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Digest bool `json:"digest"`
		}
		if !decode(w, r, &p) {
			return
		}
		reply(w, map[string]bool{"digest": p.Digest}, c.updateTelegramPerson(r.Context(), c.companyFrom(r), telegramWho(r), func(st *telegram.State, who string) error {
			if st.People[who] == nil {
				return storage.ErrNotFound
			}
			st.People[who].NoDigest = !p.Digest
			return nil
		}))
	})
	m.HandleFunc("DELETE /telegram/me", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.updateTelegramPerson(r.Context(), c.companyFrom(r), telegramWho(r), func(st *telegram.State, who string) error {
			if st.People[who] == nil {
				return storage.ErrNotFound
			}
			delete(st.People, who)
			return nil
		}))
	})
}

// updateTelegramPerson changes the bot state of company, under its notifier's lock when one runs.
func (c *Core) updateTelegramPerson(ctx context.Context, company, who string, change func(*telegram.State, string) error) error {
	c.bots.mu.Lock()
	wk := c.bots.workers[company]
	c.bots.mu.Unlock()
	if wk != nil {
		return wk.n.Update(ctx, func(st *telegram.State) error { return change(st, who) })
	}
	b := telegramBackend{c, company}
	st, err := b.LoadState(ctx)
	if err != nil {
		return err
	}
	st.Normalize()
	if err = change(&st, who); err != nil {
		return err
	}
	return b.SaveState(ctx, st)
}

// PersonContext attributes a bound person's Telegram actions to them (people-attribution); who is a user id, or the
// owner key of a chat bound before people were bound one by one.
func (b telegramBackend) PersonContext(ctx context.Context, who string) context.Context {
	p := b.c.personOf(ctx, b.Company(ctx), who)
	if p.ID == "" {
		p.ID = who
	}
	p.Channel = "telegram"
	return storage.WithPerson(ctx, p)
}

// Replies are the agents' messages in owner chats after the cursor that go to who's Telegram chat
// (telegram-reply-routing): the reply goes where the person last wrote in that chat. Questions to owners already
// come as attention items, so for an owner only non-questions are listed here.
func (b telegramBackend) Replies(ctx context.Context, who, role string, after int64) ([]telegram.Item, error) {
	page, err := b.c.Store.Feed(ctx, b.company, storage.FeedFilter{}, after, 50)
	if err != nil {
		return nil, err
	}
	out := []telegram.Item{}
	for _, f := range page.Items {
		it := telegram.Item{Seq: f.Sequence}
		if f.Type == "message.created" {
			if m, err := b.c.Store.Message(ctx, b.company, f.EntityID); err == nil && m.Sender != "owner" && m.Recipient == "owner" && strings.HasPrefix(m.ConversationID, "owner-") &&
				!(m.MessageType == "question" && role == telegram.RoleOwner) && b.lastWriter(ctx, m) == who {
				it.Target = telegram.Target{Agent: m.Sender, Project: m.ProjectID, Label: b.label(m.Sender)}
				if m.MessageType == "question" && (m.Status == "PENDING" || m.Status == "ESCALATED") {
					it.Text, it.Target = b.questionItem(m)
				} else {
					it.Text = fmt.Sprintf("💬 %s\n%s\n\n%s", it.Target.Label, m.ProjectID, m.Body)
				}
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// lastWriter is the person whose last message before m in m's chat came from Telegram ("" when it came from the web).
func (b telegramBackend) lastWriter(ctx context.Context, m messaging.Message) string {
	ms, err := b.c.Store.Messages(ctx, b.company, m.ConversationID)
	if err != nil {
		return ""
	}
	var last *messaging.Message
	for i := range ms {
		if ms[i].ID == m.ID {
			break
		}
		if ms[i].Sender == "owner" {
			last = &ms[i]
		}
	}
	if last == nil || last.Channel != "telegram" {
		return ""
	}
	return last.PersonID
}

func (b telegramBackend) LoadState(ctx context.Context) (telegram.State, error) {
	var st telegram.State
	raw, err := b.c.Store.NotifyState(ctx, b.company, telegramChannel)
	if err == nil && raw != "" {
		err = json.Unmarshal([]byte(raw), &st)
	}
	return st, err
}

func (b telegramBackend) SaveState(ctx context.Context, st telegram.State) error {
	raw, _ := json.Marshal(st)
	return b.c.Store.SaveNotifyState(ctx, b.company, telegramChannel, string(raw))
}

func (b telegramBackend) Company(ctx context.Context) string {
	if name, err := b.c.Store.CompanyName(ctx, b.company); err == nil && name != "" {
		return name
	}
	return b.company
}

func (b telegramBackend) Role(ctx context.Context, who string) (string, error) {
	if who == telegram.OwnerKey {
		return telegram.RoleOwner, nil
	}
	role, err := b.c.Store.Role(ctx, who, b.company)
	if errors.Is(err, storage.ErrNotFound) {
		return "", telegram.Gone{}
	}
	return role, err
}

func (b telegramBackend) FeedTail(ctx context.Context) (int64, error) {
	return b.c.Store.FeedTail(ctx, b.company)
}

// label names an agent with its team: "Thread PO · product team".
func (b telegramBackend) label(agent string) string {
	if team := b.c.cat(b.company).TeamOf(agent); team != "" {
		return agent + " · " + team + " team"
	}
	return agent
}

// questionOf names the question a feed row is about, if it still waits for an answer.
func (b telegramBackend) questionOf(ctx context.Context, item storage.FeedItem) (messaging.Message, bool) {
	id := ""
	switch {
	case item.Type == "message.created" || item.Type == "message.escalated" || item.Type == "message.forwarded":
		id = item.EntityID
	case strings.HasPrefix(item.Type, "task.escalation.") || strings.HasPrefix(item.Type, "task.parked."):
		id = item.Ref
	}
	if id == "" {
		return messaging.Message{}, false
	}
	m, err := b.c.Store.Message(ctx, b.company, id)
	if err != nil || m.MessageType != "question" || (m.Status != "PENDING" && m.Status != "ESCALATED") {
		return m, false
	}
	return m, true
}

// questionItem is how a question to the owner reads in the chat: who asks, where, and the question.
func (b telegramBackend) questionItem(m messaging.Message) (string, telegram.Target) {
	t := telegram.Target{Question: m.ID, Agent: m.Sender, Project: m.ProjectID, Label: b.label(m.Sender)}
	where := m.ProjectID
	if m.TaskID != "" {
		where += " / " + m.TaskID
	}
	text := fmt.Sprintf("❓ %s\n%s\n\n%s", t.Label, where, m.Body)
	if m.Details == messaging.DetailsPromised {
		// inbox-badge-parity: still open, the asking task waits for the details.
		text += "\n\n⏳ Details promised: reply with them here (or withdraw the promise in the Inbox on the website)."
	}
	return text, t
}

func (b telegramBackend) Attention(ctx context.Context, after int64) ([]telegram.Item, error) {
	page, err := b.c.Store.Feed(ctx, b.company, storage.FeedFilter{Attention: true}, after, 50)
	if err != nil {
		return nil, err
	}
	out := []telegram.Item{}
	asked := map[string]bool{}
	for _, f := range page.Items {
		if f.Type == "task.possible_duplicate" {
			// For the creating manager (its feed and the board), not a message to people (task-duplicate-warnings).
			out = append(out, telegram.Item{Seq: f.Sequence})
			continue
		}
		switch f.Type {
		case "topic.item_added":
			// People topics (people-topics D10): one message per item that waits, with its images and buttons.
			out = append(out, b.topicItem(ctx, f))
			continue
		case "approval.requested", "approval.escalated":
			// An approval reaches people as its topic item (people-topics D10); this row says nothing more.
			out = append(out, telegram.Item{Seq: f.Sequence})
			continue
		case "topic.item_replaced":
			out = append(out, telegram.Item{Seq: f.Sequence, Retire: f.Ref, RetireNote: "replaced by " + f.Summary + ": decide the new version"})
			continue
		}
		where := strings.Trim(f.ProjectID+"/"+f.TaskID, "/")
		it := telegram.Item{Seq: f.Sequence, Text: fmt.Sprintf("⚠ %s %s: %s", where, f.Type, f.Summary)}
		if node, ok := strings.CutPrefix(f.EntityID, "node:"); ok && f.Type == "node_update_available" {
			// Approval policy (node-packaging-updates 3.4): Telegram asks, the button approves the update.
			it.Text = "⬆ " + f.Summary
			it.Target = telegram.Target{Question: nodeUpdateQuestion + node, Label: "node " + node}
			it.Options = []string{"Update to " + f.Ref}
			out = append(out, it)
			continue
		}
		if d, m, ok := b.proposalOf(ctx, f); ok && m.TopicID == "" {
			it.Text = fmt.Sprintf("🗳 %s\n%s\nDecision: %s\n\n%s", b.label(m.Sender), m.ProjectID, d.Topic, m.Body)
			it.Target = telegram.Target{Question: m.ID, Agent: m.Sender, Project: m.ProjectID, Label: b.label(m.Sender)}
			it.Options = append([]string{d.Value}, d.Alternatives...)
			it.OwnersOnly = true
			out = append(out, it)
			continue
		}
		if m, ok := b.questionOf(ctx, f); ok {
			if asked[m.ID] || m.TopicID != "" { // a topic's question goes as its topic item // one message per question (its message and escalation rows); no text sends nothing
				out = append(out, telegram.Item{Seq: f.Sequence})
				continue
			}
			asked[m.ID] = true
			it.Text, it.Target = b.questionItem(m)
			it.OwnersOnly = true
			if len(m.Questions) == 1 && m.Questions[0].Type == "single_choice" {
				it.Options = m.Questions[0].Options
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func (b telegramBackend) PendingQuestions(ctx context.Context) ([]telegram.Question, error) {
	inbox, err := b.c.Store.Inbox(ctx, b.company, "")
	if err != nil {
		return nil, err
	}
	out := []telegram.Question{}
	for _, m := range inbox {
		if m.MessageType == "question" && (m.Status == "PENDING" || m.Status == "ESCALATED") {
			text, t := b.questionItem(m)
			out = append(out, telegram.Question{Target: t, Text: text, CreatedAt: time.Unix(m.CreatedAt, 0)})
		}
	}
	return out, nil
}

func (b telegramBackend) Choose(ctx context.Context, id string, choice int, key string) (string, error) {
	if node, ok := strings.CutPrefix(id, nodeUpdateQuestion); ok { // node_update_available (node-packaging-updates 3.4)
		return b.approveNodeUpdate(ctx, node)
	}
	if m, err := b.c.Store.Message(ctx, b.company, id); err == nil && m.DecisionID != "" && m.MessageType == "review_request" {
		return b.decide(ctx, m, choice, key)
	} else if err == nil && m.ApprovalID != "" {
		if a, err := b.c.Store.Approval(ctx, b.company, m.ApprovalID); err == nil && a.Choice() {
			return b.choice(ctx, a, choice, "", key)
		}
		return b.approval(ctx, m, "approve", "", key)
	}
	return b.answer(ctx, id, choice, "", key)
}

// proposalOf is the open decision proposal a feed row announces (decision-approval-ux: decided from Telegram too).
func (b telegramBackend) proposalOf(ctx context.Context, item storage.FeedItem) (decisions.Decision, messaging.Message, bool) {
	if item.Type != "message.created" {
		return decisions.Decision{}, messaging.Message{}, false
	}
	m, err := b.c.Store.Message(ctx, b.company, item.EntityID)
	if err != nil || m.DecisionID == "" || m.MessageType != "review_request" {
		return decisions.Decision{}, m, false
	}
	d, err := b.c.Store.Decision(ctx, b.company, m.DecisionID)
	if err != nil || (d.Status != "proposed" && d.Status != "under_discussion") {
		return d, m, false
	}
	return d, m, true
}

// decide approves the option chosen for a proposal (index into value, alternatives…).
func (b telegramBackend) decide(ctx context.Context, m messaging.Message, choice int, key string) (string, error) {
	d, err := b.c.Store.Decision(ctx, b.company, m.DecisionID)
	if err != nil {
		return "", err
	}
	if d.Status != "proposed" && d.Status != "under_discussion" {
		return "This decision is already " + d.Status + ".", nil
	}
	options := append([]string{d.Value}, d.Alternatives...)
	if choice < 0 || choice >= len(options) {
		return "", fmt.Errorf("choose one of the options, or reject it in the web")
	}
	if _, err = b.c.Store.RecordDecision(ctx, b.company, d.ID, decisions.Record{Version: d.Version, Status: "approved", Value: options[choice], Rationale: "Chosen in Telegram", Actor: "owner", RequestID: key}); err != nil {
		return "", err
	}
	return "Approved " + d.Topic + ": " + options[choice], nil
}

// answer answers a waiting question with option index choice, or with text when choice < 0.
func (b telegramBackend) answer(ctx context.Context, id string, choice int, text, key string) (string, error) {
	m, err := b.c.Store.Message(ctx, b.company, id)
	if err != nil {
		return "", err
	}
	if m.Status != "PENDING" && m.Status != "ESCALATED" {
		return "This question is already " + strings.ToLower(m.Status) + ".", nil
	}
	in := messaging.Reply{Version: m.Version, Sender: "owner", Body: text, RequestID: key}
	switch {
	case m.Details == messaging.DetailsPromised && choice < 0 && strings.TrimSpace(text) != "":
		// The promised details, in the owner's words, whatever the form (inbox-badge-parity).
	case len(m.Questions) == 1 && m.Questions[0].Type == "single_choice":
		q := m.Questions[0]
		if choice < 0 {
			choice = slices.IndexFunc(q.Options, func(o string) bool { return strings.EqualFold(o, strings.TrimSpace(text)) })
		}
		if choice < 0 || choice >= len(q.Options) {
			return "", fmt.Errorf("answer one of: %s", strings.Join(q.Options, ", "))
		}
		in.Answers, in.Body = map[string]any{q.ID: q.Options[choice]}, q.Options[choice]
	case len(m.Questions) > 0:
		return "", fmt.Errorf("this question has a form: answer it in the Inbox on the website")
	case strings.TrimSpace(text) == "":
		return "", fmt.Errorf("an answer needs text")
	}
	if _, err = b.c.Store.ReplyMessage(ctx, b.company, id, in); err != nil {
		return "", err
	}
	return "Answered " + b.label(m.Sender) + ": " + in.Body, nil
}

func (b telegramBackend) Reply(ctx context.Context, t telegram.Target, text, key string) (string, error) {
	if strings.HasPrefix(t.Question, nodeUpdateQuestion) {
		return "", fmt.Errorf("approve the node's update with the button, or on the Nodes page")
	}
	if t.Question != "" {
		if m, err := b.c.Store.Message(ctx, b.company, t.Question); err == nil && m.ApprovalID != "" {
			if a, err := b.c.Store.Approval(ctx, b.company, m.ApprovalID); err == nil && a.Choice() {
				// A reply to a choice asks for changes, or rejects all (artifact-choice-approvals).
				return b.choice(ctx, a, -1, text, key)
			}
			// A reply to an approval rejects it with the reply as the reason (people-topics D10).
			return b.approval(ctx, m, "reject", text, key)
		} else if err == nil && (m.Status == "PENDING" || m.Status == "ESCALATED") {
			return b.answer(ctx, t.Question, -1, text, key)
		}
	}
	// The owner talks to a team through its lead: a message from another agent (or from Core itself, like a
	// stall escalation) continues with the lead of its team, else of the project's team.
	agent := t.Agent
	cat := b.c.cat(b.company)
	if !slices.Contains(config.DirectReports(cat.Agents, config.Owner), agent) {
		if team := cat.TeamOf(agent); team != "" && cat.Lead(team) != "" {
			agent = cat.Lead(team)
		} else {
			agent = b.c.teamLead(ctx, b.company, t.Project, "")
		}
	}
	out, err := b.Message(ctx, agent, t.Project, text, key)
	if err == nil && t.Agent != "" && agent != t.Agent {
		out += " (you answered " + t.Agent + ")"
	}
	return out, err
}

// Message sends an owner message to agent, one of the owner's direct reports ("" with only one of them), on the
// owner conversation in project.
func (b telegramBackend) Message(ctx context.Context, agent, project, text, key string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("empty message")
	}
	leads := config.DirectReports(b.c.cat(b.company).Agents, config.Owner)
	switch {
	case agent == "" && len(leads) == 1:
		agent = leads[0]
	case agent == "":
		return "", fmt.Errorf("say whom it is for: @name text (%s)", "@"+strings.Join(leads, ", @"))
	case !slices.Contains(leads, agent):
		return "", fmt.Errorf("@%s is not one of your direct reports (%s)", agent, strings.Join(leads, ", "))
	}
	project, err := b.project(ctx, agent, project)
	if err != nil {
		return "", err
	}
	if _, err = b.c.ownerMessageTo(ctx, b.company, project, agent, text, "question", key); err != nil {
		return "", err
	}
	return "→ " + b.label(agent), nil
}

// project is where a Telegram message to agent goes: the given project when agent's team works there, else
// FACTORY_TELEGRAM_PROJECT, else the first project of agent's team, else the company's only project.
func (b telegramBackend) project(ctx context.Context, agent, project string) (string, error) {
	team := b.c.cat(b.company).TeamOf(agent)
	projects, err := b.c.Store.Projects(ctx, b.company)
	if err != nil {
		return "", err
	}
	for _, p := range projects {
		if p.ID == project && (p.Team == "" || team == "" || p.Team == team) {
			return project, nil
		}
	}
	if b.c.Telegram.Project != "" {
		return b.c.Telegram.Project, nil
	}
	for _, p := range projects {
		if team != "" && p.Team == team {
			return p.ID, nil
		}
	}
	if len(projects) == 1 {
		return projects[0].ID, nil
	}
	return "", fmt.Errorf("%s's team has no project yet: create one on the website first", agent)
}

func (b telegramBackend) Status(ctx context.Context) (string, error) {
	r, _ := http.NewRequestWithContext(ctx, "GET", "/status", nil)
	r = r.WithContext(context.WithValue(r.Context(), companyKey{}, b.company))
	projects, err := b.c.status(r, "")
	if err != nil {
		return "", err
	}
	var s strings.Builder
	for _, p := range projects {
		fmt.Fprintf(&s, "%s: %d running, %d runnable, %d waiting, inbox %d\n", p.Project.ID, len(p.Runs), p.Runnable, len(p.Waiting), p.Inbox)
	}
	pending, err := b.c.Store.PendingCounts(ctx, b.company)
	if err != nil {
		return "", err
	}
	pauses, err := b.c.Store.Pauses(ctx, b.company)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&s, "waiting for you: %d", pending.Total)
	for _, p := range pauses {
		fmt.Fprintf(&s, "\npaused: %s %s", p.Scope, p.Target)
	}
	return s.String(), nil
}

func (b telegramBackend) Stop(ctx context.Context) (string, error) {
	n, err := b.c.killSwitch(ctx, b.company, "telegram /stop")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Stopped: %d live runs revoked, nothing new starts until the company is started again.", n), nil
}

// Doctor is the company doctor for /doctor and the digest's problems (setup-assistant 3.1).
func (b telegramBackend) Doctor(ctx context.Context) (string, string, error) {
	d, err := b.c.CompanyDoctor(ctx, b.company, 24*time.Hour)
	if err != nil {
		return "", "", err
	}
	return d.Text(), d.Summary(5), nil
}

// Digest: done, in progress, waiting for the owner, failures and retries, nodes and the cost of the last day.
func (b telegramBackend) Digest(ctx context.Context, now time.Time) (string, error) {
	since := now.Add(-24 * time.Hour)
	counts := map[string]int{}
	var after int64
	for {
		page, err := b.c.Store.Feed(ctx, b.company, storage.FeedFilter{}, after, 100)
		if err != nil {
			return "", err
		}
		for _, f := range page.Items {
			if f.CreatedAt.Before(since) {
				continue
			}
			switch {
			case f.Type == "task.exit.done":
				counts["done"]++
			case f.Type == "task.retry":
				counts["retries"]++
			case strings.HasPrefix(f.Type, "task.escalation."):
				counts["escalations"]++
			}
		}
		after = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	r, _ := http.NewRequestWithContext(ctx, "GET", "/status", nil)
	r = r.WithContext(context.WithValue(r.Context(), companyKey{}, b.company))
	projects, err := b.c.status(r, "")
	if err != nil {
		return "", err
	}
	running := 0
	for _, p := range projects {
		running += len(p.Runs)
	}
	pending, err := b.c.Store.PendingCounts(ctx, b.company)
	if err != nil {
		return "", err
	}
	nodes, err := b.c.Store.Nodes(ctx, b.company)
	if err != nil {
		return "", err
	}
	offline := 0
	for _, n := range nodes {
		if !n.Online {
			offline++
		}
	}
	costs, err := b.c.Store.Costs(ctx, b.company, storage.CostFilter{Since: since.Unix()})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Daily digest %s\ndone: %d\nin progress: %d runs\nwaiting for you: %d\nfailures and retries: %d (escalations %d)\nnodes: %d, %d offline\ncost of the day: $%.2f over %d runs (%d without a price)",
		now.Format("2006-01-02"), counts["done"], running, pending.Total, counts["retries"], counts["escalations"], len(nodes), offline, costs.Total.CostUSD, costs.Total.Runs, costs.Total.Sources["none"]), nil
}

func (b telegramBackend) Applied(ctx context.Context, update int64) (bool, error) {
	return b.c.Store.NotifyApplied(ctx, b.company, telegramChannel, update)
}

func (b telegramBackend) MarkApplied(ctx context.Context, update int64) error {
	return b.c.Store.MarkNotifyApplied(ctx, b.company, telegramChannel, update)
}
