package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func clockStore(t *testing.T) (*Store, *clock.Fake) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	return s, fake
}

func TestRunUsageKeepsTheLastReport(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if err := s.Create(ctx, co, protocol.RunSpec{ID: "r", Project: "p", Agent: "a", Workspace: "/tmp/usage"}); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, co, "r")
	if err != nil {
		t.Fatal(err)
	}
	send := func(p protocol.Event) {
		t.Helper()
		p.RunID, p.Lease = "r", lease
		if err := s.Apply(ctx, co, protocol.Wrap("agent.progress", "r", "", p)); err != nil {
			t.Fatal(err)
		}
	}
	send(protocol.Event{Usage: []protocol.Usage{{Model: "m", Input: 10, Output: 1}}})
	send(protocol.Event{Method: "x"}) // no usage: keeps the stored one
	send(protocol.Event{Status: "CANCELLED", Usage: []protocol.Usage{{Model: "m", Input: 100, Output: 7, CacheRead: 50}, {Model: "small", Input: 3, Output: 1, CostUSD: 0.01, CostSource: "claude_estimate"}}})
	r, err := s.Get(ctx, co, "r")
	if err != nil || len(r.Usage) != 2 || r.Usage[0].Input != 100 || r.Usage[1].CostSource != "claude_estimate" {
		t.Fatalf("%+v %v", r.Usage, err)
	}
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), `"usage":[{"model":"m"`) {
		t.Fatal(string(b))
	}
}

func TestQuotaPausesTheKeyUntilReset(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/quota"}); err != nil {
		t.Fatal(err)
	}
	maxKey := json.RawMessage(`{"spec":{"runtime":{"quota_key":"claude-max-1"}}}`)
	if err := s.Create(ctx, co, protocol.RunSpec{ID: "hit", Project: "p", Agent: "dev", Workspace: "/tmp/quota", ConfigSnapshot: maxKey}); err != nil {
		t.Fatal(err)
	}
	lease, _ := s.Claim(ctx, co, "hit")
	reset := fake.Now().Add(2 * time.Hour)
	quota := func(at time.Time) {
		t.Helper()
		if err := s.Apply(ctx, co, protocol.Wrap("agent.progress", "hit", "", protocol.Event{RunID: "hit", Lease: lease, Quota: &protocol.Quota{ResetsAt: at, Type: "five_hour"}})); err != nil {
			t.Fatal(err)
		}
	}
	quota(reset)
	quota(reset)                 // runtimes may report one hit twice
	quota(reset.Add(-time.Hour)) // an earlier reset never shortens the pause
	ps, _ := s.Pauses(ctx, co)
	if len(ps) != 1 || ps[0].Scope != "quota_key" || ps[0].Target != "claude-max-1" || ps[0].Until != reset.Unix() || ps[0].Actor != "runtime" {
		t.Fatalf("%+v", ps)
	}
	task := func(id, agent string) tasks.Task {
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: agent, Ready: true})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	same := task("same-key", "reviewer")
	if _, err := s.Dispatch(ctx, co, "same-key", same.Version, protocol.RunSpec{ID: "d1", Project: "p", Agent: "reviewer", ConfigSnapshot: maxKey}); !errors.Is(err, ErrConflict) {
		t.Fatal("dispatched on a paused quota key", err)
	}
	snap, _ := s.TaskSnapshot(ctx, co, "same-key")
	if len(snap.QuotaPaused) != 1 || snap.QuotaPaused[0] != "claude-max-1" {
		t.Fatal(snap.QuotaPaused)
	}
	// The run that hit the limit ends (it holds the project's writer slot).
	if err := s.Apply(ctx, co, protocol.Wrap("agent.completed", "hit", "", protocol.Event{RunID: "hit", Lease: lease, Status: "FAILED"})); err != nil {
		t.Fatal(err)
	}
	fake.Advance(2*time.Hour + time.Second)
	if err := s.ExpirePauses(ctx, co); err != nil {
		t.Fatal(err)
	}
	if ps, _ = s.Pauses(ctx, co); len(ps) != 0 {
		t.Fatal("quota pause did not expire", ps)
	}
	if _, err := s.Dispatch(ctx, co, "same-key", same.Version, protocol.RunSpec{ID: "d2", Project: "p", Agent: "reviewer", Provider: protocol.RuntimeCodex, ConfigSnapshot: maxKey}); err != nil {
		t.Fatal("not dispatched after the reset", err)
	}
	page, _ := s.Feed(ctx, co, FeedFilter{}, 0, 100)
	kinds := map[string]bool{}
	for _, item := range page.Items {
		kinds[item.Type] = kinds[item.Type] || item.Attention
	}
	if !kinds["quota.paused"] {
		t.Fatal("no attention event for the quota pause", kinds)
	}
	paused := 0
	for _, item := range page.Items {
		if item.Type == "quota.paused" {
			paused++
		}
	}
	if paused != 1 {
		t.Fatal("a repeated limit report raised another event", paused)
	}
	if _, ok := kinds["owner.resumed"]; !ok {
		t.Fatal("expiry not in the feed", kinds)
	}
	if QuotaKey([]byte(`{}`), "dev") != "dev" || QuotaKey(maxKey, "dev") != "claude-max-1" ||
		QuotaKey([]byte(`{"spec":{"runtime":{"provider":"codex","default_quota_key":"codex-team"}}}`), "dev") != "codex-team" ||
		QuotaKey([]byte(`{"spec":{"runtime":{"provider":"codex"}}}`), "dev") != "codex-default" {
		t.Fatal("quota key default")
	}
}

