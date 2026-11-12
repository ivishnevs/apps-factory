package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// inbox-badge-parity: the badge counts what the Inbox lists, no_progress skips recorded waits, and promised details
// keep the question open while the asking task waits for them without a timer.

// ownerView is what the owner's Inbox shows and what the badge counts.
type ownerView struct {
	inbox   []messaging.Message
	alerts  []storage.ActiveAlert
	pending storage.PendingCounts
}

func (h *exitHarness) ownerView() ownerView {
	h.t.Helper()
	var v ownerView
	for path, out := range map[string]any{"/inbox": &v.inbox, "/alerts": &v.alerts, "/pending/count": &v.pending} {
		w := h.do("owner-secret", "GET", path, nil, nil)
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), out) != nil {
			h.t.Fatal(path, w.Code, w.Body.String())
		}
	}
	// The badge equals the page: its messages, the reviews for the owner and the Alerts section.
	if v.pending.Total != len(v.inbox)+len(v.alerts)+v.pending.OwnerReviews {
		h.t.Fatalf("badge %d, Inbox %d messages + %d alerts + %d reviews", v.pending.Total, len(v.inbox), len(v.alerts), v.pending.OwnerReviews)
	}
	return v
}

func (h *exitHarness) askOwner(task, question string) (exitRun, messaging.Message) {
	h.t.Helper()
	r := h.start(task)
	if w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_exit_ask", Arguments: map[string]any{"question": question, "options": []any{"Personal team", "Company team"}}}, nil); w.Code != 200 {
		h.t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
	q, err := h.s.Message(context.Background(), storage.DefaultCompany, h.task(task).WaitMessageID)
	if err != nil || q.Recipient != "owner" || !q.Blocking {
		h.t.Fatalf("%+v %v", q, err)
	}
	return r, q
}

