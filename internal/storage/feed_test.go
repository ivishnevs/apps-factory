package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
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

func TestFeedBackfillKeepsSequencesAndContent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "Project", Workspace: "/tmp/feed-backfill"}); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "a", ProjectID: "p", Title: "Write the onboarding flow", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	title := "Write the onboarding flow v2"
	if _, err = s.UpdateTask(ctx, DefaultCompany, task.ID, tasks.Update{Version: task.Version, Title: &title}); err != nil {
		t.Fatal(err)
	}
	want, err := s.ProjectFeed(ctx, DefaultCompany, "p", 0, 100)
	if err != nil || len(want.Items) != 3 {
		t.Fatal(want, err)
	}
	rollbackTo(t, s.DB, 8)
	s.DB.Close()
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	got, err := s.ProjectFeed(ctx, DefaultCompany, "p", 0, 100)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("backfill differs:\n%+v\n%+v\n%v", got.Items, want.Items, err)
	}
	if _, err = s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "b", ProjectID: "p", Title: "next"}); err != nil {
		t.Fatal(err)
	}
	next, _ := s.ProjectFeed(ctx, DefaultCompany, "p", want.NextAfter, 100)
	if len(next.Items) != 1 || next.Items[0].EntityID != "b" {
		t.Fatal("cursor from before the upgrade lost or repeated events", next.Items)
	}
}
