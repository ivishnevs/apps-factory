package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestParallelWorktreeTasks: two worktree tasks of one project dispatch together and run in parallel on a
// node with worktrees; on a node without worktrees (or a v1 worker) they share the project workspace and
// run one at a time (retry project_busy); a shared-mode task always holds the project workspace.
func TestParallelWorktreeTasks(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/parallel-app", RepoURL: "https://example.com/app.git"}); err != nil {
		t.Fatal(err)
	}
	worktree := []byte(`{"spec":{"workspace":{"mode":"worktree"}}}`)
	dispatch := func(task, agent string, snapshot []byte) protocol.RunSpec {
		t.Helper()
		v, err := s.CreateTask(ctx, co, tasks.Create{ID: task, ProjectID: "app", Title: task, Assignee: agent, Ready: true, RequestID: "c-" + task})
		if err != nil {
			t.Fatal(err)
		}
		spec, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r-" + task, Project: "app", Agent: agent, Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
		if err != nil {
			t.Fatalf("dispatch %s: %v", task, err)
		}
		return spec
	}
	a := dispatch("a", "dev-a", worktree)
	b := dispatch("b", "dev-b", worktree)
	if a.WorkspaceMode != "worktree" || b.WorkspaceMode != "worktree" {
		t.Fatal(a.WorkspaceMode, b.WorkspaceMode)
	}
	register(t, s, "wt", 1, func(r *NodeRegistration) { r.Worktrees = true; r.Slots = 4 })
	if _, err := claimV2(s, a.ID, "wt", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := claimV2(s, b.ID, "wt", 1, "1-1-2"); err != nil {
		t.Fatal("second task of the project did not run in parallel:", err)
	}

	// A node without worktrees runs both in the project workspace: one at a time.
	c := dispatch("c", "dev-c", worktree)
	d := dispatch("d", "dev-d", worktree)
	register(t, s, "plain", 1, func(r *NodeRegistration) { r.Slots = 4 })
	if _, err := claimV2(s, c.ID, "plain", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, d.ID, "plain", 1, "1-1-2"); return err }(), true, "project_busy")
	// A v1 worker is treated the same way.
	if _, err := s.Claim(ctx, co, d.ID); !errors.Is(err, ErrConflict) {
		t.Fatal("v1 claim shared the project workspace", err)
	}
	// The worktree node still takes it.
	if _, err := claimV2(s, d.ID, "wt", 1, "1-2-3"); err != nil {
		t.Fatal(err)
	}
	// Shared mode always holds the project workspace: it waits while run c uses it.
	v, err := s.CreateTask(ctx, co, tasks.Create{ID: "e", ProjectID: "app", Title: "e", Assignee: "dev-e", Ready: true, RequestID: "c-e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r-e", Project: "app", Agent: "dev-e", Provider: protocol.RuntimeCodex}); !errors.Is(err, ErrConflict) {
		t.Fatal("shared task dispatched while the project workspace is in use", err)
	}
}

// TestPinnedTask: unpushed work pins the task to its node; another node waits (retry pinned). When the node
// stays offline past PinTimeout the task escalates pinned_node_unavailable, and reset_workspace unpins it.
func TestPinnedTask(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/pin-app", RepoURL: "https://example.com/app.git"}); err != nil {
		t.Fatal(err)
	}
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Worktrees = true })
	register(t, s, "n2", 1, func(r *NodeRegistration) { r.Worktrees = true })
	v, _ := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"})
	first, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: []byte(`{"spec":{"workspace":{"mode":"worktree"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	c, err := claimV2(s, first.ID, "n1", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReportWorkspace(ctx, co, first.ID, c.Lease, tasks.Workspace{Mode: "worktree", Branch: "af/t", NodeID: "n1", Unpushed: true}); err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, first.ID, c.Lease, protocol.Event{Status: "FAILED", Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour) // past the retry backoff
	v, _ = s.Task(ctx, co, "t")
	second, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r2", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: []byte(`{"spec":{"workspace":{"mode":"worktree"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, second.ID, "n2", 1, "1-0-2"); return err }(), true, "pinned")
	if want, _ := s.PinnedUnavailable(ctx, co, nil); len(want) != 0 {
		t.Fatal("n1 is online", want)
	}
	// n1 goes silent.
	fake.Advance(NodeOffline + time.Second)
	_ = s.ExpireNodes(ctx, co)
	fake.Advance(PinTimeout)
	want, err := s.PinnedUnavailable(ctx, co, nil)
	if err != nil || len(want) != 1 || want[0].TaskID != "t" || want[0].RefID != "n1" {
		t.Fatal(want, err)
	}
	if err = s.SyncEscalations(ctx, co, "pinned_node_unavailable", want); err != nil {
		t.Fatal(err)
	}
	open, _ := s.Escalations(ctx, co, true)
	q, _ := s.Message(ctx, co, open[0].MessageID)
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "go", Answers: map[string]any{"answer": "reset_workspace"}, RequestID: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err = claimV2(s, second.ID, "n2", 1, "1-0-3"); err != nil {
		t.Fatal("reset_workspace did not unpin", err)
	}
}

// TestWorkspaceDiverged: a diverged worktree escalates instead of retrying; reset_workspace asks the node to
// reset (heartbeat reset) and the task runs again once the node confirms (reset_done).
func TestWorkspaceDiverged(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/div-app", RepoURL: "https://example.com/app.git"}); err != nil {
		t.Fatal(err)
	}
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Worktrees = true })
	v, _ := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"})
	spec, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: []byte(`{"spec":{"workspace":{"mode":"worktree"}}}`)})
	if err != nil {
		t.Fatal(err)
	}
	c, err := claimV2(s, spec.ID, "n1", 1, "1-0-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReportWorkspace(ctx, co, spec.ID, c.Lease, tasks.Workspace{Mode: "worktree", Branch: "af/t", NodeID: "n1"}); err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, spec.ID, c.Lease, protocol.Event{Status: "FAILED", Outcome: "setup_failed", Result: "workspace_diverged: task t (branch af/t): HEAD changed outside any run"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Task(ctx, co, "t")
	open, _ := s.Escalations(ctx, co, true)
	reason := func() string {
		snap, _ := s.TaskSnapshot(ctx, co, "t")
		return tasks.Evaluate(withAgent(snap)).Reason
	}
	if got.Status != "BLOCKED" || !strings.HasPrefix(got.BlockReason, "escalation: workspace_diverged: ") || len(open) != 1 || open[0].Reason != "workspace_diverged" || reason() != tasks.ReasonParked+tasks.ParkEscalation {
		t.Fatalf("%+v %+v %s", got, open, reason())
	}
	q, _ := s.Message(ctx, co, open[0].MessageID)
	if _, err = s.ReplyMessage(ctx, co, q.ID, messaging.Reply{Version: q.Version, Sender: "owner", Body: "reset", Answers: map[string]any{"answer": "reset_workspace"}, RequestID: "r"}); err != nil {
		t.Fatal(err)
	}
	if r := reason(); r != tasks.ReasonParked+tasks.ParkBlock {
		t.Fatal("ran before the node reset the worktree", r)
	}
	a, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 1})
	if err != nil || len(a.Reset) != 1 || a.Reset[0] != "t" {
		t.Fatal(a, err)
	}
	if a, err = s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 1, ResetDone: []string{"t"}}); err != nil || len(a.Reset) != 0 {
		t.Fatal(a, err)
	}
	if got, _ = s.Task(ctx, co, "t"); got.Status != "READY" || got.NotBefore != 0 || strings.HasPrefix(reason(), tasks.ReasonParked) {
		t.Fatal(got.Status, reason())
	}
}
