package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestDispatchRefusesAParkedTask (run-storm-guard 1): Dispatch asks the claim's question before it queues, so a
// task parked on an escalation gets no run, whoever calls it (prod 2026-11-12: a dispatcher that skipped the park
// queued runs the claim ended stale at once, tens of thousands in a night).
func TestDispatchRefusesAParkedTask(t *testing.T) {
	ctx := context.Background()
	s := engineStore(t)
	parked := readyEngineTask(t, s, createEngineTask(t, s, "parked"))
	tx, err := s.begin(ctx, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	if err = addPark(ctx, tx, parked, tasks.ParkEscalation, "q1", "retries_exhausted: 3 attempts in a row without progress", true); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	parked, _ = s.Task(ctx, DefaultCompany, "parked")
	for i := 0; i < 3; i++ {
		_, err = s.Dispatch(ctx, DefaultCompany, parked.ID, parked.Version, protocol.RunSpec{ID: protocol.ID(), Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex})
		if !errors.Is(err, ErrConflict) {
			t.Fatalf("dispatch of a parked task: %v", err)
		}
	}
	var runs int
	if err = s.DB.QueryRow("SELECT count(*) FROM runs WHERE task_id='parked'").Scan(&runs); err != nil || runs != 0 {
		t.Fatal("runs queued for a parked task:", runs, err)
	}
}

// TestStaleRunsThrottleDispatch (run-storm-guard 2): whatever makes the claim end a task's runs stale, after
// StaleRunCap of them within StaleRunWindow the task gets no new run until the window passes, and the feed says so
// once.
func TestStaleRunsThrottleDispatch(t *testing.T) {
	ctx := context.Background()
	s := engineStore(t)
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	v := readyEngineTask(t, s, createEngineTask(t, s, "flip"))
	agents := []string{"other", "demo"}
	for i := 0; i < StaleRunCap; i++ {
		dispatchEngineTask(t, s, v, "flip-"+string(rune('a'+i)))
		v, _ = s.Task(ctx, DefaultCompany, "flip")
		// The task moves after dispatch, so the claim ends the queued run stale (a stand-in for any disagreement
		// between the dispatcher and the claim).
		next := agents[i%2]
		var err error
		if v, err = s.UpdateTask(ctx, DefaultCompany, "flip", tasks.Update{Version: v.Version, Assignee: &next}); err != nil {
			t.Fatal(err)
		}
		fake.Advance(time.Minute)
	}
	dispatch := func() error {
		v, _ = s.Task(ctx, DefaultCompany, "flip")
		_, err := s.Dispatch(ctx, DefaultCompany, v.ID, v.Version, protocol.RunSpec{ID: protocol.ID(), Project: "p", Agent: v.Assignee, Provider: protocol.RuntimeCodex})
		return err
	}
	for i := 0; i < 3; i++ {
		if err := dispatch(); !errors.Is(err, ErrConflict) {
			t.Fatalf("dispatch %d after %d stale runs: %v", i, StaleRunCap, err)
		}
	}
	page, err := s.Feed(ctx, DefaultCompany, FeedFilter{Task: "flip"}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range page.Items {
		if f.Type == "task.dispatch_throttled" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d task.dispatch_throttled events, want 1", n)
	}
	fake.Advance(StaleRunWindow)
	if err = dispatch(); err != nil {
		t.Fatal("dispatch after the window:", err)
	}
}
