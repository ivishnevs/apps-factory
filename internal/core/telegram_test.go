package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
	"appfactory/internal/storage"
)

// fakeBot is the Bot API for tests: it serves queued updates (kept until the offset passes them, as Telegram
// does) and records sent messages. No network, no real token.
type fakeBot struct {
	mu      sync.Mutex
	updates []telegram.Update
	sent    []map[string]any
	next    int64
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
	h.c.Telegram = TelegramConfig{Project: "notes", DefaultReport: "manager"}
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
	if got := bot.texts(); len(got) != 1 || !strings.HasPrefix(got[0], "Bound") {
		t.Fatal(got)
	}
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
	if got := th.bot.texts(); len(got) != 1 {
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
	if len(got) != 3 || !strings.Contains(got[1], "node.restart_loop") || !strings.Contains(got[2], "question from factory") {
		t.Fatal(got)
	}
	markup, _ := th.bot.last()["reply_markup"].(map[string]any)
	if b, _ := json.Marshal(markup); !strings.Contains(string(b), `"text":"unblock"`) || !strings.Contains(string(b), `"text":"cancel"`) {
		t.Fatal(string(b))
	}
	th.tick()
	if len(th.bot.texts()) != 3 {
		t.Fatal("sent again", th.bot.texts())
	}
	// Reminders: one at 4 h, one at 24 h, none in between.
	th.fake.Advance(4 * time.Hour)
	th.tick()
	th.fake.Advance(time.Hour)
	th.tick()
	if got = th.bot.texts(); len(got) != 4 || !strings.HasPrefix(got[3], "Reminder") {
		t.Fatal(got)
	}
	th.fake.Advance(20 * time.Hour)
	th.tick()
	if got = th.bot.texts(); len(got) != 5 || !strings.HasPrefix(got[4], "Reminder") {
		t.Fatal(got)
	}
	// The digest at its local time, once a day.
	th.n.DigestAt = th.fake.Now().UTC().Add(-time.Minute).Format("15:04")
	th.tick()
	th.tick()
	if got = th.bot.texts(); len(got) != 6 || !strings.Contains(got[5], "Daily digest") || !strings.Contains(got[5], "waiting for you: 1") {
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
	if len(kb.Rows) != 2 || kb.Rows[0][0].Text != "unblock" {
		t.Fatal(string(markup))
	}
	press := telegram.Update{UpdateID: 10, Callback: &telegram.CallbackQuery{ID: "cb", Data: kb.Rows[0][0].Data, Message: &telegram.Message{MessageID: int64(sent["message_id"].(int64)), Chat: telegram.Chat{ID: ownerChat}}}}
	th.bot.push(press)
	th.tick()
	esc, _ := th.s.Escalations(ctx, co, false)
	if len(esc) != 1 || esc[0].Status != "answered" || esc[0].Answer != "unblock" || esc[0].Actor != "owner" {
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
	for conv, want := range map[string]string{"telegram-manager": "please plan the release", "telegram-calorie-manager": "please re-check the release notes"} {
		ms, err := th.s.Messages(ctx, co, conv)
		if err != nil || len(ms) != 1 || ms[0].Body != want || ms[0].Sender != "owner" {
			t.Fatalf("%s: %+v %v", conv, ms, err)
		}
	}
	th.bot.push(text(13, "@demo hi"))
	th.tick()
	if got := th.bot.texts(); !strings.Contains(got[len(got)-1], "not one of your direct reports") {
		t.Fatal(got)
	}

	// /stop asks first; nothing stops without the confirmation.
	th.create("live", "code", "reviewer") // demo runs s; notes is busy with the message runs to the managers
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