func (h *exitHarness) answer(q messaging.Message, body, details string) (int, messaging.Message) {
	h.t.Helper()
	cur, err := h.s.Message(context.Background(), storage.DefaultCompany, q.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	w := h.do("owner-secret", "POST", "/inbox/"+q.ID+"/answer", map[string]any{"version": cur.Version, "body": body, "details": details, "request_id": "answer-" + q.ID + "-" + details + "-" + body}, nil)
	var m messaging.Message
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

func (h *exitHarness) message(id string) messaging.Message {
	h.t.Helper()
	m, err := h.s.Message(context.Background(), storage.DefaultCompany, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return m
}

// TestAlertsListEqualsBadge (1.1, 1.2): with no message open, every active alert (any kind, comment_unhandled too) is
// listed with its task, project, summary, since and clearing action, an alert the lead can act on is marked with the
// lead, and the badge counts exactly the list.
func TestAlertsListEqualsBadge(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("dev", "notes", "demo")     // demo reports to manager
	h.create("lead", "notes", "manager") // manager reports to the owner
	if v := h.ownerView(); v.pending.Total != 0 || len(v.alerts) != 0 {
		t.Fatalf("%+v", v)
	}
	if err := h.s.SyncAlerts(ctx, co, "no_progress", []storage.Alert{{TaskID: "dev", Summary: "no progress in 3 runs"}}); err != nil {
		t.Fatal(err)
	}
	if err := h.s.SyncAlerts(ctx, co, "comment_unhandled", []storage.Alert{{TaskID: "lead", RefID: "c1", Summary: "your comment was not handled in 3 runs"}}); err != nil {
		t.Fatal(err)
	}
	v := h.ownerView()
	if len(v.inbox) != 0 || v.pending.Total != 2 || v.pending.Alerts != 2 || len(v.alerts) != 2 {
		t.Fatalf("%+v", v)
	}
	by := map[string]storage.ActiveAlert{}
	for _, a := range v.alerts {
		by[a.Kind] = a
	}
	np, cu := by["no_progress"], by["comment_unhandled"]
	if np.TaskID != "dev" || np.TaskTitle != "dev" || np.ProjectID != "notes" || np.Summary != "no progress in 3 runs" || np.Since != h.fake.Now().Unix() || np.Action == "" || np.With != "manager" {
		t.Fatalf("no_progress: %+v", np)
	}
	if cu.TaskID != "lead" || cu.RefID != "c1" || cu.With != "" || cu.Action == "" {
		t.Fatalf("comment_unhandled: %+v", cu)
	}
	// The condition goes away: the alert leaves the list and the badge together.
	if err := h.s.SyncAlerts(ctx, co, "no_progress", nil); err != nil {
		t.Fatal(err)
	}
	if v = h.ownerView(); v.pending.Total != 1 || len(v.alerts) != 1 || v.alerts[0].Kind != "comment_unhandled" {
		t.Fatalf("%+v", v)
	}
}

// TestNoProgressSkipsRecordedWaits (2.1): the producer's task that keeps waiting for the owner ("still waiting for
// your answer") raises no no_progress alert, neither while parked nor while it runs again; once a run ends without
// progress and without a recorded wait, it does (TestNoProgressAlert covers a task that never waited).
func TestNoProgressSkipsRecordedWaits(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("m7", "notes", "manager")
	sweep := func(d time.Duration) map[string]bool {
		t.Helper()
		h.fake.Advance(d)
		if err := h.s.ExpireWaits(ctx, co); err != nil {
			t.Fatal(err)
		}
		if err := h.c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
		alerts, err := h.s.ActiveAlerts(ctx, co)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, a := range alerts {
			if a.Kind == "no_progress" {
				out[a.TaskID] = true
			}
		}
		return out
	}
	deadline := h.at(72 * time.Hour) // one wait episode, one deadline
	for i := range 3 {
		r := h.start("m7")
		if code, ref := h.wait(r, map[string]any{"reason": "still waiting for your answer on the Apple team (Q12), which you said you'd send separately", "after": "3h", "deadline": deadline}); code != 200 {
			t.Fatal(code, ref)
		}
		h.end(r, "succeeded", "accepted")
		if v := h.why("m7"); v.Reason != tasks.ReasonParked+tasks.ParkWait {
			t.Fatalf("run %d: %+v", i, v)
		}
		if a := sweep(3*time.Hour + time.Minute); a["m7"] {
			t.Fatalf("run %d: a recorded wait raised no_progress", i)
		}
	}
	// The waiting task runs again (its timer fired): its live run has no park, its last run waited.
	r := h.start("m7")
	a := sweep(time.Minute)
	if a["m7"] {
		t.Fatal("no_progress while the waiting task runs again")
	}
	// A run that ends without progress and without a wait: the alert comes.
	if err := h.s.Apply(ctx, co, protocol.Wrap("agent.ended", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Status: "ENDED", Outcome: "failed"})); err != nil {
		t.Fatal(err)
	}
	h.fake.Advance(time.Hour) // past the backoff: READY, no wait recorded
	if a = sweep(time.Minute); !a["m7"] {
		t.Fatal("no alert once the task stopped waiting", a)
	}
}

// TestPromisedDetailsReplayFE707C54 (3.1-3.3, 4.1): the producer asks Q12, the owner answers "I'll send details": the
// question stays open in the Inbox as details promised, the producer's task waits for the owner without runs and
// without a no_progress alert, the badge equals the page, and the details resume it with them in its brief.
func TestPromisedDetailsReplayFE707C54(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("fe707c54", "notes", "manager")
	r, q := h.askOwner("fe707c54", "Which Apple team should TestFlight builds use (Q12)?")

	code, got := h.answer(q, "Other — I'll write details", messaging.DetailsPromised)
	if code != 200 || got.ID != q.ID || got.Details != messaging.DetailsPromised || got.DetailsNote != "Other — I'll write details" || got.Status != "PENDING" {
		t.Fatal(code, got)
	}
	if code, _ = h.answer(q, "again", messaging.DetailsPromised); code != 409 {
		t.Fatal("promised twice", code)
	}
	v := h.ownerView()
	if len(v.inbox) != 1 || v.inbox[0].ID != q.ID || v.inbox[0].Details != messaging.DetailsPromised || v.pending.Total != 1 {
		t.Fatalf("%+v", v)
	}
	why := h.why("fe707c54")
	if why.Reason != tasks.ReasonParked+tasks.ParkMessage || why.Clearer.Who != "owner" || why.Awaits == nil || why.Awaits.From != "owner" ||
		!strings.Contains(why.Detail, "the details for question "+q.ID) || !strings.Contains(why.Detail, "promised") {
		t.Fatalf("%+v %+v", why, why.Awaits)
	}
	if h.parkStatus("fe707c54", tasks.ParkAsk) != "cleared" {
		t.Fatal("ask park still active")
	}
	// Days pass: no timer reruns it, the deadline does not escalate the question, no no_progress alert is raised.
	h.idle("fe707c54", r.id, 48*time.Hour)
	if err := h.s.ExpireMessages(ctx, co); err != nil {
		t.Fatal(err)
	}
	if err := h.c.sweepStalls(ctx); err != nil {
		t.Fatal(err)
	}
	if m := h.message(q.ID); m.Status != "PENDING" || m.Details != messaging.DetailsPromised {
		t.Fatalf("%+v", m)
	}
	if v = h.ownerView(); len(v.alerts) != 0 || len(v.inbox) != 1 || v.pending.Total != 1 {
		t.Fatalf("%+v", v)
	}
	// Telegram lists the open item and says the details are promised; the details sent there close it.
	b := telegramBackend{c: h.c, company: co}
	pending, err := b.PendingQuestions(ctx)
	if err != nil || len(pending) != 1 || !strings.Contains(pending[0].Text, "Details promised") {
		t.Fatalf("%+v %v", pending, err)
	}
	if _, err = b.Reply(ctx, telegram.Target{Question: q.ID}, "Company team, ID 7XK2", "tg-details"); err != nil {
		t.Fatal(err)
	}
	if m := h.message(q.ID); m.Status != "ANSWERED" || m.Details != messaging.DetailsSent {
		t.Fatalf("%+v", m)
	}
	if v = h.ownerView(); v.pending.Total != 0 || len(v.inbox) != 0 {
		t.Fatalf("%+v", v)
	}
	if h.parkStatus("fe707c54", tasks.ParkMessage) != "cleared" {
		t.Fatal("still parked")
	}
	if why = h.why("fe707c54"); why.Reason != tasks.ReasonRunnable {
		t.Fatalf("%+v", why)
	}
	next := h.start("fe707c54")
	run, _ := h.s.Get(ctx, co, next.id)
	if !strings.Contains(run.Spec.Prompt, "Company team, ID 7XK2") {
		t.Fatal(run.Spec.Prompt)
	}
}

// TestPromisedDetailsCloseWays (3.2): withdrawing the promise lets the task go on without the details; an owner message
// to the waiting agent is the details too; an approval cannot be promised.
func TestPromisedDetailsCloseWays(t *testing.T) {
	h := newExitHarness(t)
	co := storage.DefaultCompany
	ctx := context.Background()
	h.create("w", "notes", "manager")
	_, q := h.askOwner("w", "Which store region first?")
	if code, _ := h.answer(q, "", messaging.DetailsWithdrawn); code != 400 {
		t.Fatal("withdrew a promise never made", code)
	}
	if code, _ := h.answer(q, "", messaging.DetailsPromised); code != 200 {
		t.Fatal(code)
	}
	if code, _ := h.answer(q, "", messaging.DetailsWithdrawn); code != 200 {
		t.Fatal(code)
	}
	if m := h.message(q.ID); m.Status != "ANSWERED" || m.Details != messaging.DetailsWithdrawn {
		t.Fatalf("%+v", m)
	}
	if h.why("w").Reason != tasks.ReasonRunnable || count(h.triggers("w"), "message_received") != 1 {
		t.Fatal(h.why("w"), h.triggers("w"))
	}
	next := h.start("w")
	run, _ := h.s.Get(ctx, co, next.id)
	if !strings.Contains(run.Spec.Prompt, "continue without them") {
		t.Fatal(run.Spec.Prompt)
	}
	h.end(next, "succeeded", "")
	h.cancel("w")

	// The details in a chat message to the waiting agent (no task named) wake it and close the question.
	h.create("c", "notes", "manager")
	_, q2 := h.askOwner("c", "Which icon set?")
	if code, _ := h.answer(q2, "", messaging.DetailsPromised); code != 200 {
		t.Fatal(code)
	}
	if _, err := h.s.SendMessage(ctx, co, messaging.Send{ProjectID: "notes", Sender: "owner", Recipient: "manager", MessageType: "notification", Body: "icons: the outline set", RequestID: "chat-details"}); err != nil {
		t.Fatal(err)
	}
	if m := h.message(q2.ID); m.Status != "ANSWERED" || m.Details != messaging.DetailsSent {
		t.Fatalf("%+v", m)
	}
	if h.parkStatus("c", tasks.ParkMessage) != "cleared" || h.ownerView().pending.Total != 0 {
		t.Fatal("still waiting")
	}
}
