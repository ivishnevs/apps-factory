package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/notify/telegram"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestTelegramTopicApprovals (people-topics D10): an artifact approval reaches Telegram as a topic item with the
// brief and an Approve button; a reply rejects it with the reason; a newer version brought to the same topic retires
// the old message (its buttons go, a note says what replaced it) and arrives itself.
func TestTelegramTopicApprovals(t *testing.T) {
	th := newTelegramHarness(t)
	ctx := context.Background()
	th.create("kf", "notes", "demo")
	r := th.start("kf")
	brief := testBrief()
	brief["title"] = "P3-9a key frames"
	brief["ask"] = "Is the lobby screen's style right for production UI?"
	for _, v := range []string{"v3", "v4"} {
		th.mcp(r, "factory_artifact_register", map[string]any{"artifact": tasks.Artifact{ID: "kf2", Version: v, URI: "https://example.com/kf2-" + v + ".png"}, "request_id": "reg-" + v})
	}
	th.mcp(r, "factory_artifact_request_approval", map[string]any{"artifact_id": "kf2", "artifact_version": "v3", "reason": "v3", "request_id": "a3", "brief": brief})
	th.tick()
	got := th.bot.texts()
	if len(got) != 1 || !strings.HasPrefix(got[0], "✅ ") || !strings.Contains(got[0], "P3-9a key frames") || !strings.Contains(got[0], "lobby screen") || strings.Contains(got[0], "sha256") || strings.Contains(got[0], "{") {
		t.Fatalf("%q", got)
	}
	markup, _ := json.Marshal(th.bot.last()["reply_markup"])
	if !strings.Contains(string(markup), `"text":"Approve"`) {
		t.Fatal(string(markup))
	}
	sent := th.bot.last()["message_id"].(int64)
	// A reply rejects with the reason.
	th.bot.push(telegram.Update{UpdateID: 31, Message: &telegram.Message{MessageID: 3031, Chat: telegram.Chat{ID: 4242}, Text: "no tap to play", ReplyTo: &telegram.Message{MessageID: sent}}})
	th.tick()
	list, _ := th.s.Approvals(ctx, storage.DefaultCompany, "notes")
	if len(list) != 1 || list[0].Status != "REJECTED" || list[0].DecisionReason != "no tap to play" {
		t.Fatalf("%+v", list)
	}
	// v4 joins the topic with what changed: the v3 message is retired, v4 arrives.
	th.mcp(r, "factory_artifact_request_approval", map[string]any{"artifact_id": "kf2", "artifact_version": "v4", "reason": "v4", "request_id": "a4", "note": map[string]any{"what": "kf2 lobby", "changes": "no tap to play"}})
	th.tick()
	if len(th.bot.edits) != 1 || int64(th.bot.edits[0]["message_id"].(float64)) != sent {
		t.Fatalf("edits %+v", th.bot.edits)
	}
	got = th.bot.texts()
	last := got[len(got)-1]
	if !strings.Contains(last, "no tap to play") || !strings.HasPrefix(last, "✅ ") {
		t.Fatal(got)
	}
	foundNote := false
	for _, g := range got {
		if strings.HasPrefix(g, "↪ replaced by kf2 v4") {
			foundNote = true
		}
	}
	if !foundNote {
		t.Fatal("no retire note", got)
	}
}

// TestTelegramChoiceApproval (artifact-choice-approvals 3.3): a choice approval reaches Telegram with one button per
// option plus "Changes…" and "Reject"; an option's button chooses it; a reply asks for changes, "reject: <reason>"
// rejects all.
func TestTelegramChoiceApproval(t *testing.T) {
	th := newTelegramHarness(t)
	ctx := context.Background()
	th.create("var", "notes", "demo")
	r := th.start("var")
	var options []any
	for i, v := range []string{"v1", "v2", "v3", "v4", "v5", "v6"} {
		th.mcp(r, "factory_artifact_register", map[string]any{"artifact": tasks.Artifact{ID: "paywall", Version: v, URI: "https://example.com/paywall-" + v + ".png"}, "request_id": "reg-" + v})
		options = append(options, map[string]any{"artifact_id": "paywall", "artifact_version": v, "label": []string{"Mascot", "Editorial", "Dark", "Mascot 2", "Editorial 2", "Dark 2"}[i]})
	}
	ask := func(key string, opts []any) (int64, []telegram.Button) {
		t.Helper()
		th.mcp(r, "factory_artifact_request_approval", map[string]any{"options": opts, "recommended": "2", "reason": "three paywall variations", "request_id": key, "brief": testBrief()})
		th.tick()
		markup, _ := json.Marshal(th.bot.last()["reply_markup"])
		var kb struct {
			Rows [][]telegram.Button `json:"inline_keyboard"`
		}
		_ = json.Unmarshal(markup, &kb)
		var buttons []telegram.Button
		for _, row := range kb.Rows {
			buttons = append(buttons, row...)
		}
		return th.bot.last()["message_id"].(int64), buttons
	}
	sent, buttons := ask("c1", options[:3])
	text := th.bot.texts()[len(th.bot.texts())-1]
	if len(buttons) != 5 || buttons[0].Text != "Mascot" || buttons[3].Text != "Changes…" || buttons[4].Text != "Reject" || !strings.Contains(text, "👉 2. Editorial") {
		t.Fatalf("%q %+v", text, buttons)
	}
	th.bot.push(telegram.Update{UpdateID: 41, Callback: &telegram.CallbackQuery{ID: "cb", Data: buttons[1].Data, Message: &telegram.Message{MessageID: sent, Chat: telegram.Chat{ID: ownerChat}}}})
	th.tick()
	list, _ := th.s.Approvals(ctx, storage.DefaultCompany, "notes")
	if len(list) != 1 || list[0].Status != "APPROVED" || list[0].Chosen != 2 {
		t.Fatalf("%+v", list)
	}
	// A reply asks for changes; "reject: …" rejects all.
	for i, c := range []struct{ reply, status, reason string }{{"mix the mascot with the dark palette", "CHANGES_REQUESTED", "mix the mascot with the dark palette"}, {"Reject: none fits the brand", "REJECTED", "none fits the brand"}} {
		opts := options[3:5]
		if i == 1 {
			opts = []any{options[0], options[5]}
		}
		sent, _ = ask("c"+string(rune('2'+i)), opts)
		th.bot.push(telegram.Update{UpdateID: int64(42 + i), Message: &telegram.Message{MessageID: int64(3042 + i), Chat: telegram.Chat{ID: ownerChat}, Text: c.reply, ReplyTo: &telegram.Message{MessageID: sent}}})
		th.tick()
		list, _ = th.s.Approvals(ctx, storage.DefaultCompany, "notes")
		if got := list[len(list)-1]; got.Status != c.status || got.DecisionReason != c.reason {
			t.Fatalf("%s: %+v", c.reply, got)
		}
	}
}
