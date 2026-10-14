package janitor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"appfactory/internal/node/sessions"
	"appfactory/internal/workspace"
)

// TestResetWorkspace: reset-workspace recreates the task's worktree from the remote and forgets only that
// task's sessions; a busy worktree fails the reset and keeps the records.
func TestResetWorkspace(t *testing.T) {
	w, base := trees(t)
	ctx := context.Background()
	r := base
	r.Task = "t1"
	wt, err := w.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt.Path, "wip.txt"), "wip\n")
	store := sessions.Open(t.TempDir())
	for _, rec := range []sessions.Record{{SessionID: "s1", Task: "t1", Agent: "dev", Run: "r1", Started: true}, {SessionID: "s2", Task: "t1", Agent: "rev", Run: "r2"}, {SessionID: "s3", Task: "t2", Agent: "dev", Run: "r3"}} {
		if err = store.Put(rec); err != nil {
			t.Fatal(err)
		}
	}
	j := &Janitor{Worktrees: w, Company: base.Company, Sessions: store}

	busy, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	if _, err = j.ResetWorkspace(busy, "t1", ""); !errors.Is(err, workspace.ErrBusy) {
		t.Fatalf("busy reset: %v", err)
	}
	if _, ok, _ := store.Get("s1"); !ok {
		t.Fatal("records forgotten by a failed reset")
	}
	if err = wt.Release(); err != nil {
		t.Fatal(err)
	}

	res, err := j.ResetWorkspace(ctx, "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(res.ForgottenSessions)
	if len(res.Worktrees) != 1 || res.Worktrees[0].Project != "app" || res.Worktrees[0].From != "origin/main" || !slices.Equal(res.ForgottenSessions, []string{"s1", "s2"}) {
		t.Fatalf("reset = %+v", res)
	}
	if _, err = os.Stat(filepath.Join(wt.Path, "wip.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree not recreated: %v", err)
	}
	for id, want := range map[string]bool{"s1": false, "s2": false, "s3": true} {
		if _, ok, _ := store.Get(id); ok != want {
			t.Fatalf("record %s kept = %v", id, ok)
		}
	}
	// No worktree of the task on this node: only its records go.
	if res, err = j.ResetWorkspace(ctx, "t2", ""); err != nil || len(res.Worktrees) != 0 || !slices.Equal(res.ForgottenSessions, []string{"s3"}) {
		t.Fatalf("reset without worktree = %+v, %v", res, err)
	}
}
