package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"

	"github.com/nats-io/nats.go"
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
	if r, _ := s.Get(ctx, co, first); r.Status != "CANCELLED" || r.Outcome != "unclaimed" {
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

// TestJetStreamPurgedCommandStream (D5, restored stream): the command stream is purged while a run is
// QUEUED; after redeliver_after the reconciler publishes a new command (<run>/2) and the run is claimed and
// completes once.
func TestJetStreamPurgedCommandStream(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	ctx := context.Background()
	c, s, fake := schedulerCore(t)
	c.Bus, c.Token = b, "owner"
	co := storage.DefaultCompany
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "purged", ProjectID: "p", Title: "purged", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	tick := func(d time.Duration) {
		t.Helper()
		fake.Advance(d)
		if err := c.maintain(ctx); err != nil {
			t.Fatal(err)
		}
		if err := c.publishOutbox(ctx); err != nil {
			t.Fatal(err)
		}
	}
	tick(0)
	task, _ := s.Task(ctx, co, "purged")
	run := task.CurrentRunID
	if run == "" {
		t.Fatal("not dispatched")
	}
	if err = b.JS.PurgeStream("FACTORY_COMMANDS"); err != nil {
		t.Fatal(err)
	}
	sub, err := b.Subscribe("factory.commands.agent.run.codex", "purge-test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sub.Fetch(1, nats.MaxWait(time.Second)); !errors.Is(err, nats.ErrTimeout) {
		t.Fatal("the purged command is still there:", err)
	}
	tick(30 * time.Second)
	if _, err = sub.Fetch(1, nats.MaxWait(time.Second)); !errors.Is(err, nats.ErrTimeout) {
		t.Fatal("resent before redeliver_after:", err)
	}
	tick(31 * time.Second)
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatal("no command after redeliver_after:", err)
	}
	var e protocol.Envelope
	if err = json.Unmarshal(msgs[0].Data, &e); err != nil || e.CorrelationID != run || msgs[0].Header.Get(nats.MsgIdHdr) != run+"/2" {
		t.Fatal(e, msgs[0].Header, err)
	}
	_ = msgs[0].AckSync()
	// The worker's claim and end: the task runs once.
	code, body := call(t, c, "POST", "/runs/"+run+"/claim", c.Token, nil)
	var claimed storage.Claimed
	if code != 200 || json.Unmarshal([]byte(body), &claimed) != nil || claimed.RunSpec.ID != run || claimed.RunSpec.Prompt == "" {
		t.Fatal(code, body)
	}
	if code, body = call(t, c, "POST", "/runs/"+run+"/claim", c.Token, nil); code != 409 {
		t.Fatal("claimed twice:", code, body)
	}
	if err = s.Apply(ctx, co, protocol.Wrap("agent.completed", run, "", protocol.Event{RunID: run, Lease: claimed.Lease, Status: "COMPLETED", Result: "done"})); err != nil {
		t.Fatal(err)
	}
	if task, _ = s.Task(ctx, co, "purged"); task.Status != "IN_REVIEW" || task.CurrentRunID != run {
		t.Fatalf("%+v", task)
	}
}
