package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestWorkTies (node-ties 1.1, 2.2): work saved remotely does not tie the task and is named in the next claim
// with the node that saved it; unsaved work ties it with the reason, since the first report of the tie; task views
// and queued runs carry the tie; reset_workspace releases it.
func TestWorkTies(t *testing.T) {
	s, fake := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/ties-app", RepoURL: "https://example.com/app.git"}); err != nil {
		t.Fatal(err)
	}
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Worktrees = true })
	register(t, s, "n2", 1, func(r *NodeRegistration) { r.Worktrees = true })
	snapshot := []byte(`{"spec":{"workspace":{"mode":"worktree"}}}`)
	if _, err := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"}); err != nil {
		t.Fatal(err)
	}
	n := 0
	runOn := func(node string, w tasks.Workspace) (Claimed, tasks.Workspace) {
		t.Helper()
		n++
		v, _ := s.Task(ctx, co, "t")
		spec, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r" + string(rune('0'+n)), Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		c, err := claimV2(s, spec.ID, node, 1, "c-"+spec.ID)
		if err != nil {
			t.Fatalf("claim on %s: %v", node, err)
		}
		w.Mode, w.Branch, w.NodeID = "worktree", "af/t", node
		got, err := s.ReportWorkspace(ctx, co, spec.ID, c.Lease, w)
		if err != nil {
			t.Fatal(err)
		}
		if err = end(t, s, spec.ID, c.Lease, protocol.Event{Status: "ENDED", Outcome: "failed"}); err != nil {
			t.Fatal(err)
		}
		fake.Advance(time.Hour)
		return c, got
	}
	saved := strings.Repeat("a", 40)
	// Saved remotely: the report's unpushed (local work) does not tie the task.
	if _, w := runOn("n1", tasks.Workspace{Unpushed: true, WIPSHA: saved}); w.Unpushed || w.WIPSHA != saved {
		t.Fatalf("%+v", w)
	}
	snap, _ := s.TaskSnapshot(ctx, co, "t")
	if v := tasks.Evaluate(withAgent(snap)); v.Tie != nil {
		t.Fatal("tied after the save", v.Tie)
	}
	// The next run, on the other node, is told what to restore and where the task ran last.
	c, w := runOn("n2", tasks.Workspace{Unpushed: true, WIPUnsaved: "secret finding: aws key in config.env"})
	if c.RunSpec.RestoreWIP != saved || c.RunSpec.WorkspaceNode != "n1" {
		t.Fatalf("claim answer restore_wip %q workspace_node %q", c.RunSpec.RestoreWIP, c.RunSpec.WorkspaceNode)
	}
	since := fake.Now().Add(-time.Hour).Unix()
	if !w.Unpushed || w.WIPSHA != "" || w.TiedSince != since {
		t.Fatalf("%+v (since %d)", w, since)
	}
	// Still unsaved on the same node: the tie keeps its start.
	w, err := s.ReportWorkspace(ctx, co, c.RunSpec.ID, c.Lease, tasks.Workspace{Mode: "worktree", Branch: "af/t", NodeID: "n2", Unpushed: true, WIPUnsaved: "push failed: no network"})
	if err != nil || w.TiedSince != since || w.WIPUnsaved != "push failed: no network" {
		t.Fatalf("%+v %v", w, err)
	}
	snap, _ = s.TaskSnapshot(ctx, co, "t")
	v := tasks.Evaluate(withAgent(snap))
	if v.Tie == nil || v.Tie.Node != "n2" || v.Tie.Reason != tasks.TieWork || v.Tie.Since != since || v.Tie.Unsaved != "push failed: no network" || v.Tie.Release != tasks.WorkRelease {
		t.Fatalf("verdict tie %+v", v.Tie)
	}
	vt, _ := s.Task(ctx, co, "t")
	spec, err := s.Dispatch(ctx, co, vt.ID, vt.Version, protocol.RunSpec{ID: "rq", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	refused(t, func() error { _, err := claimV2(s, spec.ID, "n1", 1, "c-rq"); return err }(), true, "pinned")
	q, err := s.QueuedWhy(ctx, co, spec.ID)
	if err != nil || q.Tie == nil || q.Tie.Node != "n2" || q.Tie.Reason != tasks.TieWork || !strings.HasPrefix(q.Detail, "tied to mini-n2 (n2): its unsaved work is there (it could not be saved: push failed: no network)") {
		t.Fatalf("queued %+v %v", q, err)
	}
	tied, _ := s.NodesTied(ctx, co)
	if got := tied["n2"]; len(got.Work) != 1 || len(got.Waiting) != 1 || got.Waiting[0].Reason != tasks.TieWork || got.Waiting[0].Tie != "pinned" {
		t.Fatalf("nodes tied %+v", got)
	}
}

// TestReviewCopyRuns (node-ties 3.1, 3.2): a run of an agent that is not the task's implementer is a review copy
// (with the project's review_carry); its workspace report changes nothing and ties nothing.
func TestReviewCopyRuns(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/review-app", RepoURL: "https://example.com/app.git"}); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Project(ctx, co, "app")
	for _, bad := range [][]string{{"../up"}, {"/abs"}, {".git"}, {""}} {
		if _, err := s.UpdateProject(ctx, co, "app", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, ReviewCarry: &bad}); err == nil {
			t.Fatalf("review_carry %q accepted", bad)
		}
	}
	carry := []string{".cache", "Library/Caches"}
	p, err := s.UpdateProject(ctx, co, "app", tasks.ProjectUpdate{Version: p.Version, Name: p.Name, ReviewCarry: &carry})
	if err != nil || len(p.ReviewCarry) != 2 {
		t.Fatal(p.ReviewCarry, err)
	}
	register(t, s, "n1", 1, func(r *NodeRegistration) { r.Worktrees = true })
	register(t, s, "n2", 1, func(r *NodeRegistration) { r.Worktrees = true })
	snapshot := []byte(`{"spec":{"workspace":{"mode":"worktree"}}}`)
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"}); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Task(ctx, co, "t")
	spec, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
	if err != nil || spec.ReviewCopy {
		t.Fatalf("implementer run review_copy=%v %v", spec.ReviewCopy, err)
	}
	c, err := claimV2(s, spec.ID, "n1", 1, "c1")
	if err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("b", 40)
	if _, err = s.ReportWorkspace(ctx, co, spec.ID, c.Lease, tasks.Workspace{Mode: "worktree", Branch: "af/t", NodeID: "n1", SealedHead: head}); err != nil {
		t.Fatal(err)
	}
	var ack int64
	_ = s.DB.QueryRowContext(ctx, "SELECT wake_seq FROM tasks WHERE company_id=$1 AND id=$2", co, "t").Scan(&ack)
	facts := &ExitFacts{Head: head, Branch: "af/t", DefaultBranch: "main", AheadOfDefault: 1, Pushed: true, Scan: &ScanFacts{OK: true}}
	if _, err = s.Exit(ctx, co, spec.ID, ExitRequest{Kind: "submit", Arguments: map[string]any{"summary": "s", "reviewer": "qa"}, AckSeq: ack, Facts: facts}); err != nil {
		t.Fatal(err)
	}
	if err = end(t, s, spec.ID, c.Lease, protocol.Event{Status: "ENDED", Outcome: "succeeded"}); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Task(ctx, co, "t")
	rv, err := s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r2", Project: "app", Agent: "qa", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot})
	if err != nil || !rv.ReviewCopy || len(rv.ReviewCarry) != 2 {
		t.Fatalf("review run review_copy=%v carry=%v %v", rv.ReviewCopy, rv.ReviewCarry, err)
	}
	c, err = claimV2(s, rv.ID, "n2", 1, "c2")
	if err != nil || c.RunSpec.RestoreWIP != "" {
		t.Fatal(c.RunSpec.RestoreWIP, err)
	}
	merged := strings.Repeat("c", 40)
	if _, err = s.ReportWorkspace(ctx, co, rv.ID, c.Lease, tasks.Workspace{Mode: "worktree", Branch: "af/t", NodeID: "n2", SealedHead: merged, Unpushed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Exit(ctx, co, rv.ID, ExitRequest{Kind: "handoff", Arguments: map[string]any{"verdict": "changes_requested", "feedback": "f", "note": "n"}, AckSeq: ack + 100, Facts: &ExitFacts{Head: merged, Branch: "af/t", DefaultBranch: "main", Pushed: false, Scan: &ScanFacts{OK: true}}}); err != nil {
		t.Fatal(err)
	}
	w, err := s.TaskWorkspace(ctx, co, "t")
	if err != nil || w.NodeID != "n1" || w.Unpushed || w.SealedHead != head || w.ExitSHA != head || w.RunID != "r1" {
		t.Fatalf("workspace changed by the review copy: %+v %v", w, err)
	}
}
