package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// commit adds everything in the worktree and commits it; it returns the new HEAD.
func commit(t *testing.T, path, file, content string) string {
	t.Helper()
	write(t, filepath.Join(path, file), content)
	run(t, path, "add", "-A")
	run(t, path, "commit", "-qm", "add "+file)
	return run(t, path, "rev-parse", "HEAD")
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// TestSealExitContinuity: after handoff, submit and done the worktree is reset to the exit's commit and
// the next run starts there; work after the exit survives only on the WIP ref. After ask, block, wait or
// no exit the worktree continues as it is (task-worktrees D4, 2.2).
func TestSealExitContinuity(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	// runExit is one run: commit the exit's work, then work after the exit, then seal with the exit.
	runExit := func(t *testing.T, task, kind string, after func(path string)) (Worktree, string, SealResult) {
		t.Helper()
		r := f.task(task)
		wt, err := f.w.Acquire(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(wt.Path, ".gitignore"), "build.log\n")
		c := commit(t, wt.Path, "exit.txt", "exit work\n")
		if after != nil {
			after(wt.Path)
		}
		res, err := f.w.SealExit(ctx, r, Exit{Kind: kind, SHA: c})
		if err != nil {
			t.Fatal(err)
		}
		if err = wt.Release(); err != nil {
			t.Fatal(err)
		}
		return wt, c, res
	}
	// next acquires the task again: the start check must pass and HEAD is returned.
	next := func(t *testing.T, task string) string {
		t.Helper()
		wt, err := f.w.Acquire(ctx, f.task(task))
		if err != nil {
			t.Fatal(err)
		}
		defer wt.Release()
		return run(t, wt.Path, "rev-parse", "HEAD")
	}
	dirty := func(path string) {
		write(t, filepath.Join(path, "late.txt"), "after the exit\n")
		write(t, filepath.Join(path, "a.txt"), "changed after the exit\n")
		write(t, filepath.Join(path, "build.log"), "ignored\n")
	}

	for _, kind := range []string{"handoff", "submit", "done"} {
		t.Run(kind+" with uncommitted work after the exit", func(t *testing.T) {
			wt, c, res := runExit(t, "r-"+kind, kind, dirty)
			if !res.Reset || res.SealedHead != c || res.ExitSHA != c || res.WIPSHA == "" {
				t.Fatalf("%+v, exit %s", res, c)
			}
			if head := run(t, wt.Path, "rev-parse", "HEAD"); head != c {
				t.Fatalf("HEAD %s, want the exit %s", head, c)
			}
			if s := run(t, wt.Path, "status", "--porcelain"); s != "" {
				t.Fatalf("tree not clean after the reset: %q", s)
			}
			if !exists(filepath.Join(wt.Path, "build.log")) || !exists(filepath.Join(wt.Path, ".factory", "state")) {
				t.Fatal("reset removed ignored files")
			}
			wip := WIPRef(wt.Path[len(filepath.Dir(wt.Path))+1:])
			if run(t, wt.Path, "show", wip+":late.txt") != "after the exit" || run(t, wt.Path, "show", wip+":a.txt") != "changed after the exit" || run(t, wt.Path, "rev-parse", wip+"^") != c {
				t.Fatal("WIP ref lacks the work after the exit")
			}
			if st, _ := readState(wt.Path); st.SealedHead != c || st.ExitSHA != c || st.ResetFrom != "" {
				t.Fatalf("state %+v", st)
			}
			if head := next(t, "r-"+kind); head != c {
				t.Fatalf("next run starts at %s, want %s", head, c)
			}
		})
	}

	t.Run("commits after the exit are kept on the WIP ref", func(t *testing.T) {
		var late string
		wt, c, res := runExit(t, "r-late", "submit", func(path string) { late = commit(t, path, "late.txt", "late\n") })
		if res.WIPSHA != late || run(t, wt.Path, "rev-parse", WIPRef("r-late")) != late || run(t, wt.Path, "rev-parse", "HEAD") != c {
			t.Fatalf("%+v, late %s", res, late)
		}
		if exists(filepath.Join(wt.Path, "late.txt")) {
			t.Fatal("late commit still checked out")
		}
	})

	t.Run("a commit after the exit and uncommitted work", func(t *testing.T) {
		var late string
		wt, c, res := runExit(t, "r-both", "done", func(path string) {
			late = commit(t, path, "late.txt", "late\n")
			write(t, filepath.Join(path, "draft.txt"), "draft\n")
		})
		if res.WIPSHA == "" || run(t, wt.Path, "rev-parse", res.WIPSHA+"^") != late || run(t, wt.Path, "rev-parse", "HEAD") != c {
			t.Fatalf("%+v", res)
		}
	})

	for _, kind := range []string{"ask", "block", "wait", ""} {
		t.Run("continue as is after "+kind, func(t *testing.T) {
			task := "c-" + kind
			if kind == "" {
				task = "c-none"
			}
			wt, c, res := runExit(t, task, kind, dirty)
			if res.Reset || res.SealedHead != c || res.WIPSHA == "" {
				t.Fatalf("%+v", res)
			}
			if kind != "" && res.ExitSHA != c {
				t.Fatalf("exit_sha %s, want %s", res.ExitSHA, c)
			}
			if run(t, wt.Path, "show", "HEAD:a.txt") != "a" || !exists(filepath.Join(wt.Path, "late.txt")) {
				t.Fatal("worktree changed")
			}
			if head := next(t, task); head != c || !exists(filepath.Join(wt.Path, "late.txt")) {
				t.Fatalf("next run at %s", head)
			}
		})
	}

	t.Run("exit_sha is kept by later seals", func(t *testing.T) {
		wt, c, _ := runExit(t, "keep", "submit", nil)
		res, err := f.w.Seal(ctx, f.task("keep"))
		if err != nil || res.ExitSHA != c || res.Reset || res.SealedHead != c {
			t.Fatalf("%+v %v", res, err)
		}
		if run(t, wt.Path, "rev-parse", "HEAD") != c {
			t.Fatal("moved")
		}
	})

	t.Run("an unknown exit commit is not reset to", func(t *testing.T) {
		r := f.task("bad")
		wt, err := f.w.Acquire(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		defer wt.Release()
		c := commit(t, wt.Path, "x.txt", "x\n")
		res, err := f.w.SealExit(ctx, r, Exit{Kind: "submit", SHA: "0123456789012345678901234567890123456789"})
		if err == nil || res.Reset || res.SealedHead != c || run(t, wt.Path, "rev-parse", "HEAD") != c {
			t.Fatalf("%+v %v", res, err)
		}
	})
}

// A reset interrupted between recording it and finishing it is completed by the next start check (and
// by a seal), so HEAD always ends at the exit and the start check passes.
func TestInterruptedResetFinishes(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	for _, via := range []string{"acquire", "seal"} {
		t.Run(via, func(t *testing.T) {
			r := f.task("t-" + via)
			wt, err := f.w.Acquire(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			c := commit(t, wt.Path, "exit.txt", "exit\n")
			late := commit(t, wt.Path, "late.txt", "late\n")
			write(t, filepath.Join(wt.Path, "draft.txt"), "draft\n")
			if _, err = f.w.Seal(ctx, r); err != nil {
				t.Fatal(err)
			}
			wip := run(t, wt.Path, "rev-parse", WIPRef(r.Task))
			// The crash: the reset is recorded, HEAD still at the late commit.
			st, _ := readState(wt.Path)
			st.ResetFrom, st.SealedHead = late, c
			if err = writeState(wt.Path, st); err != nil {
				t.Fatal(err)
			}
			if err = wt.Release(); err != nil {
				t.Fatal(err)
			}
			if via == "seal" {
				if _, err = f.w.Seal(ctx, r); err != nil {
					t.Fatal(err)
				}
			} else {
				again, err := f.w.Acquire(ctx, r)
				if err != nil {
					t.Fatal(err)
				}
				_ = again.Release()
			}
			if head := run(t, wt.Path, "rev-parse", "HEAD"); head != c || run(t, wt.Path, "status", "--porcelain") != "" {
				t.Fatalf("HEAD %s, want %s", head, c)
			}
			if run(t, wt.Path, "rev-parse", WIPRef(r.Task)) != wip {
				t.Fatal("the WIP ref was replaced")
			}
			if st, _ = readState(wt.Path); st.ResetFrom != "" || st.SealedHead != c {
				t.Fatalf("state %+v", st)
			}
		})
	}
}

// fresh_from_default: every run starts from the freshly fetched default branch, detached and clean; what
// the previous run left is sealed first.
func TestFreshFromDefault(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	r := f.task("rel")
	r.Fresh = true
	wt, err := f.w.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	main := run(t, f.remote, "rev-parse", "main")
	if wt.Branch != "" || run(t, wt.Path, "rev-parse", "HEAD") != main || run(t, wt.Path, "rev-parse", "--abbrev-ref", "HEAD") != "HEAD" {
		t.Fatalf("not detached at main: %+v", wt)
	}
	if run(t, wt.Path, "for-each-ref", "refs/heads/af/") != "" {
		t.Fatal("a task branch was created")
	}
	local := commit(t, wt.Path, "release.txt", "notes\n")
	write(t, filepath.Join(wt.Path, "draft.txt"), "draft\n")
	if _, err = f.w.Seal(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err = wt.Release(); err != nil {
		t.Fatal(err)
	}
	moved := pushRemote(t, f, "main", "news.txt", "news\n")
	again, err := f.w.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Release()
	if again.Path != wt.Path || run(t, wt.Path, "rev-parse", "HEAD") != moved || run(t, wt.Path, "status", "--porcelain") != "" {
		t.Fatal("next run is not at the fetched default branch")
	}
	if wip := WIPRef("rel"); run(t, wt.Path, "show", wip+":draft.txt") != "draft" || run(t, wt.Path, "rev-parse", wip+"^") != local {
		t.Fatal("previous run's work not sealed")
	}
	if st, _ := readState(wt.Path); st.SealedHead != moved {
		t.Fatalf("sealed_head %s", st.SealedHead)
	}
}

// Unpushed reports commits that no origin branch contains (node pinning).
func TestSealUnpushed(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	r := f.task("p1")
	wt, err := f.w.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.Release()
	if res, err := f.w.Seal(ctx, r); err != nil || res.Unpushed {
		t.Fatalf("%+v %v", res, err)
	}
	commit(t, wt.Path, "x.txt", "x\n")
	if res, err := f.w.Seal(ctx, r); err != nil || !res.Unpushed {
		t.Fatalf("%+v %v", res, err)
	}
	run(t, wt.Path, "push", "-q", "origin", "HEAD:refs/heads/"+wt.Branch)
	if res, err := f.w.Seal(ctx, r); err != nil || res.Unpushed {
		t.Fatalf("after push: %+v %v", res, err)
	}
}

func TestIsLockError(t *testing.T) {
	for stderr, want := range map[string]bool{
		"fatal: Unable to create '/r/.git/index.lock': File exists.":           true,
		"error: could not lock config file /r/config: File exists":             true,
		"error: cannot lock ref 'refs/heads/af/t1': Unable to create":          true,
		"fatal: Unable to create '/r/packed-refs.lock': File exists":           true,
		"fatal: repository '/nowhere' does not exist":                          false,
		"error: pathspec 'x' did not match any file(s) known to git (no lock)": false,
	} {
		err := error(&gitError{args: []string{"x"}, err: errors.New("exit status 128"), stderr: stderr})
		if got := IsLockError(errors.Join(errors.New("worktree"), err)); got != want {
			t.Errorf("%q: %v", stderr, got)
		}
	}
	if IsLockError(errors.New("index.lock")) || IsLockError(nil) {
		t.Fatal("not a git error")
	}
}
