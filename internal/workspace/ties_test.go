package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// node-ties tests: unsaved work saved to refs/factory/wip/<task>, restored on another node, and review copies.

func secretScanner(t *testing.T) string {
	t.Helper()
	scanner := filepath.Join(t.TempDir(), "scan")
	write(t, scanner, "#!/bin/sh\nif git log -p $1 | grep -qE 'AKIA[0-9A-Z]{16}'; then echo 'finding: aws key AKIA**** in config.env'; exit 1; fi\n")
	os.Chmod(scanner, 0o755)
	return scanner
}

// node is another node's Worktrees over the same remote.
func (f fixture) node(t *testing.T, name, scan string) *Worktrees {
	t.Helper()
	w, err := NewWorktrees(WorktreesConfig{Enabled: true, Root: filepath.Join(filepath.Dir(f.root), name), ScanCommand: []string{scan}})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func remoteRef(t *testing.T, remote, ref string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", remote, "rev-parse", "--verify", "--quiet", ref).Output()
	return strings.TrimSpace(string(out))
}

// timeout ends a run without an exit on w: seal, then save.
func timeout(t *testing.T, w *Worktrees, r Request) SealResult {
	t.Helper()
	res, err := w.SealExit(context.Background(), r, Exit{})
	if err != nil {
		t.Fatal(err)
	}
	return w.SaveWIP(context.Background(), r, res)
}

// TestWIPSavedAndRestored (node-ties 2.1, 2.3): a run times out on node A with a local commit, modified,
// deleted and untracked files; the seal saves them to the service ref (not ignored files); node B's next run
// starts from the remote branch with the same files as uncommitted changes.
func TestWIPSavedAndRestored(t *testing.T) {
	scan := secretScanner(t)
	f := setup(t, scan)
	ctx := context.Background()
	a, b := f.node(t, "nodeA", scan), f.node(t, "nodeB", scan)
	wt, err := a.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	p := wt.Path
	write(t, filepath.Join(p, ".gitignore"), "build/\n")
	write(t, filepath.Join(p, "commit.txt"), "committed locally\n")
	run(t, p, "add", "-A")
	run(t, p, "commit", "-qm", "local")
	write(t, filepath.Join(p, "a.txt"), "changed\n")
	os.Remove(filepath.Join(p, "b.txt"))
	write(t, filepath.Join(p, "new/untracked.txt"), "new\n")
	write(t, filepath.Join(p, "build/out.bin"), "ignored\n")
	wt.Release()
	res := timeout(t, a, f.req)
	if !res.LocalWork || res.SavedWIP == "" || res.Unsaved != "" {
		t.Fatalf("not saved: %+v", res)
	}
	if got := remoteRef(t, f.remote, WIPRef("t1")); got != res.SavedWIP {
		t.Fatalf("service ref %q, want %q", got, res.SavedWIP)
	}
	// The same files again (a run that changed nothing): no new commit.
	if again := timeout(t, a, f.req); again.SavedWIP != res.SavedWIP {
		t.Fatal("unchanged work saved again", again.SavedWIP)
	}

	r := f.req
	r.WIP = res.SavedWIP
	wb, err := b.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	defer wb.Release()
	q := wb.Path
	for file, want := range map[string]string{"a.txt": "changed\n", "commit.txt": "committed locally\n", "new/untracked.txt": "new\n", ".gitignore": "build/\n"} {
		if got, err := os.ReadFile(filepath.Join(q, file)); err != nil || string(got) != want {
			t.Fatalf("%s = %q, %v", file, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(q, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("deleted file came back", err)
	}
	if _, err := os.Stat(filepath.Join(q, "build/out.bin")); !os.IsNotExist(err) {
		t.Fatal("ignored file was carried", err)
	}
	// Files, not commits: HEAD is the default branch (the task branch was never pushed).
	if head, main := run(t, q, "rev-parse", "HEAD"), run(t, f.remote, "rev-parse", "main"); head != main {
		t.Fatalf("HEAD %s, want %s", head, main)
	}
	if st := run(t, q, "status", "--porcelain"); !strings.Contains(st, "commit.txt") || !strings.Contains(st, "a.txt") {
		t.Fatalf("work not uncommitted: %q", st)
	}
}

// TestWIPNotSaved (node-ties 4.1, decisions 1 and 2): a secret in an uncommitted file or a file over the cap
// keeps the work on the node with the reason; nothing reaches the remote.
func TestWIPNotSaved(t *testing.T) {
	scan := secretScanner(t)
	f := setup(t, scan)
	ctx := context.Background()
	a := f.node(t, "nodeA", scan)
	wt, err := a.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(wt.Path, "config.env"), "AWS_KEY="+secret+"\n")
	wt.Release()
	res := timeout(t, a, f.req)
	if !res.LocalWork || res.SavedWIP != "" || !strings.HasPrefix(res.Unsaved, "secret finding: ") || strings.Contains(res.Unsaved, secret) {
		t.Fatalf("%+v", res)
	}
	if got := remoteRef(t, f.remote, WIPRef("t1")); got != "" {
		t.Fatal("secret pushed", got)
	}

	old := MaxWIPFileBytes
	MaxWIPFileBytes = 16
	defer func() { MaxWIPFileBytes = old }()
	os.Remove(filepath.Join(wt.Path, "config.env"))
	write(t, filepath.Join(wt.Path, "render.png"), strings.Repeat("x", 64))
	res = timeout(t, a, f.req)
	if res.SavedWIP != "" || !strings.Contains(res.Unsaved, "file render.png is larger than") {
		t.Fatalf("%+v", res)
	}
}

// TestWIPSupersededAndDeleted (node-ties 2.3, 2.4): node A's saved leftovers are dropped when the task moved on
// (another node continued and pushed), and the service ref goes once nothing is local any more and when the
// closed task's worktree is removed.
func TestWIPSupersededAndDeleted(t *testing.T) {
	scan := secretScanner(t)
	f := setup(t, scan)
	ctx := context.Background()
	a, b := f.node(t, "nodeA", scan), f.node(t, "nodeB", scan)
	wt, _ := a.Acquire(ctx, f.req)
	write(t, filepath.Join(wt.Path, "a.txt"), "draft\n")
	wt.Release()
	saved := timeout(t, a, f.req).SavedWIP
	// The same node continues without Moved (or an older Core names no WIP): its leftovers stay as they are.
	again, err := a.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(again.Path, "a.txt")); string(b) != "draft\n" {
		t.Fatal("leftovers dropped although the task did not move", string(b))
	}
	again.Release()
	// Node B continues, commits and pushes; its clean seal deletes the service ref.
	r := f.req
	r.WIP = saved
	wb, err := b.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	run(t, wb.Path, "add", "-A")
	run(t, wb.Path, "commit", "-qm", "finish")
	run(t, wb.Path, "push", "-q", "origin", "HEAD")
	wb.Release()
	if res := timeout(t, b, r); res.LocalWork || res.SavedWIP != "" {
		t.Fatalf("%+v", res)
	}
	if got := remoteRef(t, f.remote, WIPRef("t1")); got != "" {
		t.Fatal("service ref kept after the work was pushed", got)
	}
	// Back on node A after B (Core records no WIP now): the stale draft is not continued; A starts from the remote branch.
	r.WIP, r.Moved = "", true
	wa, err := a.Acquire(ctx, r)
	if err != nil {
		t.Fatal("stale leftovers blocked the start:", err)
	}
	if head, want := run(t, wa.Path, "rev-parse", "HEAD"), run(t, f.remote, "rev-parse", wt.Branch); head != want || run(t, wa.Path, "status", "--porcelain") != "" {
		t.Fatalf("HEAD %s want %s, status %q", head, want, run(t, wa.Path, "status", "--porcelain"))
	}
	// Unsaved work again, saved, then the closed task's worktree is removed: the service ref goes with it.
	write(t, filepath.Join(wa.Path, "later.txt"), "x\n")
	wa.Release()
	if timeout(t, a, r).SavedWIP == "" || remoteRef(t, f.remote, WIPRef("t1")) == "" {
		t.Fatal("not saved")
	}
	if _, err = a.Remove(ctx, r, 0, nil); err != nil {
		t.Fatal(err)
	}
	if got := remoteRef(t, f.remote, WIPRef("t1")); got != "" {
		t.Fatal("service ref kept after the task closed", got)
	}
}

// TestReviewCopy (node-ties 3.1, 3.3): a review run works in a disposable copy at the pushed head of the task
// branch; it may merge main and commit there, but cannot push, the implementer's worktree is untouched, only the
// project's carry directories (ignored ones) come along, and the copy is gone after the run.
func TestReviewCopy(t *testing.T) {
	scan := secretScanner(t)
	f := setup(t, scan)
	ctx := context.Background()
	a := f.node(t, "nodeA", scan)
	if _, err := a.ReviewCopy(ctx, f.req, nil); !errors.As(err, new(*NotReviewableError)) {
		t.Fatal("unpushed branch reviewable:", err)
	}
	wt, err := a.Acquire(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	impl := wt.Path
	write(t, filepath.Join(impl, ".gitignore"), ".cache/\n.env\n")
	write(t, filepath.Join(impl, "feature.txt"), "feature\n")
	run(t, impl, "add", "-A")
	run(t, impl, "commit", "-qm", "feature")
	run(t, impl, "push", "-q", "-u", "origin", "HEAD")
	pushed := run(t, impl, "rev-parse", "HEAD")
	write(t, filepath.Join(impl, "local-only.txt"), "not pushed\n")
	write(t, filepath.Join(impl, ".cache/index"), "cache\n")
	write(t, filepath.Join(impl, ".env"), "TOKEN=x\n")
	wt.Release()
	// main moves on.
	seed := filepath.Join(filepath.Dir(f.root), "seed")
	write(t, filepath.Join(seed, "main.txt"), "main\n")
	run(t, seed, "add", "-A")
	run(t, seed, "commit", "-qm", "main moves")
	run(t, seed, "push", "-q", f.remote, "main")

	rc, err := a.ReviewCopy(ctx, f.req, []string{".cache", ".env", "../x"})
	if err != nil {
		t.Fatal(err)
	}
	c := rc.Path
	if head := run(t, c, "rev-parse", "HEAD"); head != pushed {
		t.Fatalf("copy at %s, want the pushed head %s", head, pushed)
	}
	if _, err = os.Stat(filepath.Join(c, "local-only.txt")); !os.IsNotExist(err) {
		t.Fatal("implementer's uncommitted file in the review copy")
	}
	if b, err := os.ReadFile(filepath.Join(c, ".cache/index")); err != nil || string(b) != "cache\n" {
		t.Fatal("carry dir missing", err)
	}
	if _, err = os.Stat(filepath.Join(c, ".env")); !os.IsNotExist(err) {
		t.Fatal(".env carried")
	}
	run(t, c, "merge", "-q", "--no-edit", "origin/main")
	write(t, filepath.Join(c, "qa-note.txt"), "tmp\n")
	for _, args := range [][]string{{"push", "origin", "HEAD:refs/heads/" + wt.Branch}, {"push", f.remote, "HEAD:refs/heads/" + wt.Branch}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = c
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("review copy pushed: %v", args)
		}
		if args[1] == f.remote && !strings.Contains(string(out), "this is a review copy; your result is the verdict") {
			t.Fatalf("hook message missing: %s", out)
		}
	}
	if got := run(t, f.remote, "rev-parse", wt.Branch); got != pushed {
		t.Fatal("task branch moved", got)
	}
	if err = rc.Release(); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(c); !os.IsNotExist(err) {
		t.Fatal("review copy kept", err)
	}
	// The implementer's worktree is as it left it: no merge commit, its own files.
	if head := run(t, impl, "rev-parse", "HEAD"); head != pushed {
		t.Fatal("implementer HEAD moved", head)
	}
	if _, err = os.Stat(filepath.Join(impl, "local-only.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(impl, "qa-note.txt")); !os.IsNotExist(err) {
		t.Fatal("reviewer file in the implementer's worktree")
	}
}

func TestValidCarry(t *testing.T) {
	for _, ok := range []string{".cache", "node_modules", "Library/Caches", "build/tmp/"} {
		if err := ValidCarry(ok); err != nil {
			t.Error(ok, err)
		}
	}
	for _, bad := range []string{"", ".", "/abs", "../up", "a/../../b", "~/x", ".git", ".git/hooks", ".factory"} {
		if ValidCarry(bad) == nil {
			t.Error("accepted", bad)
		}
	}
}
