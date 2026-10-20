package storage

import (
	"context"
	"encoding/json"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func TestFeedIsWrittenWithItsCause(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task := createEngineTask(t, s, "a")
	task = readyEngineTask(t, s, task)
	run := dispatchEngineTask(t, s, task, "run-a")
	page, err := s.ProjectFeed(ctx, DefaultCompany, "p", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	last := page.Items[len(page.Items)-1]
	if last.Type != "task.started" || last.TaskID != "a" || last.RunID != run.ID || last.Agent != "demo" || last.Status != "IN_PROGRESS" || last.Summary != "a" {
		t.Fatalf("%+v", last)
	}
	var subject string
	var body []byte
	if err = s.DB.QueryRow("SELECT subject,body FROM outbox WHERE id=?", "feed-"+last.EventID).Scan(&subject, &body); err != nil {
		t.Fatal("feed notification not in outbox", err)
	}
	var e protocol.Envelope
	var item FeedItem
	if json.Unmarshal(body, &e) != nil || json.Unmarshal(e.Payload, &item) != nil || subject != "factory.events.feed.p.task.started" || item.Sequence != last.Sequence {
		t.Fatal(subject, string(body))
	}
	// A mutation that rolls back leaves no feed row.
	before := len(page.Items)
	if _, err = s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "bad", ProjectID: "p", Title: "bad", Dependencies: []string{"missing"}}); err == nil {
		t.Fatal("dependency on missing task accepted")
	}
	if page, _ = s.ProjectFeed(ctx, DefaultCompany, "p", 0, 100); len(page.Items) != before {
		t.Fatal("rolled back mutation left feed rows")
	}
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if page, _ = s.ProjectFeed(ctx, "acme", "p", 0, 100); len(page.Items) != 0 {
		t.Fatal("feed crosses companies", page.Items)
	}
}

// TestRolledBackCauseLeavesNoFeed (trigger-log-scheduling D10): a cause transaction that wrote its trigger,
// its run command and their feed rows, then rolled back, leaves no feed row and no outbox message.
func TestRolledBackCauseLeavesNoFeed(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	count := func() (feeds, outbox int) {
		t.Helper()
		if err := s.DB.QueryRow("SELECT (SELECT count(*) FROM feed_events),(SELECT count(*) FROM outbox)").Scan(&feeds, &outbox); err != nil {
			t.Fatal(err)
		}
		return
	}
	feeds, outbox := count()
	tx, err := s.begin(ctx, DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	if err = addTrigger(ctx, tx, &task, "ready", true, "owner", "", "run now"); err != nil {
		t.Fatal(err)
	}
	spec := protocol.RunSpec{ID: "rolled-back", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex, TaskID: "a", Workspace: "/tmp/task-engine-p"}
	if err = insertRun(ctx, tx, &spec); err != nil {
		t.Fatal(err)
	}
	task.Status, task.CurrentRunID = "IN_PROGRESS", spec.ID
	if err = saveTask(ctx, tx, &task, "task.started"); err != nil {
		t.Fatal(err)
	}
	var inTx int
	if err = tx.QueryRow("SELECT count(*) FROM feed_events").Scan(&inTx); err != nil || inTx <= feeds {
		t.Fatal("the cause wrote no feed row", inTx, feeds, err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if f, o := count(); f != feeds || o != outbox {
		t.Fatalf("rolled back dispatch left %d feed rows and %d outbox messages", f-feeds, o-outbox)
	}
	if r, err := s.Get(ctx, DefaultCompany, spec.ID); err == nil {
		t.Fatal("rolled back run exists", r)
	}
}
