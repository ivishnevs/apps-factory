package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/policies"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestBackoffResetsOnProgress (reliable-recovery 1.3): failed, failed, succeeded with progress, failed back
// off 1 minute, 5 minutes, nothing (counters reset), then 1 minute again.
func TestBackoffResetsOnProgress(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("t", "code", "demo")
	check := func(step string, attempts, streak int, wait time.Duration) {
		t.Helper()
		got := h.task("t")
		want := int64(0)
		if wait > 0 {
			want = h.fake.Now().Add(wait).Unix()
		}
		if got.Status != "READY" || got.AttemptsNoProgress != attempts || got.FailStreak != streak || got.NotBefore != want {
			t.Fatalf("%s: %+v, want attempts %d streak %d not_before %d", step, got, attempts, streak, want)
		}
	}
	fail := func() {
		t.Helper()
		r := h.start("t")
		if err := h.s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.ended", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Status: "ENDED", Outcome: "failed"})); err != nil {
			t.Fatal(err)
		}
	}
	fail()
	check("first failure", 1, 1, time.Minute)
	h.fake.Advance(time.Minute)
	fail()
	check("second failure", 2, 2, 5*time.Minute)
	h.fake.Advance(5 * time.Minute)
	// Progress: a run that hands the task to itself with a commit that moved the branch.
	r := h.start("t")
	if code, ref := h.exit(r, "handoff", map[string]any{"to": "demo", "note": "continue from the first commit"}, clean("h1", nil)); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	check("success with progress", 0, 0, 0)
	h.fake.Advance(time.Second)
	fail()
	check("failure after progress", 1, 1, time.Minute)
}

// TestInspectParksOnlyItsTask (reliable-recovery 2.1, 2.2): the run of an agent whose policy says recovery:
// inspect carries the rule in its config snapshot; when it is lost, only its task is parked for inspection,
// and another task of the same project is dispatched meanwhile.
func TestInspectParksOnlyItsTask(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	publisher := h.c.Agents["demo"]
	publisher.Metadata.Name = "publisher"
	publisher.Rules = append(append([]policies.Rule{}, publisher.Rules...), policies.Rule{Name: "inspect-publishing", Type: "recovery", Mode: "inspect", Instruction: "Inspect, never retry."})
	h.c.Agents["publisher"] = publisher
	h.create("publish", "notes", "publisher")
	r := h.start("publish")
	run, _ := h.s.Get(ctx, co, r.id)
	if !strings.Contains(string(run.Spec.ConfigSnapshot), `"mode":"inspect"`) {
		t.Fatal("the recovery rule is not in the run's snapshot")
	}
	// A later policy change does not change how this run recovers: the snapshot decides.
	delete(h.c.Agents, "publisher")
	h.c.Agents["publisher"] = h.c.Agents["demo"]
	h.fake.Advance(storage.LeaseTTL + time.Second)
	if err := h.s.Expire(ctx, co); err != nil {
		t.Fatal(err)
	}
	open, _ := h.s.Escalations(ctx, co, true)
	if len(open) != 1 || open[0].Reason != "inspect" || open[0].TaskID != "publish" || open[0].RefID != r.id {
		t.Fatalf("%+v", open)
	}
	h.create("other", "notes", "reviewer")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if got := h.task("other"); got.Status != "IN_PROGRESS" || got.CurrentRunID == "" {
		t.Fatalf("another task of the project waits behind the inspection: %+v", got)
	}
	got := h.task("publish")
	if got.CurrentRunID != r.id {
		t.Fatalf("the inspected task was retried: %+v", got)
	}
	snap, _ := h.s.TaskSnapshot(ctx, co, "publish")
	if v := h.c.evaluate(snap); v.Reason != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatalf("%+v", v)
	}
}

// TestNoProgressAlert (reliable-recovery 1.6, D8): runs without progress for 8 hours raise one attention
// alert; more runs do not repeat it; progress clears it and re-arms it.
func TestNoProgressAlert(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	h.create("t", "notes", "demo")
	fail := func() {
		t.Helper()
		r := h.start("t")
		if err := h.s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.ended", r.id, "", protocol.Event{RunID: r.id, Lease: r.lease, Status: "ENDED", Outcome: "failed"})); err != nil {
			t.Fatal(err)
		}
	}
	sweep := func(after time.Duration) []string {
		t.Helper()
		h.fake.Advance(after)
		if err := h.c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := h.s.Feed(ctx, storage.DefaultCompany, storage.FeedFilter{Task: "t"}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var alerts []string
		for _, item := range page.Items {
			if strings.HasSuffix(item.Type, ".no_progress") {
				alerts = append(alerts, item.Type)
				if item.Type == "task.alert.no_progress" && !item.Attention {
					t.Fatal("no attention", item)
				}
			}
		}
		return alerts
	}
	fail()
	if a := sweep(7 * time.Hour); len(a) != 0 {
		t.Fatal("alerted before 8 hours", a)
	}
	fail()
	if a := sweep(time.Hour); len(a) != 1 || a[0] != "task.alert.no_progress" {
		t.Fatal(a)
	}
	if a := sweep(time.Hour); len(a) != 1 {
		t.Fatal("alerted twice in one episode", a)
	}
	// A run with progress ends the episode: the alert clears.
	r := h.start("t")
	if code, ref := h.exit(r, "submit", map[string]any{"summary": "done"}, nil); code != 200 {
		t.Fatal(code, ref)
	}
	h.end(r, "succeeded", "accepted")
	if a := sweep(time.Minute); len(a) != 2 || a[1] != "task.alert_cleared.no_progress" {
		t.Fatal(a)
	}
}
