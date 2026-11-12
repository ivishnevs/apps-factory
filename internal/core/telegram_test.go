package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
)

// fakeBot is the Bot API for tests: it serves queued updates (kept until the offset passes them, as Telegram
// does) and records sent messages. No network, no real token.
type fakeBot struct {
	mu      sync.Mutex
	updates []telegram.Update
	sent    []map[string]any
	next    int64
	// webhook is the URL setWebhook set ("" after deleteWebhook).
	webhook string
	// edits are editMessageReplyMarkup calls (people-topics D10: replaced versions lose their buttons).
	edits []map[string]any
}

func (b *fakeBot) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var in map[string]any
	_ = json.NewDecoder(r.Body).Decode(&in)
	method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	if !strings.HasPrefix(r.URL.Path, "/botTEST-TOKEN/") {
		http.Error(w, `{"ok":false,"description":"Unauthorized"}`, 401)
		return
	}
	var result any = true
	switch method {
	case "getUpdates":
		offset := int64(in["offset"].(float64))
		out := []telegram.Update{}
		for _, u := range b.updates {
			if u.UpdateID >= offset {
				out = append(out, u)
			}
		}
		result = out
	case "getMe":
		result = telegram.User{ID: 1, Username: "factory_bot"}
	case "setWebhook":
		b.webhook = in["url"].(string)
	case "deleteWebhook":
		b.webhook = ""
	case "editMessageReplyMarkup":
		b.edits = append(b.edits, in)
	case "sendMessage":
		b.next++
		in["message_id"] = b.next
		b.sent = append(b.sent, in)
		result = telegram.Message{MessageID: b.next, Chat: telegram.Chat{ID: int64(in["chat_id"].(float64))}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (b *fakeBot) push(u telegram.Update) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.updates = append(b.updates, u)
}

func (b *fakeBot) texts() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []string{}
	for _, m := range b.sent {
		out = append(out, m["text"].(string))
	}
	return out
}

func (b *fakeBot) to(chat int64) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []string{}
	for _, m := range b.sent {
		if int64(m["chat_id"].(float64)) == chat {
			out = append(out, m["text"].(string))
		}
	}
	return out
}

func (b *fakeBot) hook() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.webhook
}

func (b *fakeBot) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = nil
}

func (b *fakeBot) last() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sent[len(b.sent)-1]
}

const ownerChat = 4242

func text(id int64, s string) telegram.Update {
	return telegram.Update{UpdateID: id, Message: &telegram.Message{MessageID: 1000 + id, Chat: telegram.Chat{ID: ownerChat}, Text: s}}
}

type telegramHarness struct {
	*exitHarness
	bot *fakeBot
	n   *telegram.Notifier
}

func newTelegramHarness(t *testing.T) *telegramHarness {
	h := newExitHarness(t)
	h.c.Telegram, h.c.Teams = TelegramConfig{Project: "notes", Token: "TEST-TOKEN"}, map[string]config.Team{"core": {Spec: config.TeamSpec{Lead: "manager"}}}
	bot := &fakeBot{}
	srv := httptest.NewServer(bot)
	t.Cleanup(srv.Close)
	n := &telegram.Notifier{API: telegram.API{BaseURL: srv.URL, Token: "TEST-TOKEN"}, Backend: h.c.TelegramBackend(), Now: h.fake.Now}
	th := &telegramHarness{h, bot, n}
	// Bind the owner's chat with the one-time code.
	w := h.do(h.c.Token, "POST", "/telegram/bind", nil, nil)
	var bind struct{ Code string }
	if json.Unmarshal(w.Body.Bytes(), &bind) != nil || bind.Code == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	bot.push(text(1, "/start wrong"))
	bot.push(telegram.Update{UpdateID: 2, Message: &telegram.Message{MessageID: 2, Chat: telegram.Chat{ID: 999}, Text: "/status"}})
	bot.push(text(3, "/start "+bind.Code))
	th.tick()
	// A wrong code and a chat that is not connected are told so; the code connects the owner's chat.
	if got := bot.texts(); len(got) != 3 || !strings.Contains(got[0], "expired") || !strings.Contains(got[1], "not connected") || !strings.HasPrefix(got[2], "Connected to") {
		t.Fatal(got)
	}
	bot.reset()
	return th
}

func (th *telegramHarness) tick() {
	th.t.Helper()
	if err := th.n.Tick(context.Background()); err != nil {
		th.t.Fatal(err)
	}
}

