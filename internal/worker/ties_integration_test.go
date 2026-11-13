package worker

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/workspace"
)

// node-ties 4.1 and 4.2: Core and two node workers with their own worktree roots over one local bare remote.

// hookDriver is the fake runtime with an agent: before the session starts, act does what the agent would do in
// its working directory (edit, commit, push, exit). Each agent has its own session id.
type hookDriver struct {
	*fake.Driver
	act func(l driver.Launch)
}

func (d hookDriver) Launch(ctx context.Context, l driver.Launch) (driver.Session, error) {
	if d.act != nil {
		d.act(l)
	}
	fd := &fake.Driver{Script: d.Script}
	fd.Script.SessionID = l.Spec.Agent + "-thread"
	return fd.Launch(ctx, l)
}

type tiesHarness struct {
	s      *storage.Store
	c      *core.Core
	fake   *clock.Fake
	remote string
	seed   string
	a, b   *Worker
	failed fake.Script
	aRoot  string
	bRoot  string
	nodeA  string
	nodeB  string
}

func newTiesHarness(t *testing.T) *tiesHarness {
	t.Helper()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	fk := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock, s.ExplicitExits = fk, true
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	dev := agents["demo"]
	dev.Spec.Workspace.Mode = "worktree"
	qa := dev
	qa.Metadata.Name = "qa"
	agents = map[string]config.Agent{"dev": dev, "qa": qa}
	c := &core.Core{Store: s, Agents: agents, Token: "test", ExplicitExits: true}
	server := httptest.NewServer(c.Handler())
	t.Cleanup(server.Close)
	h := &tiesHarness{s: s, c: c, fake: fk, remote: bareRemote(t)}
	h.seed = filepath.Join(t.TempDir(), "seed")
	gitOut(t, filepath.Dir(h.seed), "clone", "--quiet", h.remote, h.seed)
	scanner := filepath.Join(t.TempDir(), "scan")
	if err = os.WriteFile(scanner, []byte("#!/bin/sh\nif git log -p $1 | grep -qE 'AKIA[0-9A-Z]{16}'; then echo 'finding: aws key AKIA**** in config.env'; exit 1; fi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sc, err := fake.Load(filepath.Join("testdata", "fake", "completed.json"))
	if err != nil {
		t.Fatal(err)
	}
	if h.failed, err = fake.Load(filepath.Join("testdata", "fake", "failed.json")); err != nil {
		t.Fatal(err)
	}
	shim := buildShim(t)
	mk := func(root string) *Worker {
		trees, err := workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: root, ScanCommand: []string{scanner}})
		if err != nil {
			t.Fatal(err)
		}
		w := &Worker{Driver: hookDriver{Driver: &fake.Driver{Script: sc}}, Worktrees: trees, ShimBinary: shim}
		asNode(t, s, w, server.URL)
		return w
	}
	h.aRoot, h.bRoot = t.TempDir(), t.TempDir()
	h.a, h.b = mk(h.aRoot), mk(h.bRoot)
	h.nodeA, h.nodeB = h.a.Inventory.NodeID, h.b.Inventory.NodeID
	if _, err = s.CreateProject(context.Background(), storage.DefaultCompany, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: t.TempDir(), RepoURL: h.remote, DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *tiesHarness) create(t *testing.T, id, agent string) {
	t.Helper()
	if _, err := h.s.CreateTask(context.Background(), storage.DefaultCompany, tasks.Create{ID: id, ProjectID: "app", Title: id, Assignee: agent, ReviewBy: "owner", Ready: true, RequestID: "c-" + id}); err != nil {
		t.Fatal(err)
	}
}

// next dispatches the task's next run and returns its stored spec.
func (h *tiesHarness) next(t *testing.T, task string) protocol.RunSpec {
	t.Helper()
	ctx := context.Background()
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	tk, _ := h.s.Task(ctx, storage.DefaultCompany, task)
	r, err := h.s.Get(ctx, storage.DefaultCompany, tk.CurrentRunID)
	if err != nil || r.Status != "QUEUED" {
		t.Fatalf("task %s (%s) has no queued run: %+v %v", task, tk.Status, r, err)
	}
	return r.Spec
}

// run claims run on w and executes it with act as the agent; refused claims return the refusal.
func (h *tiesHarness) run(t *testing.T, w *Worker, run string, act func(l driver.Launch)) *Refusal {
	t.Helper()
	return h.runScript(t, w, run, nil, act)
}

// runScript is run with another session script (nil: completed).
func (h *tiesHarness) runScript(t *testing.T, w *Worker, run string, sc *fake.Script, act func(l driver.Launch)) *Refusal {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	epoch, c, err := w.ClaimV2(ctx, run, 0)
	var refusal *Refusal
	if errors.As(err, &refusal) {
		return refusal
	}
	if err != nil {
		t.Fatal(err)
	}
	base := w.Driver.(hookDriver).Script
	script := base
	if sc != nil {
		script = *sc
	}
	w.Driver = hookDriver{Driver: &fake.Driver{Script: script}, act: act}
	defer func() { w.Driver = hookDriver{Driver: &fake.Driver{Script: base}} }()
	runCtx, cancel := context.WithCancelCause(node.WithSlot(ctx, 0))
	defer cancel(nil)
	w.Inventory.AddCause(run, epoch, cancel)
	defer w.Inventory.End(run)
	if err = w.execute(runCtx, c.RunSpec, c.Lease, ""); err != nil {
		t.Log("execute:", err)
	}
	if r, _ := h.s.Get(ctx, storage.DefaultCompany, run); r.Status != "ENDED" {
		t.Fatalf("run %s not ended: %s", run, r.Status)
	}
	return nil
}

func (h *tiesHarness) exit(t *testing.T, run, kind string, args map[string]any, head string) {
	t.Helper()
	ctx := context.Background()
	var task string
	var ack int64
	if err := h.s.DB.QueryRowContext(ctx, "SELECT t.id,t.wake_seq FROM runs r JOIN tasks t ON t.company_id=r.company_id AND t.id=r.task_id WHERE r.id=$1", run).Scan(&task, &ack); err != nil {
		t.Fatal(err)
	}
	facts := &storage.ExitFacts{Head: head, Branch: "af/" + task, DefaultBranch: "main", AheadOfDefault: 1, Pushed: true, Scan: &storage.ScanFacts{OK: true}}
	if _, err := h.s.Exit(ctx, storage.DefaultCompany, run, storage.ExitRequest{Kind: kind, Arguments: args, AckSeq: ack, Facts: facts}); err != nil {
		t.Fatalf("%s exit: %v", kind, err)
	}
}

func fileIs(t *testing.T, path, want string) {
	t.Helper()
	if b, err := os.ReadFile(path); err != nil || string(b) != want {
		t.Fatalf("%s = %q, %v; want %q", path, b, err, want)
	}
}

// TestReplayVP3 (node-ties 4.1): a run ends without an exit with uncommitted files on node A; the seal saves them
// to refs/factory/wip/<task>, so the task is not tied to A: with A unavailable, node B takes the next run and
// finds the files. With a secret in a file the work is not saved, the task stays tied to A and says why.
func TestReplayVP3(t *testing.T) {
	h := newTiesHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	h.create(t, "vp3", "dev")
	r1 := h.next(t, "vp3")
	// The run fails without an exit (VP-3 timed out), leaving its files uncommitted.
	h.runScript(t, h.a, r1.ID, &h.failed, func(l driver.Launch) {
		fileIs(t, filepath.Join(l.Cwd, "README.md"), "app\n")
		os.WriteFile(filepath.Join(l.Cwd, "README.md"), []byte("app, half rewritten\n"), 0o644)
		os.MkdirAll(filepath.Join(l.Cwd, "src"), 0o755)
		os.WriteFile(filepath.Join(l.Cwd, "src", "talents.go"), []byte("package talents\n"), 0o644)
	})
	ws, err := h.s.TaskWorkspace(ctx, co, "vp3")
	if err != nil || ws.NodeID != h.nodeA || ws.Unpushed || ws.WIPSHA == "" {
		t.Fatalf("workspace %+v %v", ws, err)
	}
	if got := gitOut(t, h.remote, "rev-parse", workspace.WIPRef("vp3")); got != ws.WIPSHA {
		t.Fatalf("service ref %s, want %s", got, ws.WIPSHA)
	}
	// Node A is paused (busy elsewhere); after the retry backoff and the session release B takes the run.
	if err = h.s.SetPause(ctx, co, storage.Pause{Scope: "node", Target: h.nodeA, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	h.fake.Advance(2 * time.Hour)
	r2 := h.next(t, "vp3")
	if q, err := h.s.QueuedWhy(ctx, co, r2.ID); err != nil || q.Tie != nil && q.Tie.Reason == tasks.TieWork {
		t.Fatalf("tied by work after the save: %+v %v", q, err)
	}
	found := false
	if r := h.run(t, h.b, r2.ID, func(l driver.Launch) {
		found = true
		fileIs(t, filepath.Join(l.Cwd, "README.md"), "app, half rewritten\n")
		fileIs(t, filepath.Join(l.Cwd, "src", "talents.go"), "package talents\n")
	}); r != nil || !found {
		t.Fatalf("node B did not take the run: %+v", r)
	}

	// A secret in the unsaved work: not pushed, the task stays tied to A by its work with the reason.
	if err = h.s.ClearPause(ctx, co, "node", h.nodeA, "owner"); err != nil {
		t.Fatal(err)
	}
	h.create(t, "vp3s", "dev")
	s1 := h.next(t, "vp3s")
	h.runScript(t, h.a, s1.ID, &h.failed, func(l driver.Launch) {
		os.WriteFile(filepath.Join(l.Cwd, "config.env"), []byte("AWS_KEY=AKIA"+"ABCDEFGHIJKLMNOP\n"), 0o600)
	})
	ws, _ = h.s.TaskWorkspace(ctx, co, "vp3s")
	if !ws.Unpushed || ws.WIPSHA != "" || !strings.Contains(ws.WIPUnsaved, "secret finding") || ws.TiedSince == 0 {
		t.Fatalf("workspace %+v", ws)
	}
	if out, _ := gitOutErr(h.remote, "rev-parse", "--verify", "--quiet", workspace.WIPRef("vp3s")); out != "" {
		t.Fatal("secret pushed to the service ref")
	}
	if err = h.s.SetPause(ctx, co, storage.Pause{Scope: "node", Target: h.nodeA, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	h.fake.Advance(2 * time.Hour)
	s2 := h.next(t, "vp3s")
	if r := h.run(t, h.b, s2.ID, nil); r == nil || r.Reason != "pinned" {
		t.Fatalf("node B took a run tied to A by its work: %+v", r)
	}
	q, err := h.s.QueuedWhy(ctx, co, s2.ID)
	if err != nil || q.Tie == nil || q.Tie.Reason != tasks.TieWork || q.Tie.Node != h.nodeA || !strings.Contains(q.Tie.Unsaved, "secret finding") ||
		!strings.Contains(q.Detail, "its unsaved work is there (it could not be saved: secret finding") || q.Tie.Release != tasks.WorkRelease {
		t.Fatalf("queued %+v %v", q, err)
	}
	snap, _ := h.s.TaskSnapshot(ctx, co, "vp3s")
	if v := tasks.Evaluate(snap); v.Tie == nil || v.Tie.Reason != tasks.TieWork || v.Tie.Node != h.nodeA {
		t.Fatalf("verdict tie %+v", v.Tie)
	}
}

// TestReplay85622aeb (node-ties 4.2): the implementer pushes the branch from node A and submits; the reviewer on
// node B merges origin/main in its copy and requests changes. The task is not tied to B, Core's workspace record is
// A's, the copy is gone, and the implementer's next run — on B, once A is unavailable — starts from the remote
// branch without the reviewer's merge commit or files.
func TestReplay85622aeb(t *testing.T) {
	h := newTiesHarness(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	// The task is reviewed by qa (a task's reviewer is binding, approval-gates-merge).
	if _, err := h.s.CreateTask(ctx, co, tasks.Create{ID: "cls", ProjectID: "app", Title: "cls", Assignee: "dev", ReviewBy: "qa", Ready: true, RequestID: "c-cls"}); err != nil {
		t.Fatal(err)
	}
	r1 := h.next(t, "cls")
	if r1.ReviewCopy {
		t.Fatal("the implementer's run is a review copy")
	}
	var pushed string
	h.run(t, h.a, r1.ID, func(l driver.Launch) {
		os.WriteFile(filepath.Join(l.Cwd, "talents.go"), []byte("package talents\n"), 0o644)
		gitOut(t, l.Cwd, "add", "-A")
		gitOut(t, l.Cwd, "commit", "--quiet", "-m", "class talents core")
		gitOut(t, l.Cwd, "push", "--quiet", "-u", "origin", "HEAD")
		pushed = gitOut(t, l.Cwd, "rev-parse", "HEAD")
		h.exit(t, r1.ID, "submit", map[string]any{"summary": "talents", "reviewer": "qa"}, pushed)
	})
	// main moves on meanwhile.
	os.WriteFile(filepath.Join(h.seed, "main.txt"), []byte("main\n"), 0o644)
	gitOut(t, h.seed, "add", "-A")
	gitOut(t, h.seed, "commit", "--quiet", "-m", "main moves")
	gitOut(t, h.seed, "push", "--quiet", "origin", "HEAD:main")

	r2 := h.next(t, "cls")
	if r2.Agent != "qa" || !r2.ReviewCopy {
		t.Fatalf("review run %s review_copy=%v", r2.Agent, r2.ReviewCopy)
	}
	var copyPath, merged string
	h.run(t, h.b, r2.ID, func(l driver.Launch) {
		copyPath = l.Cwd
		if !strings.Contains(l.Instructions, ReviewCopyNote) {
			t.Error("review run not told about its review copy")
		}
		if !strings.Contains(l.Cwd, string(filepath.Separator)+"reviews"+string(filepath.Separator)) || gitOut(t, l.Cwd, "rev-parse", "HEAD") != pushed {
			t.Errorf("review copy %s at %s, want the pushed head %s", l.Cwd, gitOut(t, l.Cwd, "rev-parse", "HEAD"), pushed)
		}
		gitOut(t, l.Cwd, "merge", "--quiet", "--no-edit", "origin/main")
		os.WriteFile(filepath.Join(l.Cwd, "qa-scratch.log"), []byte("tmp\n"), 0o644)
		if out, err := gitOutErr(l.Cwd, "push", h.remote, "HEAD:refs/heads/af/cls"); err == nil || !strings.Contains(out, "this is a review copy; your result is the verdict") {
			t.Errorf("review copy pushed: %v %s", err, out)
		}
		merged = gitOut(t, l.Cwd, "rev-parse", "HEAD")
		h.exit(t, r2.ID, "handoff", map[string]any{"verdict": "changes_requested", "feedback": "fails with main", "note": "merged main locally to test"}, merged)
	})
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Fatal("review copy kept", err)
	}
	if _, err := os.Stat(filepath.Join(h.bRoot, storage.DefaultCompany, "app", "tasks", "cls")); !os.IsNotExist(err) {
		t.Fatal("the review made a task worktree on B", err)
	}
	ws, err := h.s.TaskWorkspace(ctx, co, "cls")
	if err != nil || ws.NodeID != h.nodeA || ws.Unpushed || ws.ExitSHA == merged || ws.RunID != r1.ID || ws.SealedHead != pushed {
		t.Fatalf("workspace changed by the review: %+v %v (pushed %s)", ws, err, pushed)
	}
	if got := gitOut(t, h.remote, "rev-parse", "af/cls"); got != pushed {
		t.Fatal("task branch moved", got)
	}

	r3 := h.next(t, "cls")
	if r3.Agent != "dev" || r3.ReviewCopy {
		t.Fatalf("rework run %s review_copy=%v", r3.Agent, r3.ReviewCopy)
	}
	if q, _ := h.s.QueuedWhy(ctx, co, r3.ID); q != nil && q.Tie != nil && (q.Tie.Reason == tasks.TieWork || q.Tie.Node == h.nodeB) {
		t.Fatalf("rework tied: %+v", q.Tie)
	}
	// A becomes unavailable; once dev's session tie to A is released, B (free) takes the rework.
	if err = h.s.SetPause(ctx, co, storage.Pause{Scope: "node", Target: h.nodeA, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if r := h.run(t, h.b, r3.ID, nil); r == nil || r.Reason != "affinity" {
		t.Fatalf("B claimed before the session tie was released: %+v", r)
	}
	q, _ := h.s.QueuedWhy(ctx, co, r3.ID)
	if q == nil || q.Tie == nil || q.Tie.Reason != tasks.TieSession || q.Tie.Node != h.nodeA || q.Tie.ReleaseAt == 0 || !strings.Contains(q.Detail, "its agent session is there") {
		t.Fatalf("session tie %+v", q)
	}
	h.fake.Advance(storage.DefaultSessionAffinity + time.Minute)
	ran := false
	if r := h.run(t, h.b, r3.ID, func(l driver.Launch) {
		ran = true
		if head := gitOut(t, l.Cwd, "rev-parse", "HEAD"); head != pushed {
			t.Errorf("rework starts at %s, want the pushed head %s", head, pushed)
		}
		if st := gitOut(t, l.Cwd, "status", "--porcelain"); st != "" {
			t.Errorf("rework worktree not clean: %q", st)
		}
		for _, f := range []string{"main.txt", "qa-scratch.log"} {
			if _, err := os.Stat(filepath.Join(l.Cwd, f)); !os.IsNotExist(err) {
				t.Errorf("reviewer's %s in the implementer's worktree", f)
			}
		}
	}); r != nil || !ran {
		t.Fatalf("B did not take the rework: %+v", r)
	}
}

func gitOutErr(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
