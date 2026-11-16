package workspace

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/gitexclude"
)

// storeFixture: an origin that serves partial clones (as GitHub does), with a history of a large file (above
// the 1k limit the tests use) and small text files.
type storeFixture struct {
	base, url, origin string
	req               Request
}

func blob(n int, seed string) string { return strings.Repeat(seed, n/len(seed)+1)[:n] }

func newStore(t *testing.T) storeFixture {
	t.Helper()
	base := t.TempDir()
	base, _ = filepath.EvalSymlinks(base)
	gc := filepath.Join(base, "gitconfig")
	write(t, gc, "[user]\n\tname = Test\n\temail = test@example.com\n[init]\n\tdefaultBranch = main\n")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	origin := filepath.Join(base, "origin.git")
	run(t, base, "init", "--bare", "-q", origin)
	run(t, origin, "config", "uploadpack.allowFilter", "true")
	run(t, origin, "config", "uploadpack.allowAnySHA1InWant", "true")
	seed := filepath.Join(base, "seed")
	run(t, base, "init", "-q", seed)
	for i, v := range []string{"one", "two", "three"} {
		write(t, filepath.Join(seed, "art.bin"), blob(4096, v))
		write(t, filepath.Join(seed, "notes.txt"), "version "+v+"\n")
		run(t, seed, "add", "-A")
		run(t, seed, "commit", "-qm", "v"+string(rune('1'+i)))
	}
	run(t, seed, "push", "-q", origin, "main")
	url := "file://" + origin
	return storeFixture{base: base, url: url, origin: origin, req: Request{Company: "co", Project: "p", Task: "t1", RepoURL: url, DefaultBranch: "main"}}
}

