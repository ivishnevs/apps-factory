package core

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/decisions"

	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
	"appfactory/internal/storage"
)

// TestTelegramReplyRouting (telegram-reply-routing 2.1, 2.2): an agent's reply in an owner chat goes to the Telegram
// chat of the person whose last message there came from Telegram; after a web message it stays in the web.
func TestTelegramReplyRouting(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	co := storage.DefaultCompany
	if _, w := h.create(map[string]any{"id": "tg", "name": "TG", "workspace": "/tmp/tg"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	conv, err := h.s.CreateConversation(ctx, co, messaging.ConversationCreate{ID: "owner-demo-tg", ProjectID: "tg", Agent: "demo", Title: "Owner and demo", RequestID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	b := telegramBackend{c: h.c, company: co}
	say := func(ctx context.Context, body, key string) {
		t.Helper()
		if _, err := h.s.SendMessage(ctx, co, messaging.Send{ProjectID: "tg", Sender: "owner", Recipient: "demo", ConversationID: conv.ID, MessageType: "notification", Body: body, RequestID: key}); err != nil {
			t.Fatal(err)
		}
	}
	reply := func(body, key string) {
		t.Helper()
		if _, err := h.s.SendMessage(ctx, co, messaging.Send{ProjectID: "tg", Sender: "demo", Recipient: "owner", ConversationID: conv.ID, MessageType: "notification", Body: body, RequestID: key}); err != nil {
			t.Fatal(err)
		}
	}
	routed := func(who string) []string {
		t.Helper()
		items, err := b.Replies(ctx, who, telegram.RoleAdmin, 0)
		if err != nil {
			t.Fatal(err)
		}
		var texts []string
		for _, it := range items {
			if it.Text != "" {
				texts = append(texts, it.Text)
			}
		}
		return texts
	}
	mia := storage.WithPerson(ctx, storage.Person{ID: "u-mia", Name: "Mia", Channel: "telegram"})
	say(mia, "status?", "s1")
	reply("all green", "r1")
	if got := routed("u-mia"); len(got) != 1 {
		t.Fatalf("reply to Mia's Telegram message: %v", got)
	}
	if got := routed("u-other"); len(got) != 0 {
		t.Fatalf("another person got it: %v", got)
	}
	web := storage.WithPerson(ctx, storage.Person{ID: "u-mia", Name: "Mia", Channel: "web"})
	say(web, "and now?", "s2")
	reply("still green", "r2")
	if got := routed("u-mia"); len(got) != 1 {
		t.Fatalf("a reply to a web message went to Telegram: %v", got)
	}
}

// TestTelegramDecisionButtons (decision-approval-ux 2.2): a proposal reaches Telegram with its options; a choice
// approves that option.
func TestTelegramDecisionButtons(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	co := storage.DefaultCompany
	if _, w := h.create(map[string]any{"id": "tgd", "name": "TGD", "workspace": "/tmp/tgd"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	d, err := h.s.ProposeDecision(ctx, co, "tgd", decisions.Propose{Topic: "map size", Value: "small", Alternatives: []string{"medium", "large"}, Rationale: "3v3", Actor: "demo", RequestID: "p"})
	if err != nil {
		t.Fatal(err)
	}
	b := telegramBackend{c: h.c, company: co}
	items, err := b.Attention(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, it := range items {
		if len(it.Options) == 3 && it.Options[1] == "medium" {
			target = it.Target.Question
		}
	}
	if target == "" {
		t.Fatalf("no proposal with options in %+v", items)
	}
	if out, err := b.Choose(ctx, target, 1, "k"); err != nil || !strings.Contains(out, "medium") {
		t.Fatal(out, err)
	}
	if got, _ := h.s.Decision(ctx, co, d.ID); got.Status != "approved" || got.Value != "medium" {
		t.Fatalf("%+v", got)
	}
}
