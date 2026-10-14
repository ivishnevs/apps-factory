package storage

import (
	"context"
	"errors"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestParallelWorktreeTasks: two worktree tasks of one project dispatch together and run in parallel on a
// node with worktrees; on a node without worktrees (or a v1 worker) they share the project workspace and
// run one at a time (retry project_busy); a shared-mode task always holds the project workspace.
func TestParallelWorktreeTasks(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/parallel-app", RepoURL: "https://example.com/app.git"}); err != nil {
		t.Fatal(err)
	}
	worktree := []byte(`{"spec":{"workspace":{"mode":"worktree"}}}`)
	dispatch := func(task, agent string, snapshot []byte) protocol.RunSpec {
		t.Helper()
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: task, ProjectID: "app", Title: task, Assignee: agent, Ready: true, RequestID: "c-" + task})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r-" + task, Project: "app", Agent: agent, Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
		if err != nil {
			t.Fatalf("dispatch %s: %v", task, err)
		}
		return spec
	}
	a := dispatch("a", "dev-a", worktree)
	b := dispatch("b", "dev-b", worktree)
	if a.WorkspaceMode != "worktree" || b.WorkspaceMode != "worktree" {
		t.Fatal(a.WorkspaceMode, b.WorkspaceMode)
	}
	register(t, s, "wt", 1, func(r *NodeRegistration) { r.Worktrees = true; r.Slots = 4 })
	if _, err := claimV2(s, a.ID, "wt", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := claimV2(s, b.ID, "wt", 1, "1-1-2"); err != nil {
		t.Fatal("second task of the project did not run in parallel:", err)
	}

	// A node without worktrees runs both in the project workspace: one at a time.
	c := dispatch("c", "dev-c", worktree)
	d := dispatch("d", "dev-d", worktree)
	register(t, s, "plain", 1, func(r *NodeRegistration) { r.Slots = 4 })
	if _, err := claimV2(s, c.ID, "plain", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, d.ID, "plain", 1, "1-1-2"); return err }(), true, "project_busy")
	// A v1 worker is treated the same way.
	if _, err := s.Claim(ctx, co, d.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("v1 claim shared the project workspace", err)
	}
	// The worktree node still takes it.
	if _, err := claimV2(s, d.ID, "wt", 1, "1-2-3"); err != nil {
		t.Fatal(err)
	}
	// Shared mode always holds the project workspace: it waits while run c uses it.
	v, err := s.CreateTask(ctx, co, tasks.Create{ID: "e", ProjectID: "app", Title: "e", Assignee: "dev-e", Ready: true, RequestID: "c-e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r-e", Project: "app", Agent: "dev-e", Provider: protocol.RuntimeCodex}); !errors.Is(err, ErrConflict) {
		t.Fatal("shared task dispatched while the project workspace is in use", err)
	}
}