func (f storeFixture) trees(t *testing.T, limit string) *Worktrees {
	t.Helper()
	w, err := NewWorktrees(WorktreesConfig{Enabled: true, Root: filepath.Join(f.base, "root"), ScanCommand: []string{"true"}, BlobLimit: limit})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// missing lists the objects reachable in repo that it does not hold (promised by origin).
func missing(t *testing.T, repo string) []string {
	t.Helper()
	var out []string
	for _, l := range strings.Split(run(t, repo, "rev-list", "--objects", "--all", "--missing=print"), "\n") {
		if strings.HasPrefix(l, "?") {
			out = append(out, l[1:])
		}
	}
	return out
}

// TestPartialProjectClone: with a blob limit the project clone holds the history of small files and none of
// the old versions of large ones; a command that needs one fetches it. Without a limit the clone is full.
func TestPartialProjectClone(t *testing.T) {
	f := newStore(t)
	ctx := context.Background()
	wt, err := f.trees(t, "1k").Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	if !partial(ctx, wt.Repo) || run(t, wt.Repo, "config", "repack.writeBitmaps") != "false" {
		t.Fatal("the project clone is not partial")
	}
	if n := len(missing(t, wt.Repo)); n != 2 {
		t.Fatalf("missing %d objects, want the two old versions of art.bin", n)
	}
	if run(t, wt.Path, "log", "-p", "--format=%s", "--", "notes.txt") == "" || run(t, wt.Path, "cat-file", "-s", "HEAD~2:art.bin") != "4096" {
		t.Fatal("history not readable")
	}
	if got := run(t, wt.Path, "show", "HEAD~2:art.bin"); got != blob(4096, "one") {
		t.Fatal("old version not fetched")
	}

	g := newStore(t)
	wt, err = g.trees(t, "0").Ensure(ctx, g.req)
	if err != nil {
		t.Fatal(err)
	}
	if partial(ctx, wt.Repo) || len(missing(t, wt.Repo)) != 0 {
		t.Fatal("blob limit 0 must keep a full clone")
	}
	if !ValidBlobLimit("512k") || !ValidBlobLimit("0") || ValidBlobLimit("1x") || ValidBlobLimit("-1m") {
		t.Fatal("ValidBlobLimit")
	}
}

// TestCompactKeepsLocalWork: a full clone made by an earlier node becomes partial without losing what only the
// node holds: an unpushed commit, a staged file, uncommitted changes, a WIP ref. Old versions origin holds are
// dropped and fetched again when needed. A clone in use is left for later.
func TestCompactKeepsLocalWork(t *testing.T) {
	f := newStore(t)
	ctx := context.Background()
	full := f.trees(t, "0")
	t1, err := full.Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	r2 := f.req
	r2.Task = "t2"
	t2, err := full.Ensure(ctx, r2)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(t1.Path, "big-new.bin"), blob(5000, "unpushed"))
	run(t, t1.Path, "add", "big-new.bin")
	run(t, t1.Path, "commit", "-qm", "unpushed")
	run(t, t1.Path, "update-ref", WIPRef("t1"), "HEAD")
	write(t, filepath.Join(t2.Path, "staged.bin"), blob(5000, "staged"))
	run(t, t2.Path, "add", "staged.bin")
	write(t, filepath.Join(t2.Path, "notes.txt"), "edited\n")
	before := run(t, t2.Path, "status", "--porcelain")
	dir := filepath.Dir(t1.Repo)

	w := f.trees(t, "1k")
	unlock, err := flockTry(filepath.Join(t1.Path, ".factory", "lock"))
	if err != nil {
		t.Fatal(err)
	}
	if done, err := w.Compact(ctx, dir); done || err != nil {
		t.Fatal("compacted a clone in use", done, err)
	}
	unlock()
	if done, err := w.Compact(ctx, dir); !done || err != nil {
		t.Fatal(done, err)
	}
	if !partial(ctx, t1.Repo) || pathExists(filepath.Join(dir, "repo.compact")) || pathExists(filepath.Join(dir, "repo.old")) {
		t.Fatal("not compacted cleanly")
	}
	if len(missing(t, t1.Repo)) == 0 {
		t.Fatal("old versions are still held")
	}
	if run(t, t1.Path, "show", "HEAD:big-new.bin") != blob(5000, "unpushed") || run(t, t1.Path, "rev-parse", WIPRef("t1")) != run(t, t1.Path, "rev-parse", "HEAD") {
		t.Fatal("unpushed work lost")
	}
	if run(t, t2.Path, "show", ":staged.bin") != blob(5000, "staged") || run(t, t2.Path, "status", "--porcelain") != before {
		t.Fatal("staged or uncommitted work lost")
	}
	if run(t, t1.Path, "show", "HEAD~3:art.bin") != blob(4096, "one") {
		t.Fatal("an old version is not fetched again")
	}
	run(t, t1.Repo, "gc", "-q")
	if run(t, t1.Path, "show", "HEAD:big-new.bin") != blob(5000, "unpushed") {
		t.Fatal("gc dropped local work")
	}
	if _, err := full.Ensure(ctx, Request{Company: "co", Project: "p", Task: "t3", RepoURL: f.url, DefaultBranch: "main"}); err != nil {
		t.Fatal("a new worktree of the compacted clone:", err)
	}
	if done, err := w.Compact(ctx, dir); !done || err != nil {
		t.Fatal("compacting again", done, err)
	}
}

// TestRecoverCompact: a compaction a crash interrupted is dropped before it was verified and finished after.
func TestRecoverCompact(t *testing.T) {
	dir := t.TempDir()
	repo, tmp, old := filepath.Join(dir, "repo"), filepath.Join(dir, "repo.compact"), filepath.Join(dir, "repo.old")
	mk := func(p string) { t.Helper(); write(t, filepath.Join(p, "id"), filepath.Base(p)) }
	id := func(p string) string { b, _ := os.ReadFile(filepath.Join(p, "id")); return string(b) }

	mk(repo)
	mk(tmp)
	if err := recoverCompact(dir); err != nil || pathExists(tmp) || id(repo) != "repo" {
		t.Fatal("an unverified clone must be dropped", err)
	}
	mk(tmp)
	write(t, filepath.Join(tmp, compactReady), "")
	mk(filepath.Join(repo, "worktrees", "t1"))
	if err := recoverCompact(dir); err != nil || id(repo) != "repo.compact" || !pathExists(filepath.Join(repo, "worktrees", "t1")) || pathExists(old) || pathExists(filepath.Join(repo, compactReady)) {
		t.Fatal("a verified clone must replace the old one with its worktrees", err)
	}
	os.RemoveAll(repo)
	mk(old)
	if err := recoverCompact(dir); err != nil || id(repo) != "repo.old" {
		t.Fatal("the old clone must come back when there is no other", err)
	}
}