func TestRepoFieldsReachTheRunSpec(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	for _, bad := range []tasks.ProjectCreate{
		{ID: "b1", Name: "b", Workspace: "/tmp/b1", RepoURL: "not a url"},
		{ID: "b2", Name: "b", Workspace: "/tmp/b2", DefaultBranch: "main"},
		{ID: "b3", Name: "b", Workspace: "/tmp/b3", RepoURL: "git@github.com:x/y.git", DefaultBranch: "../evil"},
	} {
		if _, err := s.CreateProject(ctx, co, bad); !errors.Is(err, ErrInput) {
			t.Fatal("invalid repo accepted", bad, err)
		}
	}
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "r", Name: "R", Workspace: "/tmp/repo-r", RepoURL: "git@github.com:acme/app.git", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "plain", Name: "Plain", Workspace: "/tmp/repo-plain"}); err != nil {
		t.Fatal(err)
	}
	worktree := json.RawMessage(`{"spec":{"workspace":{"mode":"worktree"}}}`)
	dispatch := func(project, agent string, snapshot json.RawMessage) protocol.RunSpec {
		t.Helper()
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: project + "-" + agent, ProjectID: project, Title: "t", Assignee: agent, Ready: true})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: v.ID + "-run", Project: project, Agent: agent, Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		return spec
	}
	if spec := dispatch("r", "a1", worktree); spec.RepoURL != "git@github.com:acme/app.git" || spec.DefaultBranch != "main" || spec.WorkspaceMode != "worktree" {
		t.Fatalf("%+v", spec)
	}
	if spec := dispatch("plain", "a2", worktree); spec.RepoURL != "" || spec.WorkspaceMode != "" {
		t.Fatalf("worktree without a repository: %+v", spec)
	}
	p, _ := s.Project(ctx, co, "r")
	clear := ""
	if p, err := s.UpdateProject(ctx, co, "r", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, RepoURL: &clear, DefaultBranch: &clear}); err != nil || p.RepoURL != "" {
		t.Fatal(p, err)
	}
}

func TestWorkspaceReportsUseTheRunLease(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	run := dispatchEngineTask(t, s, readyEngineTask(t, s, createEngineTask(t, s, "a")), "a1")
	lease, err := s.Claim(ctx, co, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("ab", 20)
	if _, err = s.ReportWorkspace(ctx, co, run.ID, "wrong", tasks.Workspace{Mode: "worktree"}); !errors.Is(err, ErrConflict) {
		t.Fatal("wrong lease accepted", err)
	}
	if _, err = s.ReportWorkspace(ctx, co, run.ID, lease, tasks.Workspace{Mode: "worktree", SealedHead: "abc"}); !errors.Is(err, ErrInput) {
		t.Fatal("short sha accepted", err)
	}
	if _, err = s.ReportWorkspace(ctx, co, run.ID, lease, tasks.Workspace{Mode: "worktree", Branch: "af/a-login", NodeID: "mini-1"}); err != nil {
		t.Fatal(err)
	}
	finishWith := protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: "COMPLETED"})
	if err = s.Apply(ctx, co, finishWith); err != nil {
		t.Fatal(err)
	}
	// The seal is reported after the run ended; the lease still authenticates it.
	if _, err = s.ReportWorkspace(ctx, co, run.ID, lease, tasks.Workspace{Mode: "worktree", Branch: "af/a-login", NodeID: "mini-1", SealedHead: sha, SealedAt: 42}); err != nil {
		t.Fatal(err)
	}
	w, err := s.TaskWorkspace(ctx, co, "a")
	if err != nil || w.SealedHead != sha || w.Branch != "af/a-login" || w.RunID != run.ID {
		t.Fatal(w, err)
	}
	if _, err = s.TaskWorkspace(ctx, co, "none"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
