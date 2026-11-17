package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/storage"
)

// TestOwnerReviewOneItem (owner-items-one-source 1.1, 1.2, 2.5): work submitted for the owner's review is one review
// item in the owner's topics. The task view carries it as person_items, and Telegram sends it once, as that item,
// with no raw attention line for the submission.
func TestOwnerReviewOneItem(t *testing.T) {
	th := newTelegramHarness(t)
	th.createReviewedBy("rv", "notes", "demo", "owner")
	r := th.start("rv")
	if w := th.exitAt(r, "submit", map[string]any{"summary": "notes written"}, th.ack(r), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	th.end(r, "succeeded", "accepted")
	var view struct {
		Status      string               `json:"status"`
		PersonItems []storage.PersonItem `json:"person_items"`
	}
	w := th.do(th.c.Token, "GET", "/tasks/rv", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil || view.Status != "IN_REVIEW" {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(view.PersonItems) != 1 || view.PersonItems[0].Kind != "review" || view.PersonItems[0].Person != "owner" || view.PersonItems[0].Topic == "" {
		t.Fatalf("person_items %+v", view.PersonItems)
	}
	// The list carries the same items.
	var list []struct {
		ID          string               `json:"id"`
		PersonItems []storage.PersonItem `json:"person_items"`
	}
	w = th.do(th.c.Token, "GET", "/tasks?project=notes", nil, nil)
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, x := range list {
		if want := map[bool]int{true: 1, false: 0}[x.ID == "rv"]; len(x.PersonItems) != want || x.PersonItems == nil {
			t.Fatalf("list %s person_items %+v", x.ID, x.PersonItems)
		}
	}
	th.tick()
	got := th.bot.texts()
	if len(got) != 1 || !strings.HasPrefix(got[0], "🔍 ") {
		t.Fatalf("telegram %q", got)
	}
	for _, g := range got {
		if strings.HasPrefix(g, "⚠") {
			t.Fatalf("a raw line for a cause with an item: %q", got)
		}
	}
}

// TestEscalatedReviewOnce (owner-items-one-source 2.7, the 2026-11-15 case with fixtures): a task in review by an agent
// that is escalated to the owner reaches the owner once — one topic with one waiting item, the escalation, which the
// task view lists linked to that topic — and never as a review for the owner; the badge equals the Inbox.
func TestEscalatedReviewOnce(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.createReviewedBy("ev", "notes", "demo", "reviewer")
	r := h.start("ev")
	if w := h.exitAt(r, "submit", map[string]any{"summary": "notes written"}, h.ack(r), nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	if tk := h.task("ev"); tk.Status != "IN_REVIEW" || tk.Reviewer != "reviewer" {
		t.Fatalf("%+v", tk)
	}
	if err := h.s.SyncEscalations(ctx, co, "stalled", []storage.EscalationWant{{TaskID: "ev", RefID: "dep", Summary: "waits on dep", Target: "owner"}}); err != nil {
		t.Fatal(err)
	}
	var topics []storage.TopicView
	if w := h.do(h.c.Token, "GET", "/topics?open=1", nil, nil); json.Unmarshal(w.Body.Bytes(), &topics) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	var waiting []storage.ItemView
	topic := ""
	for _, tp := range topics {
		for _, it := range tp.Items {
			if it.Waits && it.Task != nil && it.Task.ID == "ev" {
				waiting, topic = append(waiting, it), tp.ID
			}
		}
	}
	if len(waiting) != 1 || waiting[0].Kind != "escalation" || waiting[0].Person != "owner" {
		t.Fatalf("waiting items of the task: %+v", waiting)
	}
	var view struct {
		PersonItems []storage.PersonItem `json:"person_items"`
	}
	if w := h.do(h.c.Token, "GET", "/tasks/ev", nil, nil); json.Unmarshal(w.Body.Bytes(), &view) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(view.PersonItems) != 1 || view.PersonItems[0].Topic != topic || view.PersonItems[0].Kind != "escalation" || !strings.HasPrefix(view.PersonItems[0].Title, "ev: ") {
		t.Fatalf("task page items %+v (topic %s)", view.PersonItems, topic)
	}
	v := h.ownerView() // checks the badge equals the Inbox
	if v.pending.OwnerReviews != 0 {
		t.Fatalf("an agent's review counted as the owner's: %+v", v.pending)
	}
}