// TestAdoptWorkspace: the project workspace the node cloned becomes a worktree of the project clone with
// everything it held: files, index, HEAD branch, ignored and untracked files, excludes, unpushed branches and
// the node's marker. A branch the project clone holds with work of its own is not moved.
func TestAdoptWorkspace(t *testing.T) {
	f := newStore(t)
	ctx := context.Background()
	w := f.trees(t, "1k")
	task, err := w.Ensure(ctx, f.req)
	if err != nil {
		t.Fatal(err)
	}
	ws := filepath.Join(f.base, "projects", "p")
	run(t, f.base, "clone", "-q", f.url, ws)
	write(t, filepath.Join(ws, writerLock), "")
	write(t, filepath.Join(ws, ".git", "factory-project"), "co/p\n")
	if err := gitexclude.Add(ctx, ws, "/.factory/"); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(ws, ".factory", "manifest.json"), "{}")
	write(t, filepath.Join(ws, ".gitignore"), "*.log\n")
	write(t, filepath.Join(ws, "debug.log"), "ignored")
	// An unpushed branch with a large file, and a branch the project clone also has with work of its own.
	run(t, ws, "checkout", "-q", "-b", "feature")
	write(t, filepath.Join(ws, "feature.bin"), blob(3000, "feature"))
	run(t, ws, "add", "feature.bin")
	run(t, ws, "commit", "-qm", "feature")
	run(t, ws, "checkout", "-q", "-b", "clash", "main")
	write(t, filepath.Join(ws, "clash.txt"), "workspace\n")
	run(t, ws, "add", "clash.txt")
	run(t, ws, "commit", "-qm", "clash in the workspace")
	write(t, filepath.Join(task.Path, "own.txt"), "task\n")
	run(t, task.Path, "add", "own.txt")
	run(t, task.Path, "commit", "-qm", "task work")
	run(t, task.Path, "branch", "clash", "HEAD")
	clashRepo := run(t, task.Repo, "rev-parse", "clash")
	run(t, ws, "checkout", "-q", "feature")
	write(t, filepath.Join(ws, "notes.txt"), "edited\n")
	write(t, filepath.Join(ws, "new.txt"), "untracked\n")
	before := run(t, ws, "status", "--porcelain", "--untracked-files=all")
	head := run(t, ws, "rev-parse", "HEAD")

	ok, err := w.AdoptWorkspace(ctx, "co", "p", f.url, ws)
	if !ok || err != nil {
		t.Fatal(ok, err)
	}
	if fi, err := os.Lstat(filepath.Join(ws, ".git")); err != nil || fi.IsDir() || pathExists(ws+adoptedSuffix) {
		t.Fatal("the workspace keeps its own repository")
	}
	if got := run(t, ws, "rev-parse", "--path-format=absolute", "--git-common-dir"); got != task.Repo {
		t.Fatal("common dir", got)
	}
	if run(t, ws, "status", "--porcelain", "--untracked-files=all") != before || run(t, ws, "rev-parse", "HEAD") != head || run(t, ws, "symbolic-ref", "HEAD") != "refs/heads/feature" {
		t.Fatal("the workspace changed:\n" + run(t, ws, "status", "--porcelain", "--untracked-files=all"))
	}
	if run(t, ws, "show", "HEAD:feature.bin") != blob(3000, "feature") {
		t.Fatal("unpushed work lost")
	}
	if run(t, task.Repo, "rev-parse", "clash") != clashRepo || run(t, task.Repo, "show", "refs/factory/workspace/heads/clash:clash.txt") != "workspace" {
		t.Fatal("a branch with work of its own was moved")
	}
	gd := run(t, ws, "rev-parse", "--absolute-git-dir")
	if b, _ := os.ReadFile(filepath.Join(gd, "factory-project")); string(b) != "co/p\n" {
		t.Fatal("marker lost")
	}
	if run(t, task.Path, "status", "--porcelain", "--untracked-files=all") != "" {
		t.Fatal("the task worktree sees the workspace's files")
	}
	if ok, err := w.AdoptWorkspace(ctx, "co", "p", f.url, ws); ok || err != nil {
		t.Fatal("adopting again", ok, err)
	}
	// The workspace pulls and commits as before.
	run(t, ws, "checkout", "-q", "main")
	run(t, ws, "pull", "-q")
	if run(t, ws, "rev-parse", "HEAD") != run(t, f.origin, "rev-parse", "main") {
		t.Fatal("pull")
	}
	// The project clone compacts with the workspace in it.
	if done, err := w.Compact(ctx, filepath.Dir(task.Repo)); !done || err != nil {
		t.Fatal(done, err)
	}
}

