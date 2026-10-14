package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestResetFromDefaultBranch: a task that never pushed is reset to origin/main; its unpushed commit and
// uncommitted work are kept on the reset ref, the new worktree is clean and the next run passes the
// start check. Repository URL and branch come from the node's clone and the worktree state.
func TestResetFromDefaultBranch(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt.Path, "a.txt"), "changed\n")
	run(t, wt.Path, "commit", "-qam", "local only")
	local := run(t, wt.Path, "rev-parse", "HEAD")
	write(t, filepath.Join(wt.Path, "untracked.txt"), "wip\n")
	write(t, filepath.Join(wt.Scratch, "tmp"), "x")
	if err = wt.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err = f.w.Seal(ctx, f.req); err != nil {
		t.Fatal(err)
	}

	res, err := f.w.Reset(ctx, Request{Company: "acme", Project: "app", Task: "t1"})
	if err != nil {
		t.Fatal(err)
	}
	main := run(t, f.remote, "rev-parse", "main")
	if res.Branch != "af/t1-add-login" || res.From != "origin/main" || res.Head != main || res.PreviousHead != local || res.Path != wt.Path {
		t.Fatalf("reset = %+v", res)
	}
	if res.Seal.WIPSHA == "" || res.Kept != res.Seal.WIPSHA {
		t.Fatalf("uncommitted work not kept: %+v", res)
	}
	if got := run(t, wt.Repo, "rev-parse", ResetRef("t1")+"^"); got != local {
		t.Fatalf("reset ref parent %s, want %s", got, local)
	}
	if got := run(t, wt.Repo, "show", ResetRef("t1")+":untracked.txt"); got != "wip" {
		t.Fatalf("reset ref untracked.txt %q", got)
	}
	if out := run(t, wt.Repo, "for-each-ref", WIPRef("t1")); out != "" {
		t.Fatalf("WIP ref kept: %s", out)
	}
	if s := run(t, wt.Path, "status", "--porcelain", "--untracked-files=all"); s != "" {
		t.Fatalf("worktree not clean: %q", s)
	}
	if _, err = os.Stat(filepath.Join(wt.Scratch, "tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch kept: %v", err)
	}
	if got := run(t, wt.Path, "symbolic-ref", "--short", "HEAD"); got != "af/t1-add-login" {
		t.Fatalf("branch %s", got)
	}
	again, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal("start check after reset:", err)
	}
	_ = again.Release()
}

// TestResetFromRemoteBranch: a pushed task branch is the start point; an unpushed commit on a clean tree
// is kept on the reset ref.
func TestResetFromRemoteBranch(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt.Path, "pushed.txt"), "p\n")
	run(t, wt.Path, "add", "-A")
	run(t, wt.Path, "commit", "-qm", "pushed")
	run(t, wt.Path, "push", "-q", "origin", wt.Branch)
	pushed := run(t, wt.Path, "rev-parse", "HEAD")
	write(t, filepath.Join(wt.Path, "local.txt"), "l\n")
	run(t, wt.Path, "add", "-A")
	run(t, wt.Path, "commit", "-qm", "local")
	local := run(t, wt.Path, "rev-parse", "HEAD")
	if err = wt.Release(); err != nil {
		t.Fatal(err)
	}
	res, err := f.w.Reset(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	if res.From != "origin/af/t1-add-login" || res.Head != pushed || res.Kept != local || res.Seal.WIPSHA != "" {
		t.Fatalf("reset = %+v", res)
	}
	if _, err = os.Stat(filepath.Join(wt.Path, "local.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local commit still checked out: %v", err)
	}
	// Reset of a task without worktree (e.g. removed by the janitor) recreates it.
	if _, err = f.w.Remove(ctx, f.req, time.Second, nil); err != nil {
		t.Fatal(err)
	}
	if res, err = f.w.Reset(ctx, f.req); err != nil || res.Head != pushed || res.Kept != "" {
		t.Fatalf("reset without worktree = %+v, %v", res, err)
	}
}

// TestResetBusy: a run holding the worktree makes Reset fail with ErrBusy and leaves it untouched.
func TestResetBusy(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	wt, err := f.w.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Release()
	write(t, filepath.Join(wt.Path, "work.txt"), "w\n")
	waitCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	if _, err = f.w.Reset(waitCtx, f.req); !errors.Is(err, ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if _, err = os.Stat(filepath.Join(wt.Path, "work.txt")); err != nil {
		t.Fatal("busy worktree changed:", err)
	}
	if _, err = f.w.Reset(ctx, Request{Company: "acme", Project: "other", Task: "t1"}); err == nil {
		t.Fatal("reset without a clone accepted")
	}
}
