package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// inTx runs f on a task in one transaction and commits it.
func inTx(t *testing.T, s *Store, f func(tx *Tx) error) {
	t.Helper()
	ctx := context.Background()
	tx, err := s.begin(ctx, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = f(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// toReview submits the task's work: it enters review with the given agent reviewer ("" = a person reviews).
func toReview(t *testing.T, s *Store, id, reviewer, reviewBy string) {
	t.Helper()
	inTx(t, s, func(tx *Tx) error {
		tk, err := taskTx(context.Background(), tx, id)
		if err != nil {
			return err
		}
		tk.Status, tk.Implementer, tk.Reviewer, tk.ReviewBy = "IN_REVIEW", tk.Assignee, reviewer, reviewBy
		tk.Submission = &tasks.Submission{Summary: "done: " + id}
		if reviewer != "" {
			tk.Assignee = reviewer // the review run is the reviewer's
		}
		return saveTask(context.Background(), tx, &tk, "task.submitted")
	})
}

func openReviewItems(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow("SELECT count(*) FROM people_topic_items WHERE company_id=? AND kind='review' AND state='open'", DefaultCompany).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestPersonItems (owner-items-one-source 1.1–1.4): a person's review is an item of their topics, settled when the
// review ends; a task reviewed by an agent and escalated to the owner has exactly one item, the escalation, and no
// review item; the task's alerts give way to its item.
func TestPersonItems(t *testing.T) {
	s := engineStore(t)
	s.Clock = clock.NewFake(time.Now())
	ctx := context.Background()
	co := DefaultCompany
	for _, id := range []string{"owner-review", "member-review", "agent-review"} {
		createEngineTask(t, s, id)
	}

	// The owner reviews: one review item, waiting for the owner, with the review's actions.
	toReview(t, s, "owner-review", "", "owner")
	items, err := s.TaskPersonItems(ctx, co, "owner-review")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "review" || items[0].Person != "owner" || items[0].Title != "owner-review" || strings.Join(items[0].Actions, ",") != "accept,return" || items[0].Topic == "" {
		t.Fatalf("owner review items: %+v", items)
	}
	// A member reviews: the item waits for that member.
	toReview(t, s, "member-review", "", "member:u1")
	items, _ = s.TaskPersonItems(ctx, co, "member-review")
	if len(items) != 1 || items[0].Person != "member:u1" {
		t.Fatalf("member review items: %+v", items)
	}

	// Accepting settles the item; a second sweep changes nothing.
	tk, _ := s.Task(ctx, co, "owner-review")
	if _, err = s.ReviewTask(ctx, co, tk.ID, tasks.Review{Version: tk.Version, Accept: true, RequestID: "accept"}); err != nil {
		t.Fatal(err)
	}
	if items, _ = s.TaskPersonItems(ctx, co, "owner-review"); len(items) != 0 {
		t.Fatalf("accepted review still waits: %+v", items)
	}
	if n := openReviewItems(t, s); n != 1 {
		t.Fatalf("open review items after accept: %d", n)
	}

	// The 2026-11-15 case: a task in review by an agent (the reviewer's run) fails to start and is escalated to the
	// owner. It has exactly one item, the escalation, titled with the task and the cause; no review item.
	toReview(t, s, "agent-review", "ceo", "ceo")
	inTx(t, s, func(tx *Tx) error {
		return openEscalation(ctx, tx, SetupBlockedReason, escalationAnswers[SetupBlockedReason], EscalationWant{TaskID: "agent-review", Summary: "workspace setup failed: dependency missing", Target: "owner"})
	})
	items, err = s.TaskPersonItems(ctx, co, "agent-review")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Kind != "escalation" || items[0].Person != "owner" {
		t.Fatalf("escalated review items: %+v", items)
	}
	if want := "agent-review: review can't start"; !strings.HasPrefix(items[0].Title, want) {
		t.Fatalf("escalation item title %q, want it to start with %q", items[0].Title, want)
	}
	if strings.Join(items[0].Actions, ",") != strings.Join(escalationAnswers[SetupBlockedReason], ",") {
		t.Fatalf("escalation actions %v", items[0].Actions)
	}
	tp, err := s.PeopleTopic(ctx, co, items[0].Topic)
	if err != nil || !strings.HasPrefix(tp.Brief.Title, "agent-review: review can't start") || strings.HasPrefix(tp.Brief.Title, "ceo") {
		t.Fatalf("escalation topic brief %+v %v", tp.Brief, err)
	}
	for _, it := range tp.Items {
		if it.Message != nil && (strings.Contains(it.Message.Text, "Answer with one of") || strings.Contains(it.Message.Text, "Escalation ")) {
			t.Fatalf("people text carries the raw escalation body: %q", it.Message.Text)
		}
	}
	if n := openReviewItems(t, s); n != 1 { // only the member's
		t.Fatalf("open review items: %d", n)
	}

	// The sweep: a task already in a person's review without an item gets one; an item whose task left it settles.
	if _, err = s.DB.Exec("DELETE FROM people_topic_items WHERE company_id=? AND kind='review' AND ref_id='member-review'", co); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncReviewItems(ctx, co); err != nil {
		t.Fatal(err)
	}
	if items, _ = s.TaskPersonItems(ctx, co, "member-review"); len(items) != 1 || items[0].Kind != "review" {
		t.Fatalf("sweep did not create the review item: %+v", items)
	}
	if _, err = s.DB.Exec("UPDATE tasks SET status='DONE', body=(body::jsonb||'{\"status\":\"DONE\"}')::json WHERE company_id=? AND id='member-review'", co); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncReviewItems(ctx, co); err != nil {
		t.Fatal(err)
	}
	if n := openReviewItems(t, s); n != 0 {
		t.Fatalf("sweep did not settle: %d", n)
	}
}

// TestPersonItemsAlerts (owner-items-one-source 1.3): a task asked about in its item is not also alerted.
func TestPersonItemsAlerts(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	createEngineTask(t, s, "asked")
	createEngineTask(t, s, "quiet")
	for _, id := range []string{"asked", "quiet"} {
		if _, err := s.DB.Exec("INSERT INTO task_alerts(company_id,id,kind,task_id,ref_id,summary,status,raised_at) VALUES(?,?,'idle',?,'','idle for a day','active',1)", co, "al-"+id, id); err != nil {
			t.Fatal(err)
		}
	}
	inTx(t, s, func(tx *Tx) error {
		return openEscalation(ctx, tx, "retries_exhausted", escalationAnswers["retries_exhausted"], EscalationWant{TaskID: "asked", Summary: "3 attempts in a row without progress", Target: "owner"})
	})
	alerts, err := s.ActiveAlerts(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].TaskID != "quiet" {
		t.Fatalf("alerts %+v", alerts)
	}
	if in, err := s.InPeopleTopic(ctx, co, alerts[0].RefID); err != nil || in {
		t.Fatalf("an alert's ref is not a topic item: %v %v", in, err)
	}
}

// TestEscalationTemplatesComplete (owner-items-one-source 3.3): every escalation reason has its situation and group
// texts and every answer its effect, in every people language.
func TestEscalationTemplatesComplete(t *testing.T) {
	answers := map[string]bool{ReturnAnswer: true}
	for reason, list := range escalationAnswers {
		for i, lang := range PeopleLanguages {
			if escalationSituation[reason][i] == "" {
				t.Errorf("reason %s has no situation in %s", reason, lang)
			}
			if escalationGroup[reason][i] == "" {
				t.Errorf("reason %s has no group title in %s", reason, lang)
			}
		}
		for _, a := range list {
			answers[a] = true
		}
	}
	for a := range answers {
		for i, lang := range PeopleLanguages {
			if answerEffect[a][i] == "" {
				t.Errorf("answer %s has no effect text in %s", a, lang)
			}
		}
	}
	for reason, texts := range reviewSituation {
		if _, ok := escalationAnswers[reason]; !ok {
			t.Errorf("review situation for unknown reason %s", reason)
		}
		for i, lang := range PeopleLanguages {
			if texts[i] == "" {
				t.Errorf("review situation %s has no text in %s", reason, lang)
			}
		}
	}
	// A brief names the task and the cause, not the agent and a group, unless the topic groups several tasks.
	b := escalationBrief("en", "retries_exhausted", "ceo", "Audit and onboard", "x", escalationAnswers["retries_exhausted"], false, false)
	if b.Title != "Audit and onboard: stopped after several failed runs in a row" {
		t.Fatalf("title %q", b.Title)
	}
	if g := escalationBrief("en", "retries_exhausted", "ceo", "Audit and onboard", "x", nil, false, true); !strings.HasPrefix(g.Title, "ceo: ") {
		t.Fatalf("grouped title %q", g.Title)
	}
	if got := messaging.PeopleText("Escalation retries_exhausted on task abc (T): 3 attempts\nAnswer with one of: retry, cancel."); got != "3 attempts" {
		t.Fatalf("people text %q", got)
	}
}