// TestAdoptWorkspaceRefusals: a busy workspace is left for later; one git cannot carry over is refused and kept
// as it is; another origin is never adopted.
func TestAdoptWorkspaceRefusals(t *testing.T) {
	f := newStore(t)
	ctx := context.Background()
	w := f.trees(t, "1k")
	ws := filepath.Join(f.base, "projects", "p")
	run(t, f.base, "clone", "-q", f.url, ws)
	unlock, err := flockTry(filepath.Join(ws, writerLock))
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := w.AdoptWorkspace(ctx, "co", "p", f.url, ws); ok || err != nil {
		t.Fatal("adopted a busy workspace", ok, err)
	}
	unlock()
	// A run that finds the workspace being adopted waits for it instead of failing.
	unlock, _ = flockTry(filepath.Join(ws, writerLock))
	write(t, ws+adoptingSuffix, "")
	got := make(chan error, 1)
	go func() {
		a, err := Local{}.Acquire(ctx, ws)
		if err == nil {
			a.Release()
		}
		got <- err
	}()
	time.Sleep(1500 * time.Millisecond)
	os.Remove(ws + adoptingSuffix)
	unlock()
	if err := <-got; err != nil {
		t.Fatal("a run during an adoption", err)
	}
	write(t, filepath.Join(ws, "notes.txt"), "stashed\n")
	run(t, ws, "stash", "-q")
	var na *NotAdoptableError
	if _, err := w.AdoptWorkspace(ctx, "co", "p", f.url, ws); !errors.As(err, &na) {
		t.Fatal("stash", err)
	}
	if fi, err := os.Lstat(filepath.Join(ws, ".git")); err != nil || !fi.IsDir() {
		t.Fatal("a refused workspace changed")
	}
	// Refused: not tried again until the retry time.
	run(t, ws, "stash", "drop", "-q")
	if ok, err := w.AdoptWorkspace(ctx, "co", "p", f.url, ws); ok || err != nil {
		t.Fatal("retried before the retry time", ok, err)
	}
	w.adoptAfter.Delete(ws)
	if _, err := w.AdoptWorkspace(ctx, "co", "p", "file:///elsewhere.git", ws); !errors.As(err, &na) {
		t.Fatal("another origin", err)
	}
}

// TestRecoverAdopt: an adoption a crash interrupted is finished when the workspace's link works, undone when not.
func TestRecoverAdopt(t *testing.T) {
	f := newStore(t)
	ctx := context.Background()
	ws := filepath.Join(f.base, "ws")
	run(t, f.base, "clone", "-q", f.url, ws)
	if err := os.Rename(filepath.Join(ws, ".git"), ws+adoptedSuffix); err != nil {
		t.Fatal(err)
	}
	if err := recoverAdopt(ctx, ws); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(ws, ".git")); err != nil || !fi.IsDir() || pathExists(ws+adoptedSuffix) {
		t.Fatal("not undone")
	}
	cmd := exec.Command("git", "status")
	cmd.Dir = ws
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
}
