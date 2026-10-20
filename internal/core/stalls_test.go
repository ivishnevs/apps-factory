package core

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

func TestStallEscalationsAndIdleAlerts(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-stalls"}); err != nil {
		t.Fatal(err)
	}
	create := func(in tasks.Create) tasks.Task {
		t.Helper()
		in.ProjectID, in.Title = "p", in.ID
		v, err := s.CreateTask(ctx, co, in)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	design := create(tasks.Create{ID: "design"})               // BACKLOG, no assignee
	spec := create(tasks.Create{ID: "spec", Assignee: "demo"}) // will be cancelled
	create(tasks.Create{ID: "build", Assignee: "demo", Ready: true, Dependencies: []string{"design", "spec"}})
	if _, err = s.TaskAction(ctx, co, "spec", tasks.Action{Version: spec.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	sweep := func(after time.Duration) []storage.FeedItem {
		t.Helper()
		fake.Advance(after)
		if err := c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
		page, err := s.Feed(ctx, co, storage.FeedFilter{Task: "build"}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		alerts := []storage.FeedItem{}
		for _, item := range page.Items {
			if strings.HasPrefix(item.Type, "task.escalation") {
				alerts = append(alerts, item)
			}
		}
		return alerts
	}
	alerts := sweep(time.Minute)
	// Routed to the manager, not the owner: no attention (waits-events-cost-cli D2).
	if len(alerts) != 2 || alerts[0].Attention || alerts[1].Attention {
		t.Fatalf("%+v", alerts)
	}
	open, err := s.Escalations(ctx, co, true)
	if err != nil || len(open) != 2 {
		t.Fatal(open, err)
	}
	byRef := map[string]storage.Escalation{open[0].RefID: open[0], open[1].RefID: open[1]}
	// Routed to the assignee's manager (reports_to).
	if !strings.Contains(byRef["spec"].Summary, "cancelled") || !strings.Contains(byRef["design"].Summary, "BACKLOG") || byRef["spec"].Target != "manager" || byRef["design"].Target != "manager" {
		t.Fatalf("%+v", byRef)
	}
	if snap, _ := s.TaskSnapshot(ctx, co, "build"); c.evaluate(snap).Reason != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatalf("%+v", c.evaluate(snap))
	}
	if again := sweep(time.Minute); len(again) != 2 {
		t.Fatal("alert repeated within its episode", again)
	}
	if again := sweep(10 * time.Second); len(again) != 2 {
		t.Fatal("sweep ran more than once a minute", again)
	}
	// Releasing the BACKLOG dependency clears its alert; the cancelled one stays.
	design, _ = s.Task(ctx, co, "design")
	assignee := "demo"
	if design, err = s.UpdateTask(ctx, co, "design", tasks.Update{Version: design.Version, Assignee: &assignee}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAction(ctx, co, "design", tasks.Action{Version: design.Version, Action: "ready"}); err != nil {
		t.Fatal(err)
	}
	alerts = sweep(time.Minute)
	if len(alerts) != 3 || alerts[2].Type != "task.escalation_cleared.stalled" || alerts[2].Ref != byRef["design"].MessageID || alerts[2].Attention {
		t.Fatalf("%+v", alerts)
	}
	if m, _ := s.Message(ctx, co, byRef["design"].MessageID); m.Status != "WITHDRAWN" {
		t.Fatal("cleared escalation left its question open", m.Status)
	}
	// The owner answers the cancelled dependency's escalation: build stops waiting on spec.
	q, _ := s.Message(ctx, co, byRef["spec"].MessageID)
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "go on", Answers: map[string]any{"answer": "nonsense"}, RequestID: "bad"}); err == nil {
		t.Fatal("answer outside the options accepted")
	}
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "go on without it", Answers: map[string]any{"answer": "unblock"}, RequestID: "ok"}); err != nil {
		t.Fatal(err)
	}
	build, _ := s.Task(ctx, co, "build")
	if len(build.Dependencies) != 1 || build.Dependencies[0] != "design" {
		t.Fatal(build.Dependencies)
	}
	if left, _ := s.Escalations(ctx, co, true); len(left) != 0 {
		t.Fatalf("%+v", left)
	}
	all, _ := s.Escalations(ctx, co, false)
	for _, e := range all {
		if e.RefID == "spec" && (e.Status != "answered" || e.Answer != "unblock" || e.Actor != "owner") || e.RefID == "design" && e.Status != "cleared" {
			t.Fatalf("%+v", all)
		}
	}
	if again := sweep(time.Minute); len(again) != 4 { // + task.escalation_answered.stalled; nothing reopened
		t.Fatalf("%+v", again)
	}

	// Idle: a task waiting for review for over a day raises one idle alert.
	review := create(tasks.Create{ID: "review", Assignee: "demo", Ready: true})
	if _, err = s.Dispatch(ctx, co, "review", review.Version, protocol.RunSpec{ID: "r1", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, co, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(ctx, co, protocol.Wrap("agent.completed", "r1", "", protocol.Event{RunID: "r1", Lease: lease, Status: "ENDED", Outcome: "succeeded", Result: "done"})); err != nil {
		t.Fatal(err)
	}
	idle := func(after time.Duration) int {
		t.Helper()
		fake.Advance(after)
		if err := c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
		page, _ := s.Feed(ctx, co, storage.FeedFilter{Task: "review", Attention: true}, 0, 100)
		return len(page.Items)
	}
	if n := idle(23 * time.Hour); n != 0 {
		t.Fatal("idle alert before a day", n)
	}
	if n := idle(2 * time.Hour); n != 1 {
		t.Fatal("no idle alert after a day", n)
	}
	if n := idle(time.Hour); n != 1 {
		t.Fatal("idle alert repeated", n)
	}
}

// TestStallChildrenPark: a task parked on its children (factory_exit_block on_children) stalls on an open child
// in BACKLOG or without an assignee: the same self-clearing stalled escalation to the parent's manager; releasing
// the child clears it.
func TestStallChildrenPark(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create("parent", "notes", "demo")
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "kid", ProjectID: "notes", Title: "kid", Assignee: "calorie-ios", ParentTaskID: "parent", RequestID: "kid"}); err != nil {
		t.Fatal(err)
	}
	r := h.start("parent")
	h.callOK(r, "factory_exit_block", map[string]any{"on_children": true, "reason": "after the kid"})
	h.end(r, "succeeded", "accepted")
	open := func() []storage.Escalation {
		t.Helper()
		h.fake.Advance(time.Minute)
		if err := h.c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
		all, err := h.s.Escalations(ctx, co, true)
		if err != nil {
			t.Fatal(err)
		}
		var out []storage.Escalation
		for _, e := range all {
			if e.Reason == "stalled" && e.TaskID == "parent" {
				out = append(out, e)
			}
		}
		return out
	}
	got := open()
	if len(got) != 1 || got[0].RefID != "kid" || got[0].Summary != "waits on its child kid, which is in BACKLOG" || got[0].Target != "manager" {
		t.Fatalf("%+v", got)
	}
	if again := open(); len(again) != 1 || again[0].ID != got[0].ID {
		t.Fatalf("not one escalation per episode: %+v", again)
	}
	if _, err := h.s.TaskAction(ctx, co, "kid", tasks.Action{Version: h.task("kid").Version, Action: "ready"}); err != nil {
		t.Fatal(err)
	}
	if left := open(); len(left) != 0 {
		t.Fatalf("released child left the escalation open: %+v", left)
	}
	// An unassigned child: the answer unblock releases the parent from its children park.
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "orphan", ProjectID: "notes", Title: "orphan", ParentTaskID: "parent", RequestID: "orphan"}); err != nil {
		t.Fatal(err)
	}
	got = open()
	if len(got) != 1 || got[0].RefID != "orphan" || !strings.Contains(got[0].Summary, "child orphan, which") {
		t.Fatalf("%+v", got)
	}
	q, _ := h.s.Message(ctx, co, got[0].MessageID)
	if _, err := h.s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "go on", Answers: map[string]any{"answer": "unblock"}, RequestID: "unblock"}); err != nil {
		t.Fatal(err)
	}
	if p := h.task("parent"); p.Status != "READY" || h.parkStatus("parent", tasks.ParkChildren) != "overridden" {
		t.Fatalf("%+v %s", p, h.parkStatus("parent", tasks.ParkChildren))
	}
}
