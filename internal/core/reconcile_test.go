package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/testnode"
)

// TestUnclaimedRunIsDispatchedAgain (trigger-log-scheduling D5): a task run nobody claims within the queue
// TTL ends unclaimed without an attempt or backoff, and the task gets a new run on the same tick.
func TestUnclaimedRunIsDispatchedAgain(t *testing.T) {
	ctx := context.Background()
	c, s, fake := schedulerCore(t)
	co := storage.DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "orphan", ProjectID: "p", Title: "orphan", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	tick := func(d time.Duration) tasks.Task {
		t.Helper()
		fake.Advance(d)
		if err := c.maintain(ctx); err != nil {
			t.Fatal(err)
		}
		task, _ := s.Task(ctx, co, "orphan")
		return task
	}
	first := tick(0).CurrentRunID
	if got := tick(storage.QueueTTL - time.Second); got.CurrentRunID != first {
		t.Fatal("expired before the queue TTL", got)
	}
	got := tick(time.Second)
	if got.CurrentRunID == first || got.Status != "IN_PROGRESS" || got.AttemptsNoProgress != 0 || got.FailStreak != 0 || got.NotBefore != 0 {
		t.Fatalf("not dispatched again, or counted as an attempt: %+v", got)
	}
	if r, _ := s.Get(ctx, co, first); r.Status != "ENDED" || r.Outcome != "unclaimed" {
		t.Fatalf("%+v", r)
	}
	if r, _ := s.Get(ctx, co, got.CurrentRunID); r.Status != "QUEUED" {
		t.Fatalf("%+v", r)
	}
	feed, _ := s.Feed(ctx, co, storage.FeedFilter{Task: "orphan"}, 0, 100)
	found := false
	for _, f := range feed.Items {
		found = found || f.Type == "run.unclaimed" && f.RunID == first
	}
	if !found {
		t.Fatalf("no run.unclaimed event: %+v", feed.Items)
	}
}

// TestQueuedRunOfferedWithoutCommand (D5, http-node-transport D7): a dispatched task's run is a QUEUED row and
// nothing else; a node's poll offers it, one claim takes it, a second is refused, and the task runs once.
func TestQueuedRunOfferedWithoutCommand(t *testing.T) {
	ctx := context.Background()
	c, s, fake := schedulerCore(t)
	c.Token = "owner"
	co := storage.DefaultCompany
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "purged", ProjectID: "p", Title: "purged", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(0)
	if err := c.maintain(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ := s.Task(ctx, co, "purged")
	run := task.CurrentRunID
	if run == "" {
		t.Fatal("not dispatched")
	}
	n := testnode.Enroll(t, s, co)
	if _, err := s.RegisterNode(ctx, co, registration(n.ID, 1)); err != nil {
		t.Fatal(err)
	}
	offers, err := s.Offers(ctx, co, n.ID, 1, map[string]int{"codex": 1}, nil)
	if err != nil || len(offers) != 1 || offers[0].RunID != run {
		t.Fatalf("%+v %v", offers, err)
	}
	// The worker's claim and end: the task runs once.
	claim := storage.ClaimRequest{NodeID: n.ID, Epoch: 1, ClaimID: "1-0-1"}
	code, body := signedCall(t, c, n, "POST", "/runs/"+run+"/claim", claim)
	var claimed storage.Claimed
	if code != 200 || json.Unmarshal([]byte(body), &claimed) != nil || claimed.RunSpec.ID != run || claimed.RunSpec.Prompt == "" {
		t.Fatal(code, body)
	}
	claim.ClaimID = "1-0-2"
	if code, body = signedCall(t, c, n, "POST", "/runs/"+run+"/claim", claim); code != 409 {
		t.Fatal("claimed twice:", code, body)
	}
	if err = s.Apply(ctx, co, protocol.Wrap("agent.completed", run, "", protocol.Event{RunID: run, Lease: claimed.Lease, Status: "ENDED", Outcome: "succeeded", Result: "done"})); err != nil {
		t.Fatal(err)
	}
	if task, _ = s.Task(ctx, co, "purged"); task.Status != "IN_REVIEW" || task.CurrentRunID != run {
		t.Fatalf("%+v", task)
	}
}
