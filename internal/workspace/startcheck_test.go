package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// pushRemote commits file=content on branch of the bare remote (created from main when missing) and
// returns the new remote head.
func pushRemote(t *testing.T, f fixture, branch, file, content string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "other")
	run(t, filepath.Dir(dir), "clone", "-q", f.remote, dir)
	if run(t, dir, "ls-remote", "--heads", "origin", branch) != "" {
		run(t, dir, "checkout", "-q", branch)
	} else {
		run(t, dir, "checkout", "-q", "-b", branch)
	}
	write(t, filepath.Join(dir, file), content)
	run(t, dir, "add", "-A")
	run(t, dir, "commit", "-qm", "remote "+file)
	run(t, dir, "push", "-q", "origin", branch)
	return run(t, dir, "rev-parse", "HEAD")
}

// runOnce acquires, lets work change the worktree, seals and releases, as one run of the executor.
func runOnce(t *testing.T, f fixture, r Request, work func(path string)) Worktree {
	t.Helper()
	ctx := context.Background()
	wt, err := f.w.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if work != nil {
		work(wt.Path)
	}
	if _, err = f.w.Seal(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = wt.Release(); err != nil {
		t.Fatal(err)
	}
	return wt
}

func TestStartCheck(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	acquire := func(r Request) (Worktree, error) {
		wt, err := f.w.Acquire(ctx, r)
		if err == nil {
			_ = wt.Release()
		}
		return wt, err
	}
	diverged := func(r Request, reason string) {
		t.Helper()
		var d *DivergedError
		if _, err := f.w.Acquire(ctx, r); !errors.As(err, &d) || d.Task != r.Task || d.Reason != reason {
			t.Fatalf("want diverged (%s), got %v", reason, err)
		}
	}

	t.Run("clean tree fast-forwards to the moved remote", func(t *testing.T) {
		r := f.task("t1")
		wt := runOnce(t, f, r, nil)
		remote := pushRemote(t, f, wt.Branch, "r.txt", "r\n")
		if _, err := acquire(r); err != nil {
			t.Fatal(err)
		}
		if head := run(t, wt.Path, "rev-parse", "HEAD"); head != remote {
			t.Fatalf("HEAD %s, want %s", head, remote)
		}
		if st, _ := readState(wt.Path); st.SealedHead != remote {
			t.Fatalf("sealed_head %s", st.SealedHead)
		}
	})

	t.Run("local commits of a run start as they are", func(t *testing.T) {
		r := f.task("t2")
		wt := runOnce(t, f, r, func(p string) {
			write(t, filepath.Join(p, "l.txt"), "l\n")
			run(t, p, "add", "-A")
			run(t, p, "commit", "-qm", "local")
		})
		head := run(t, wt.Path, "rev-parse", "HEAD")
		if _, err := acquire(r); err != nil {
			t.Fatal(err)
		}
		if got := run(t, wt.Path, "rev-parse", "HEAD"); got != head {
			t.Fatal("HEAD changed")
		}
	})

	t.Run("dirty tree without conflict fast-forwards and keeps the changes", func(t *testing.T) {
		r := f.task("t3")
		wt := runOnce(t, f, r, func(p string) { write(t, filepath.Join(p, "a.txt"), "mine\n") })
		remote := pushRemote(t, f, wt.Branch, "r.txt", "r\n")
		if _, err := acquire(r); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(filepath.Join(wt.Path, "a.txt")); string(b) != "mine\n" || run(t, wt.Path, "rev-parse", "HEAD") != remote {
			t.Fatalf("%q", b)
		}
	})

	t.Run("dirty tree conflicting with the remote is left untouched", func(t *testing.T) {
		r := f.task("t4")
		wt := runOnce(t, f, r, func(p string) { write(t, filepath.Join(p, "a.txt"), "mine\n") })
		head := run(t, wt.Path, "rev-parse", "HEAD")
		pushRemote(t, f, wt.Branch, "a.txt", "theirs\n")
		diverged(r, "the remote branch moved and uncommitted changes conflict with it")
		if b, _ := os.ReadFile(filepath.Join(wt.Path, "a.txt")); string(b) != "mine\n" || run(t, wt.Path, "rev-parse", "HEAD") != head {
			t.Fatal("worktree touched")
		}
		// The refused run released the lock.
		write(t, filepath.Join(wt.Path, "a.txt"), "a\n")
		if _, err := acquire(r); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("HEAD moved outside a run", func(t *testing.T) {
		r := f.task("t5")
		wt := runOnce(t, f, r, nil)
		write(t, filepath.Join(wt.Path, "x.txt"), "x\n")
		run(t, wt.Path, "add", "-A")
		run(t, wt.Path, "commit", "-qm", "outside")
		diverged(r, "HEAD changed outside any run")
		pushRemote(t, f, wt.Branch, "r.txt", "r\n")
		diverged(r, "HEAD changed outside any run")
	})

	t.Run("missing worktree is recreated and fast-forwarded", func(t *testing.T) {
		r := f.task("t6")
		wt := runOnce(t, f, r, nil)
		remote := pushRemote(t, f, wt.Branch, "r.txt", "r\n")
		if err := os.RemoveAll(wt.Path); err != nil {
			t.Fatal(err)
		}
		if _, err := acquire(r); err != nil {
			t.Fatal(err)
		}
		if got := run(t, wt.Path, "rev-parse", "HEAD"); got != remote {
			t.Fatalf("HEAD %s, want %s", got, remote)
		}
	})

	t.Run("a crashed run's commits are sealed as its own", func(t *testing.T) {
		r := f.task("t7")
		wt := runOnce(t, f, r, nil)
		pid, _ := startGroup(t, "sleep 60 & wait")
		if err := wt.Track(pid); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(wt.Path, "c.txt"), "c\n")
		run(t, wt.Path, "add", "-A")
		run(t, wt.Path, "commit", "-qm", "crashed run")
		if _, err := acquire(r); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRemove(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	r := f.task("t1")
	wt := runOnce(t, f, r, func(p string) { write(t, filepath.Join(p, "wip.txt"), "wip\n") })
	if found, err := f.w.Find(r.Company, r.Task); err != nil || len(found) != 1 || found[0].Project != "app" {
		t.Fatal(found, err)
	}
	held, err := f.w.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.w.Remove(ctx, r, 0, nil); !errors.Is(err, ErrBusy) {
		t.Fatalf("removed a held worktree: %v", err)
	}
	_ = held.Release()
	var hooked string
	res, err := f.w.Remove(ctx, r, 0, func(p string) {
		if _, err := os.Stat(filepath.Join(p, "wip.txt")); err == nil {
			hooked = p
		}
	})
	if err != nil || hooked != wt.Path || res.WIPSHA == "" {
		t.Fatal(res, hooked, err)
	}
	if _, err = os.Stat(wt.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("worktree left", err)
	}
	if _, err = os.Stat(wt.Scratch); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("scratch left", err)
	}
	if run(t, wt.Repo, "rev-parse", WIPRef(r.Task)) != res.WIPSHA || run(t, wt.Repo, "worktree", "list", "--porcelain") == "" {
		t.Fatal("WIP ref lost")
	}
	if found, _ := f.w.Find(r.Company, r.Task); len(found) != 0 {
		t.Fatal(found)
	}
	// A reopened task gets its worktree back from the branch.
	if _, err = f.w.Acquire(ctx, r); err != nil {
		t.Fatal(err)
	}

	// A failed seal keeps the worktree.
	broken := f.task("t2")
	bw := runOnce(t, f, broken, nil)
	if err = os.WriteFile(filepath.Join(bw.Path, ".git"), []byte("gitdir: /nonexistent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = f.w.Remove(ctx, broken, 0, func(string) { t.Fatal("hook ran before a failed seal") }); err == nil {
		t.Fatal("removed without seal")
	}
	if _, err = os.Stat(filepath.Join(bw.Path, ".factory", "state")); err != nil {
		t.Fatal("worktree removed after a failed seal", err)
	}
}