// TestTelegramAttentionOnly: a routine handoff sends nothing; a node restart loop and a question to the owner
// do (with option buttons); reminders come at 4 h and 24 h; one digest a day.
func TestTelegramAttentionOnly(t *testing.T) {
	th := newTelegramHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	th.create("h", "notes", "demo")
	r := th.start("h")
	th.mcp(r, "factory_exit_handoff", map[string]any{"to": "reviewer", "note": "for review"})
	th.end(r, "succeeded", "accepted")
	th.tick()
	if got := th.bot.texts(); len(got) != 0 {
		t.Fatal("routine events reached Telegram", got)
	}
	if err := th.s.AddFeed(ctx, co, storage.FeedItem{EventID: "loop", Type: "node.restart_loop", EntityID: "node:n1", Summary: "node mini: 4 restarts within 15m0s"}); err != nil {
		t.Fatal(err)
	}
	th.create("s", "notes", "demo")
	if err := th.s.SyncEscalations(ctx, co, "stalled", []storage.EscalationWant{{TaskID: "s", RefID: "x", Summary: "waits on x", Target: "owner"}}); err != nil {
		t.Fatal(err)
	}
	th.tick()
	got := th.bot.texts()
	if len(got) != 2 || !strings.Contains(got[0], "node.restart_loop") || !strings.HasPrefix(got[1], "❓ ") {
		t.Fatal(got)
	}
	markup, _ := th.bot.last()["reply_markup"].(map[string]any)
	if b, _ := json.Marshal(markup); !strings.Contains(string(b), `"text":"Keep waiting"`) || !strings.Contains(string(b), `"text":"Stop waiting for this task"`) || !strings.Contains(string(b), `"text":"cancel"`) {
		t.Fatal(string(b))
	}
	th.tick()
	if len(th.bot.texts()) != 2 {
		t.Fatal("sent again", th.bot.texts())
	}
	// Reminders: one at 4 h, one at 24 h, none in between.
	th.fake.Advance(4 * time.Hour)
	th.tick()
	th.fake.Advance(time.Hour)
	th.tick()
	if got = th.bot.texts(); len(got) != 3 || !strings.HasPrefix(got[2], "Reminder") {
		t.Fatal(got)
	}
	th.fake.Advance(20 * time.Hour)
	th.tick()
	if got = th.bot.texts(); len(got) != 4 || !strings.HasPrefix(got[3], "Reminder") {
		t.Fatal(got)
	}
	// The digest at its local time, once a day.
	th.n.DigestAt = th.fake.Now().UTC().Add(-time.Minute).Format("15:04")
	th.tick()
	th.tick()
	if got = th.bot.texts(); len(got) != 5 || !strings.Contains(got[4], "Daily digest") || !strings.Contains(got[4], "waiting for you: 1") {
		t.Fatal(got)
	}
}

