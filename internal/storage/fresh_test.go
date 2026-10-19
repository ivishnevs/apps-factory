package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestFreshFromDefaultTasks (task-worktrees 2.2): fresh_from_default reaches the run spec; its task runs hold
// their own writer slot on a worktree node like worktree runs (two tasks of one project in parallel), and its
// worktree never pins the task (each run starts again from the default branch).
func TestFreshFromDefaultTasks(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "rel", Name: "rel", Workspace: "/tmp/fresh-rel", RepoURL: "https://example.com/rel.git"}); err != nil {
		t.Fatal(err)
	}
	fresh := []byte(`{"spec":{"workspace":{"mode":"fresh_from_default"}}}`)
	dispatch := func(task, run string) protocol.RunSpec {
		t.Helper()
		v, err := s.Task(ctx, co, task)
		if errors.Is(err, ErrNotFound) {
			v, err = s.CreateTask(ctx, co, tasks.Create{ID: task, ProjectID: "rel", Title: task, Assignee: "rel-" + task, Ready: true, RequestID: "c-" + task})
		}
		if err != nil {
			t.Fatal(err)
		}
		spec, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: run, Project: "rel", Agent: "rel-" + task, Provider: protocol.RuntimeCodex, ConfigSnapshot: fresh})
		if err != nil {
			t.Fatalf("dispatch %s: %v", task, err)
		}
		return spec
	}
	a, b := dispatch("a", "ra1"), dispatch("b", "rb1")
	if a.WorkspaceMode != "fresh_from_default" || b.WorkspaceMode != "fresh_from_default" {
		t.Fatal(a.WorkspaceMode, b.WorkspaceMode)
	}
	register(t, s, "wt", 1, func(r *NodeRegistration) { r.Worktrees = true; r.Slots = 4 })
	register(t, s, "n2", 1, func(r *NodeRegistration) { r.Worktrees = true; r.Slots = 4 })
	ca, err := claimV2(s, a.ID, "wt", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = claimV2(s, b.ID, "wt", 1, "1-1-2"); err != nil {
		t.Fatal("second fresh_from_default task of the project did not run in parallel:", err)
	}
	// The node reports local commits on the fresh worktree: no pin, the next run may go to any node.
	w, err := s.ReportWorkspace(ctx, co, a.ID, ca.Lease, tasks.Workspace{Mode: "worktree", NodeID: "wt", Unpushed: true})
	if err != nil || w.Unpushed {
		t.Fatalf("%+v %v", w, err)
	}
	if err = end(t, s, a.ID, ca.Lease, protocol.Event{Status: "FAILED", Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	next := dispatch("a", "ra2")
	if _, err = claimV2(s, next.ID, "n2", 1, "1-0-3"); err != nil {
		t.Fatal("a fresh_from_default task was pinned:", err)
	}
}
