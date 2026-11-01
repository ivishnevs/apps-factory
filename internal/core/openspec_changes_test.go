package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// feedOf are the feed items of type typ in project.
func feedOf(t *testing.T, s *storage.Store, project, typ string) []storage.FeedItem {
	t.Helper()
	page, err := s.ProjectFeed(context.Background(), storage.DefaultCompany, project, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []storage.FeedItem
	for _, it := range page.Items {
		if it.Type == typ {
			out = append(out, it)
		}
	}
	return out
}

// doneWith runs task to an accepted done exit whose facts report change in state with unchecked items.
func (h *exitHarness) doneWith(task string, o *storage.OpenSpecFacts) {
	h.t.Helper()
	r := h.start(task)
	var brief struct {
		AckSeq int64 `json:"ack_seq"`
	}
	_ = json.Unmarshal(h.do(r.token, "GET", "/agent/brief", nil, nil).Body.Bytes(), &brief)
	facts := clean("h-"+task, &storage.PRFacts{Number: 1, State: "merged", HeadSHA: "h-" + task})
	facts.OpenSpec = o
	w := h.do(r.token, "POST", "/agent/exit", map[string]any{"kind": "done", "arguments": map[string]any{"summary": "merged"}, "ack_seq": brief.AckSeq, "facts": facts}, nil)
	if w.Code != 200 || h.task(task).Status != "DONE" {
		h.t.Fatal(w.Code, w.Body.String())
	}
	h.end(r, "succeeded", "accepted")
}

func (h *exitHarness) createLinked(id, change string) {
	h.t.Helper()
	if _, err := h.s.CreateTask(context.Background(), storage.DefaultCompany, tasks.Create{ID: id, ProjectID: "code", Title: id, Assignee: "demo", OpenSpecChange: change, Ready: true, RequestID: "create-" + id}); err != nil {
		h.t.Fatal(err)
	}
}

// TestChangeTrackedThroughTasks (openspec-in-git D2, D3): Core accepts any well-formed change name (the node
// checks it on the checkout); the done facts of the last open task of a change tell the manager when it was
// left active, never while another task of the change is open or when it was archived; the daily check
// reminds once more.
func TestChangeTrackedThroughTasks(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	if _, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "code", Title: "bad", Assignee: "demo", OpenSpecChange: "Not A Name", RequestID: "bad"}); !errors.Is(err, storage.ErrInput) {
		t.Fatal("invalid change name accepted", err)
	}
	h.createLinked("a", "add-login")
	h.createLinked("b", "add-login")
	h.createLinked("c", "dark-mode")

	h.doneWith("a", &storage.OpenSpecFacts{Change: "add-login", State: "active", Unchecked: 2})
	if n := len(feedOf(t, h.s, "code", "openspec.change_unfinished")); n != 0 {
		t.Fatal("notice while task b of the change is open", n)
	}
	h.doneWith("b", &storage.OpenSpecFacts{Change: "add-login", State: "active", Unchecked: 0})
	notices := feedOf(t, h.s, "code", "openspec.change_unarchived")
	if len(notices) != 1 || notices[0].EntityID != "add-login" || notices[0].TaskID != "b" || !notices[0].Attention || !strings.Contains(notices[0].Summary, "not archived") {
		t.Fatalf("%+v", notices)
	}
	if n := len(feedOf(t, h.s, "code", "openspec.change_unfinished")); n != 0 {
		t.Fatal("unfinished notice from older facts", n)
	}
	h.doneWith("c", &storage.OpenSpecFacts{Change: "dark-mode", State: "archived"})
	for _, typ := range []string{"openspec.change_unarchived", "openspec.change_unfinished"} {
		for _, it := range feedOf(t, h.s, "code", typ) {
			if it.EntityID == "dark-mode" {
				t.Fatalf("archived change noticed: %+v", it)
			}
		}
	}

	// Facts about another change than the task's are ignored.
	h.createLinked("d", "search")
	h.doneWith("d", &storage.OpenSpecFacts{Change: "other", State: "active", Unchecked: 5})
	if n := len(feedOf(t, h.s, "code", "openspec.change_unfinished")); n != 0 {
		t.Fatal("foreign facts recorded", n)
	}

	// The daily check: nothing within the window, one reminder after it, none again.
	if n, err := h.s.ReportOrphanChanges(ctx, storage.DefaultCompany, "owner"); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	h.fake.Advance(storage.OrphanWindow + time.Hour)
	if n, err := h.s.ReportOrphanChanges(ctx, storage.DefaultCompany, "owner"); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if orphans := feedOf(t, h.s, "code", "openspec.change_orphaned"); len(orphans) != 1 || orphans[0].EntityID != "add-login" {
		t.Fatalf("%+v", orphans)
	}
	if n, err := h.s.ReportOrphanChanges(ctx, storage.DefaultCompany, "owner"); err != nil || n != 0 {
		t.Fatal("reported twice", n, err)
	}
}

// TestChangeMissingEscalates (openspec-in-git D3): a run ended change_missing by the node's preflight costs no
// attempt, backoff or budget and holds its task with a change_missing escalation (retry or cancel).
func TestChangeMissingEscalates(t *testing.T) {
	h := newExitHarness(t)
	h.createLinked("m", "not-merged-yet")
	r := h.start("m")
	h.end(r, "change_missing", "")
	got := h.task("m")
	if got.AttemptsNoProgress != 0 || got.FailStreak != 0 || got.NotBefore != 0 {
		t.Fatalf("%+v", got)
	}
	open, err := h.s.Escalations(context.Background(), storage.DefaultCompany, true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range open {
		if e.TaskID == "m" && e.Reason == "change_missing" && strings.Join(e.Answers, ",") == "retry,cancel" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%+v", open)
	}
}
