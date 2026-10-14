package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSealLeftovers: a worktree whose run pidfile outlived the node process gets its group killed and its
// work sealed onto the WIP ref; worktrees without a pidfile are left alone.
func TestSealLeftovers(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	left, err := f.w.Ensure(ctx, f.task("t1"))
	if err != nil {
		t.Fatal(err)
	}
	idle, err := f.w.Ensure(ctx, f.task("t2"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := startGroup(t, "sleep 60 & wait")
	if err = left.Track(pid); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(left.Path, "work.txt"), "unsaved\n")
	write(t, filepath.Join(idle.Path, "other.txt"), "x\n")
	if err = f.w.SealLeftovers(ctx); err != nil {
		t.Fatal(err)
	}
	if !groupGone(pid) {
		t.Fatal("leftover group alive")
	}
	if _, err = os.Stat(pidPath(left.Path)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pidfile left", err)
	}
	if got := run(t, left.Path, "show", WIPRef("t1")+":work.txt"); got != "unsaved" {
		t.Fatalf("WIP ref content %q", got)
	}
	if out := run(t, idle.Repo, "for-each-ref", WIPRef("t2")); out != "" {
		t.Fatalf("idle worktree sealed: %s", out)
	}
}

// TestRemove: cleanup of a closed task seals and removes its worktree and scratch but keeps the branch
// and the WIP ref; a worktree held by a run is kept.
func TestRemoveTask(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Ensure(ctx, f.task("t1"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt.Path, "work.txt"), "unsaved\n")
	held, err := f.w.Acquire(ctx, f.task("t2"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	if err = f.w.RemoveTask(ctx, "t2"); !errors.Is(err, ErrBusy) {
		t.Fatalf("held worktree: %v", err)
	}
	if err = f.w.RemoveTask(ctx, "t1"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{wt.Path, wt.Scratch} {
		if _, err = os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s left: %v", p, err)
		}
	}
	if _, err = os.Stat(held.Path); err != nil {
		t.Fatal(err)
	}
	if got := run(t, wt.Repo, "show", WIPRef("t1")+":work.txt"); got != "unsaved" {
		t.Fatalf("WIP ref content %q", got)
	}
	run(t, wt.Repo, "rev-parse", "--verify", "refs/heads/af/t1")
	if out := run(t, wt.Repo, "worktree", "list", "--porcelain"); strings.Contains(out, wt.Path) {
		t.Fatalf("worktree still registered: %s", out)
	}
	if err = f.w.RemoveTask(ctx, "t1"); err != nil { // repeated cleanup is a no-op
		t.Fatal(err)
	}
	if err = f.w.RemoveTask(ctx, "../x"); err == nil {
		t.Fatal("unsafe task id accepted")
	}
}