// TestTelegramInbound: a button answers the question; a duplicate update is applied once; @name addresses a
// direct report and other messages go to the default one; /stop without confirmation does nothing.
func TestTelegramInbound(t *testing.T) {
	th := newTelegramHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	th.create("s", "notes", "demo")
	if err := th.s.SyncEscalations(ctx, co, "stalled", []storage.EscalationWant{{TaskID: "s", RefID: "x", Summary: "waits on x", Target: "owner"}}); err != nil {
		t.Fatal(err)
	}
	th.tick()
	sent := th.bot.last()
	markup, _ := json.Marshal(sent["reply_markup"])
	var kb struct {
		Rows [][]telegram.Button `json:"inline_keyboard"`
	}
	_ = json.Unmarshal(markup, &kb)
	if len(kb.Rows) != 3 || kb.Rows[1][0].Text != "Stop waiting for this task" {
		t.Fatal(string(markup))
	}
	press := telegram.Update{UpdateID: 10, Callback: &telegram.CallbackQuery{ID: "cb", Data: kb.Rows[1][0].Data, Message: &telegram.Message{MessageID: int64(sent["message_id"].(int64)), Chat: telegram.Chat{ID: ownerChat}}}}
	th.bot.push(press)
	th.tick()
	esc, _ := th.s.Escalations(ctx, co, false)
	if len(esc) != 1 || esc[0].Status != "answered" || esc[0].Answer != "drop_dependency" || esc[0].Actor != "owner" {
		t.Fatalf("%+v", esc)
	}

	// Owner messages: unaddressed to the default direct report, @name to that one; a repeated update once.
	th.bot.push(text(11, "please plan the release"))
	th.bot.push(text(12, "@calorie-manager please re-check the release notes"))
	th.tick()
	st, _ := th.n.Backend.LoadState(ctx)
	st.UpdateOffset = 11 // Telegram delivers 11 and 12 again (an offset that was not saved)
	if err := th.n.Backend.SaveState(ctx, st); err != nil {
		t.Fatal(err)
	}
	th.tick()
	// They are owner messages like POST /messages: the owner conversation with the agent, delivered as a
	// trigger on the agent's conversation task (hierarchy D5).
	// Plain text answers the last message (the escalation, already answered: its project's lead gets it); @name
	// writes to that lead with the text alone.
	for agent, want := range map[string]string{"manager": "please plan the release", "calorie-manager": "please re-check the release notes"} {
		ms, err := th.s.Messages(ctx, co, "owner-"+agent+"-notes")
		if err != nil || len(ms) != 1 || ms[0].Body != want || ms[0].Sender != "owner" || ms[0].Recipient != agent {
			t.Fatalf("%s: %+v %v", agent, ms, err)
		}
		trs, err := th.s.PendingTriggers(ctx, co, storage.ConversationID("notes", agent))
		if err != nil || len(trs) != 1 || trs[0].Kind != "message" || !strings.Contains(trs[0].Text, ms[0].ID) || !trs[0].Authority {
			t.Fatalf("%s: %+v %v", agent, trs, err)
		}
	}
	th.bot.push(text(13, "@demo hi"))
	th.tick()
	if got := th.bot.texts(); !strings.Contains(got[len(got)-1], "not one of your direct reports") {
		t.Fatal(got)
	}

	// /stop asks first; nothing stops without the confirmation.
	th.create("live", "code", "reviewer") // demo runs s; notes is busy with the managers' conversation runs
	r := th.start("live")
	th.bot.push(text(14, "/stop"))
	th.tick()
	if run, _ := th.s.Get(ctx, co, r.id); run.Status != "CLAIMED" && run.Status != "RUNNING" {
		t.Fatal("stopped without confirmation", run.Status)
	}
	if p, _ := th.s.Pauses(ctx, co); len(p) != 0 {
		t.Fatal(p)
	}
	th.fake.Advance(6 * time.Minute)
	th.bot.push(text(15, "/stop confirm"))
	th.tick()
	if run, _ := th.s.Get(ctx, co, r.id); run.Status == "CANCELLING" {
		t.Fatal("a late confirmation stopped the company")
	}
	th.bot.push(text(16, "/stop"))
	th.bot.push(text(17, "/stop confirm"))
	th.tick()
	if run, _ := th.s.Get(ctx, co, r.id); run.Status != "CANCELLING" {
		t.Fatal(run.Status)
	}
	if p, _ := th.s.Pauses(ctx, co); len(p) != 1 || p[0].Scope != "company" {
		t.Fatal(p)
	}
	th.bot.push(text(18, "/status"))
	th.tick()
	if got := th.bot.texts(); !strings.Contains(got[len(got)-1], "paused: company") {
		t.Fatal(got)
	}
	// A reply to a question message answers it with text.
	q, err := th.s.SendMessage(ctx, co, messaging.Send{ProjectID: "notes", Sender: "manager", Recipient: "owner", MessageType: "question", Body: "Which store region?", RequestID: "q-region"})
	if err != nil {
		t.Fatal(err)
	}
	th.tick()
	asked := th.bot.last()
	th.bot.push(telegram.Update{UpdateID: 19, Message: &telegram.Message{MessageID: 2019, Chat: telegram.Chat{ID: ownerChat}, Text: "EU first", ReplyTo: &telegram.Message{MessageID: asked["message_id"].(int64)}}})
	th.tick()
	if m, _ := th.s.Message(ctx, co, q.ID); m.Status != "ANSWERED" {
		t.Fatalf("%+v", m)
	}
}

// TestTelegramTokenNotInErrors: a failing Bot API call names the method, never the token.
func TestTelegramTokenNotInErrors(t *testing.T) {
	api := telegram.API{BaseURL: "http://127.0.0.1:1", Token: "SECRET-TOKEN"}
	_, err := api.Send(context.Background(), 1, "x", nil, 0)
	if err == nil || strings.Contains(err.Error(), "SECRET-TOKEN") || !strings.Contains(err.Error(), "sendMessage") {
		t.Fatal(err)
	}
}

