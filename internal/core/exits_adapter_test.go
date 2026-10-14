package core

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/factorytools"
	"appfactory/internal/storage"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestAdapterAgainstCore runs the real MCP adapter against Core with explicit exits on: a dirty tree is
// refused on the node, a clean submit pushes the branch and opens the pull request (fake gh), Core records
// the facts and the commit; the reviewer's approval returns it; done is refused until the PR is merged.
func TestAdapterAgainstCore(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	server := httptest.NewServer(h.c.Handler())
	defer server.Close()

	base, _ := filepath.EvalSymlinks(t.TempDir())
	gc := filepath.Join(base, "gitconfig")
	_ = os.WriteFile(gc, []byte("[user]\n\tname = T\n\temail = t@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	remote, work := filepath.Join(base, "remote.git"), filepath.Join(base, "work")
	gitIn(t, base, "init", "--bare", "-q", remote)
	gitIn(t, base, "clone", "-q", remote, work)
	_ = os.WriteFile(filepath.Join(work, "a.txt"), []byte("a\n"), 0o644)
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-qm", "init")
	gitIn(t, work, "push", "-q", "origin", "main")
	gitIn(t, work, "checkout", "-qb", "af/t")
	_ = os.WriteFile(filepath.Join(work, "login.swift"), []byte("// login\n"), 0o644)
	gitIn(t, work, "add", "-A")
	gitIn(t, work, "commit", "-qm", "login screen")
	bin := filepath.Join(base, "bin")
	_ = os.MkdirAll(bin, 0o755)
	state := filepath.Join(base, "pr-state")
	_ = os.WriteFile(filepath.Join(bin, "gh"), []byte(`#!/bin/sh
if [ "$1 $2" = "pr view" ]; then
  [ -f `+state+` ] || exit 1
  echo "{\"number\": 9, \"state\": \"$(cat `+state+`)\", \"headRefOid\": \"$(git rev-parse HEAD)\", \"url\": \"u\"}"
  exit 0
fi
if [ "$1 $2" = "pr create" ]; then echo OPEN > `+state+`; exit 0; fi
exit 1
`), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	h.create("t", "code", "demo")
	r := h.start("t")
	adapter := func(token string) *factorytools.Adapter {
		return &factorytools.Adapter{URL: server.URL, Token: token, Workdir: work, DefaultBranch: "main"}
	}
	a := adapter(r.token)
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_project_get", Arguments: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	a.Delivered()
	_ = os.WriteFile(filepath.Join(work, "scratch.txt"), []byte("x"), 0o644)
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_exit_submit", Arguments: map[string]any{"summary": "login"}}); err == nil || !strings.Contains(err.Error(), "dirty_tree") {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(work, "scratch.txt"))
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_exit_submit", Arguments: map[string]any{"summary": "login", "reviewer": "reviewer"}}); err != nil {
		t.Fatal(err)
	}
	head := gitIn(t, work, "rev-parse", "HEAD")
	got := h.task("t")
	if got.Status != "IN_REVIEW" || got.Submission == nil || got.Submission.Commit != head || got.Assignee != "reviewer" {
		t.Fatalf("%+v", got)
	}
	var facts string
	_ = h.s.DB.QueryRow("SELECT facts FROM run_exits WHERE run_id=?", r.id).Scan(&facts)
	if !strings.Contains(facts, `"number":9`) || !strings.Contains(facts, `"pushed":true`) {
		t.Fatal(facts)
	}
	calls, _ := h.s.ToolCalls(ctx, storage.DefaultCompany, r.id)
	audited := false
	for _, call := range calls {
		audited = audited || call.Tool == "factory_exit_submit" && call.Status == 200
	}
	if !audited {
		t.Fatalf("exit not audited: %+v", calls)
	}
	h.end(r, "COMPLETED", "accepted")

	rv := h.start("t")
	if _, err := adapter(rv.token).Call(ctx, factorytools.Call{Name: "factory_exit_handoff", Arguments: map[string]any{"to": "demo", "note": "ok", "verdict": "approved"}}); err != nil {
		t.Fatal(err)
	}
	var approved string
	_ = h.s.DB.QueryRow("SELECT approved_head FROM pr_approvals WHERE task_id='t' AND pr_number=9").Scan(&approved)
	if approved != head {
		t.Fatal("approval not bound to the PR head:", approved)
	}
	h.end(rv, "COMPLETED", "accepted")

	r = h.start("t")
	a = adapter(r.token)
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "shipped"}}); err == nil || !strings.Contains(err.Error(), "pr_not_merged") {
		t.Fatal(err)
	}
	_ = os.WriteFile(state, []byte("MERGED"), 0o644)
	if _, err := a.Call(ctx, factorytools.Call{Name: "factory_exit_done", Arguments: map[string]any{"summary": "shipped"}}); err != nil {
		t.Fatal(err)
	}
	if h.task("t").Status != "DONE" {
		t.Fatal(h.task("t"))
	}
	_ = storage.DefaultCompany
}