// TestTelegramCompanyBot (telegram-per-company): an admin adds the company's bot by its token (checked with
// getMe, sealed under the company key); with an https address Core sets a webhook and takes updates only with
// its secret; each person connects their own chat with a t.me link; questions reach owners only, members only
// their digest; a person who leaves the company is disconnected; removing the bot stops it.
func TestTelegramCompanyBot(t *testing.T) {
	h := newExitHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	co := storage.DefaultCompany
	keys, _ := secrets.NewMasterKeys(bytes.Repeat([]byte{7}, secrets.KeySize), nil)
	h.s.Keys = keys
	bot := &fakeBot{}
	srv := httptest.NewServer(bot)
	t.Cleanup(srv.Close)
	h.c.PublicURL = "https://app.example"
	h.c.Telegram = TelegramConfig{Project: "notes", APIBase: srv.URL}
	h.c.Teams = map[string]config.Team{"core": {Spec: config.TeamSpec{Lead: "manager"}}}
	go h.c.RunTelegram(ctx)

	if w := h.do(h.c.Token, "POST", "/telegram/connect", nil, nil); w.Code != 409 {
		t.Fatal("connect without a bot", w.Code)
	}
	if w := h.do(h.c.Token, "PUT", "/telegram/bot", map[string]string{"token": "WRONG"}, nil); w.Code != 400 {
		t.Fatal("a token Telegram refuses", w.Code, w.Body.String())
	}
	if w := h.do(h.c.Token, "PUT", "/telegram/bot", map[string]string{"token": "TEST-TOKEN"}, nil); w.Code != 200 || !strings.Contains(w.Body.String(), "factory_bot") {
		t.Fatal(w.Code, w.Body.String())
	}
	var view TelegramView
	if w := h.do(h.c.Token, "GET", "/telegram", nil, nil); json.Unmarshal(w.Body.Bytes(), &view) != nil || view.Bot == nil || view.Bot.Username != "factory_bot" || strings.Contains(w.Body.String(), "TEST-TOKEN") {
		t.Fatal(w.Body.String())
	}
	stored, err := h.s.TelegramBotOf(ctx, co)
	if err != nil || stored.Token != "TEST-TOKEN" {
		t.Fatal(stored, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for bot.hook() != "https://app.example/telegram/hook/default" {
		if time.Now().After(deadline) {
			t.Fatal("no webhook", bot.hook())
		}
		time.Sleep(20 * time.Millisecond)
	}
	hook := func(secret string, u telegram.Update) int {
		t.Helper()
		b, _ := json.Marshal(u)
		r := httptest.NewRequest("POST", "/telegram/hook/default", bytes.NewReader(b))
		r.Header.Set("X-Telegram-Bot-Api-Secret-Token", secret)
		w := httptest.NewRecorder()
		h.c.Handler().ServeHTTP(w, r)
		return w.Code
	}
	connect := func(token string, header map[string]string) string {
		t.Helper()
		var out struct{ Code, URL string }
		w := h.do(token, "POST", "/telegram/connect", nil, header)
		if json.Unmarshal(w.Body.Bytes(), &out) != nil || out.URL != "https://t.me/factory_bot?start="+out.Code {
			t.Fatal(w.Code, w.Body.String())
		}
		return out.Code
	}
	if code := hook("wrong", text(1, "/start x")); code != 403 {
		t.Fatal("wrong secret", code)
	}
	if code := hook(stored.HookSecret, telegram.Update{UpdateID: 1, Message: &telegram.Message{MessageID: 1, Chat: telegram.Chat{ID: ownerChat}, Text: "/start " + connect(h.c.Token, nil)}}); code != 200 {
		t.Fatal(code)
	}
	if got := bot.to(ownerChat); len(got) != 1 || !strings.HasPrefix(got[0], "Connected to") {
		t.Fatal(got)
	}

	// A member connects their own chat (an owner token of the CLI binds the owner; a member's code is made here
	// as the website does for their session).
	if err = h.s.CreateUser(ctx, storage.DefaultAccount, "u-mia", "mia@example.com"); err != nil {
		t.Fatal(err)
	}
	if err = h.s.AddMember(ctx, co, "u-mia", storage.RoleMember); err != nil {
		t.Fatal(err)
	}
	const miaChat = 777
	if err = h.c.updateTelegramPerson(ctx, co, "u-mia", func(st *telegram.State, who string) error {
		st.Binds["mia-code"] = telegram.Bind{Who: who, Expires: h.fake.Now().Add(time.Minute).Unix()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	hook(stored.HookSecret, telegram.Update{UpdateID: 2, Message: &telegram.Message{MessageID: 2, Chat: telegram.Chat{ID: miaChat}, Text: "/start mia-code"}})
	hook(stored.HookSecret, telegram.Update{UpdateID: 3, Message: &telegram.Message{MessageID: 3, Chat: telegram.Chat{ID: miaChat}, Text: "/stop"}})
	if got := bot.to(miaChat); len(got) != 2 || !strings.HasPrefix(got[0], "Connected to") || !strings.Contains(got[1], "Only owners and admins") {
		t.Fatal(got)
	}
	// A question to the owner: the owner's chat only.
	if _, err = h.s.SendMessage(ctx, co, messaging.Send{ProjectID: "notes", Sender: "manager", Recipient: "owner", MessageType: "question", Body: "Ship on Friday?", RequestID: "q-ship"}); err != nil {
		t.Fatal(err)
	}
	h.c.kickTelegram()
	deadline = time.Now().Add(10 * time.Second)
	for len(bot.to(ownerChat)) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("question not sent", bot.to(ownerChat))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := bot.to(ownerChat); !strings.HasPrefix(got[1], "❓ manager · core team") || !strings.Contains(got[1], "Ship on Friday?") {
		t.Fatal(got)
	}
	if got := bot.to(miaChat); len(got) != 2 {
		t.Fatal("a member got the owner's question", got)
	}
	// Mia leaves: her chat is disconnected on the next message.
	if err = h.s.RemoveMember(ctx, co, "u-mia"); err != nil {
		t.Fatal(err)
	}
	hook(stored.HookSecret, telegram.Update{UpdateID: 4, Message: &telegram.Message{MessageID: 4, Chat: telegram.Chat{ID: miaChat}, Text: "hello"}})
	if got := bot.to(miaChat); !strings.Contains(got[len(got)-1], "no longer in") {
		t.Fatal(got)
	}
	// Removing the bot: its webhook is deleted and its updates are refused.
	if w := h.do(h.c.Token, "DELETE", "/telegram/bot", nil, nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	deadline = time.Now().Add(5 * time.Second)
	for hook(stored.HookSecret, text(5, "/status")) != 404 {
		if time.Now().After(deadline) {
			t.Fatal("bot still served")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if bot.hook() != "" {
		t.Fatal("webhook left", bot.hook())
	}
}

// TestTelegramRouting (telegram-per-company): with two leads writing to the owner, a reply goes to exactly that
// message, plain text answers the last message, @name writes to that lead, and the bot says where it went;
// before any message plain text asks whom it is for.
func TestTelegramRouting(t *testing.T) {
	th := newTelegramHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	th.bot.push(text(20, "hello there"))
	th.tick()
	if got := th.bot.texts(); len(got) != 1 || !strings.Contains(got[0], "say whom it is for") {
		t.Fatal(got)
	}
	ask := func(from, body string) (messaging.Message, int64) {
		t.Helper()
		m, err := th.s.SendMessage(ctx, co, messaging.Send{ProjectID: "notes", Sender: from, Recipient: "owner", MessageType: "question", Body: body, RequestID: "q-" + from})
		if err != nil {
			t.Fatal(err)
		}
		th.tick()
		return m, th.bot.last()["message_id"].(int64)
	}
	qa, msgA := ask("manager", "Which region first?")
	qb, _ := ask("calorie-manager", "Which font?")
	// Plain text answers the last message: calorie-manager's question.
	th.bot.push(text(21, "Inter"))
	th.tick()
	if m, _ := th.s.Message(ctx, co, qb.ID); m.Status != "ANSWERED" {
		t.Fatalf("%+v", m)
	}
	if got := th.bot.texts(); !strings.HasPrefix(got[len(got)-1], "Answered calorie-manager") {
		t.Fatal(got)
	}
	// A reply to manager's message answers that one.
	th.bot.push(telegram.Update{UpdateID: 22, Message: &telegram.Message{MessageID: 2022, Chat: telegram.Chat{ID: ownerChat}, Text: "EU", ReplyTo: &telegram.Message{MessageID: msgA}}})
	th.tick()
	if m, _ := th.s.Message(ctx, co, qa.ID); m.Status != "ANSWERED" {
		t.Fatalf("%+v", m)
	}
	// Plain text after the answer continues calorie-manager's conversation.
	th.bot.push(text(23, "and use 16px"))
	th.tick()
	if got := th.bot.texts(); !strings.HasPrefix(got[len(got)-1], "→ calorie-manager · ") {
		t.Fatal(got)
	}
	ms, err := th.s.Messages(ctx, co, "owner-calorie-manager-notes")
	if err != nil || len(ms) != 1 || ms[0].Body != "and use 16px" {
		t.Fatalf("%+v %v", ms, err)
	}
	// @name writes to that lead.
	th.bot.push(text(24, "@manager status?"))
	th.tick()
	if got := th.bot.texts(); !strings.HasPrefix(got[len(got)-1], "→ manager") {
		t.Fatal(got)
	}
}
