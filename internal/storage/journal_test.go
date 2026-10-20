package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"appfactory/internal/artifacts"
	"appfactory/internal/decisions"
	"appfactory/internal/protocol"
	"appfactory/internal/reports"
	"appfactory/internal/storage/pgtest"
	"appfactory/internal/tasks"
)

func TestReportsDurabilityIdentityAndRuntime(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "reporter"))
	run := dispatchEngineTask(t, s, task, "report-run")
	in := reports.Create{TaskID: task.ID, RunID: run.ID, Actor: "demo", Kind: "environment_change", Summary: "Updated test configuration", Purpose: "Enable verification", Changes: []string{"test config"}, Result: "checks passed", NeedsOwnerAction: true, OwnerAction: "Review configuration", RequestID: "report"}
	report, err := s.CreateReport(ctx, DefaultCompany, "p", in)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateReport(ctx, DefaultCompany, "p", in)
	if err != nil || replay.ID != report.ID {
		t.Fatal(replay, err)
	}
	in.Result = "different"
	if _, err = s.CreateReport(ctx, DefaultCompany, "p", in); !errors.Is(err, ErrConflict) {
		t.Fatal("changed replay", err)
	}
	inbox, err := s.Inbox(ctx, DefaultCompany, "p")
	if err != nil || len(inbox) != 1 || inbox[0].ReportID != report.ID || inbox[0].Blocking {
		t.Fatal(inbox, err)
	}
	current, _ := s.Task(ctx, DefaultCompany, task.ID)
	if current.WaitMessageID != "" || current.Status != "IN_PROGRESS" {
		t.Fatal(current)
	}
	in.RequestID = "wrong-project"
	if _, err = s.CreateReport(ctx, DefaultCompany, "other", in); err == nil {
		t.Fatal("cross project report")
	}
	in.Kind = "runtime"
	in.RequestID = "forged-core"
	if _, err = s.CreateReport(ctx, DefaultCompany, "p", in); err == nil {
		t.Fatal("forged runtime report")
	}
	event := finishEngineRun(t, s, run, "failed")
	if err = s.Apply(ctx, DefaultCompany, event); err != nil {
		t.Fatal(err)
	}
	// The runtime report records the end; a failed run retries on its own, so it asks nothing of the owner.
	page, err := s.Reports(ctx, DefaultCompany, "p", "runtime", "", run.ID, 0, 10)
	if err != nil || len(page.Items) != 1 || page.Items[0].Result != "failed" || page.Items[0].Source != "core" || page.Items[0].NeedsOwnerAction {
		t.Fatal(page, err)
	}
	persisted, err := s.Report(ctx, DefaultCompany, report.ID)
	if err != nil || persisted.Result != "checks passed" {
		t.Fatal(persisted, err)
	}
	// Lightweight event pagination loses or repeats no event, including links in message snapshots.
	var cursor int64
	seen := map[string]bool{}
	for {
		p, e := s.ProjectFeed(ctx, DefaultCompany, "p", cursor, 2)
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range p.Items {
			if seen[v.EventID] {
				t.Fatal("repeated event")
			}
			seen[v.EventID] = true
		}
		cursor = p.NextAfter
		if !p.HasMore {
			break
		}
	}
	if len(seen) < 6 {
		t.Fatal(seen)
	}
}

func TestDecisionsReplacementConcurrencyAndCapturedContext(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	propose := func(key, value string) decisions.Decision {
		d, e := s.ProposeDecision(ctx, DefaultCompany, "p", decisions.Propose{Topic: "  Storage   Choice ", Value: value, Rationale: "durability requirements", Actor: "demo", RequestID: key})
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	first := propose("first", "SQLite")
	record := decisions.Record{Actor: "owner", Version: 1, Status: "approved", Rationale: "Single Core owns state", RequestID: "accept"}
	// Neither an agent assertion nor reading the proposal approves it.
	forged := record
	forged.Actor = "demo"
	if _, e := s.RecordDecision(ctx, DefaultCompany, first.ID, forged); e == nil {
		t.Fatal("agent approved itself")
	}
	before, _ := s.ProjectContext(ctx, DefaultCompany, "p")
	if len(before.Decisions) != 0 {
		t.Fatal(before)
	}
	approved, err := s.RecordDecision(ctx, DefaultCompany, first.ID, record)
	if err != nil || approved.Version != 2 || approved.Topic != "storage choice" {
		t.Fatal(approved, err)
	}
	if _, err = s.RecordDecision(ctx, DefaultCompany, first.ID, record); err != nil {
		t.Fatal("idempotent replay", err)
	}
	oldRun := protocol.RunSpec{ID: "old-context", Project: "p", Agent: "demo", Workspace: "/tmp/task-engine-p"}
	if err = s.Create(ctx, DefaultCompany, oldRun); err != nil {
		t.Fatal(err)
	}
	second := propose("second", "PostgreSQL")
	third := propose("third", "Other")
	attempts := []decisions.Decision{second, third}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i, d := range attempts {
		wg.Add(1)
		go func(i int, d decisions.Decision) {
			defer wg.Done()
			_, e := s.RecordDecision(ctx, DefaultCompany, d.ID, decisions.Record{Actor: "owner", Version: 1, Status: "approved", Rationale: "Changed requirements", RequestID: fmt.Sprint("replace-", i), SupersedesID: first.ID, SupersedesVersion: approved.Version})
			results <- e
		}(i, d)
	}
	wg.Wait()
	close(results)
	ok, conflicts := 0, 0
	for e := range results {
		if e == nil {
			ok++
		} else if errors.Is(e, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(e)
		}
	}
	if ok != 1 || conflicts != 1 {
		t.Fatal(ok, conflicts)
	}
	current, _ := s.ProjectContext(ctx, DefaultCompany, "p")
	if len(current.Decisions) != 1 || current.Decisions[0].ID == first.ID {
		t.Fatal(current)
	}
	old, _ := s.Get(ctx, DefaultCompany, oldRun.ID)
	if len(old.Spec.DecisionContext) != 1 || old.Spec.DecisionContext[0].Value != "SQLite" {
		t.Fatal(old)
	}
	finishEngineRun(t, s, old.Spec, "succeeded")
	next := oldRun
	next.ID = "new-context"
	if err = s.Create(ctx, DefaultCompany, next); err != nil {
		t.Fatal(err)
	}
	newer, _ := s.Get(ctx, DefaultCompany, next.ID)
	if newer.Spec.DecisionContext[0].ID != current.Decisions[0].ID {
		t.Fatal(newer)
	}
	superseded, _ := s.Decision(ctx, DefaultCompany, first.ID)
	if superseded.Status != "superseded" || superseded.SupersededBy != current.Decisions[0].ID {
		t.Fatal(superseded)
	}
	// Persisted decision and captured snapshots survive reopening the database.
	var name string
	s.DB.QueryRow("SELECT current_database()").Scan(&name)
	s.DB.Close()
	reopened, e := Open(pgtest.URLFor(os.Getenv(pgtest.Env), name))
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.DB.Close()
	got, e := reopened.ProjectContext(ctx, DefaultCompany, "p")
	if e != nil || got.Decisions[0].ID != current.Decisions[0].ID {
		t.Fatal(got, e)
	}
}

func TestDecisionContextBounds(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	for i := 0; i < 33; i++ {
		d, e := s.ProposeDecision(ctx, DefaultCompany, "p", decisions.Propose{Topic: fmt.Sprint("topic-", i), Value: strings.Repeat("я", 2000), Rationale: strings.Repeat("漢", 1000), RequestID: fmt.Sprint(i)})
		if e != nil {
			t.Fatal(e)
		}
		if _, e = s.RecordDecision(ctx, DefaultCompany, d.ID, decisions.Record{Actor: "owner", Version: 1, Status: "approved", Rationale: "reviewed", RequestID: fmt.Sprint(i)}); e != nil {
			t.Fatal(e)
		}
	}
	v, e := s.ProjectContext(ctx, DefaultCompany, "p")
	if e != nil || len(v.Decisions) != 32 || !v.MoreDecisions {
		t.Fatal(v, e)
	}
	for _, d := range v.Decisions {
		if !d.Truncated || len(d.Value) > 2048 || len(d.Rationale) > 1024 || !utf8.ValidString(d.Value) || !utf8.ValidString(d.Rationale) {
			t.Fatal(d)
		}
	}
}

func TestJournalArtifactReferencesAndPagination(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task := createEngineTask(t, s, "design-producer")
	ref := tasks.Artifact{ID: "design", Version: "1", URI: "git:abc:design.svg"}
	if _, e := s.RegisterArtifact(ctx, DefaultCompany, "p", artifacts.Register{Artifact: ref, TaskID: task.ID, Actor: "owner", RequestID: "register"}); e != nil {
		t.Fatal(e)
	}
	in := reports.Create{Actor: "owner", Kind: "stage_completed", Summary: "Design ready", Purpose: "Review exact content", Changes: []string{}, Result: "Ready", Artifacts: []tasks.Artifact{ref}, RequestID: "report"}
	report, e := s.CreateReport(ctx, DefaultCompany, "p", in)
	if e != nil || len(report.Artifacts) != 1 {
		t.Fatal(report, e)
	}
	proposal := decisions.Propose{Actor: "owner", Topic: "design", Value: "Use design v1", Rationale: "Reviewed content", Artifacts: []tasks.Artifact{ref}, RequestID: "proposal"}
	d, e := s.ProposeDecision(ctx, DefaultCompany, "p", proposal)
	if e != nil || len(d.Artifacts) != 1 {
		t.Fatal(d, e)
	}
	bad := ref
	bad.URI = "git:different:design.svg"
	in.Artifacts = []tasks.Artifact{bad}
	in.RequestID = "conflict"
	if _, e = s.CreateReport(ctx, DefaultCompany, "p", in); e == nil {
		t.Fatal("conflicting artifact accepted")
	}
	proposal.Artifacts = []tasks.Artifact{bad}
	proposal.RequestID = "conflict"
	if _, e = s.ProposeDecision(ctx, DefaultCompany, "p", proposal); e == nil {
		t.Fatal("conflicting proposal reference accepted")
	}
	in.Artifacts = nil
	in.RequestID = "second"
	if _, e = s.CreateReport(ctx, DefaultCompany, "p", in); e != nil {
		t.Fatal(e)
	}
	first, e := s.Reports(ctx, DefaultCompany, "p", "", "", "", 0, 1)
	if e != nil || !first.HasMore || len(first.Items) != 1 || first.Items[0].ID != report.ID {
		t.Fatal(first, e)
	}
	second, e := s.Reports(ctx, DefaultCompany, "p", "", "", "", first.NextAfter, 1)
	if e != nil || second.HasMore || len(second.Items) != 1 || second.Items[0].ID == report.ID {
		t.Fatal(second, e)
	}
	state, e := s.RecordDecision(ctx, DefaultCompany, d.ID, decisions.Record{Actor: "owner", Version: 1, Status: "under_discussion", Rationale: "Need review", RequestID: "discuss"})
	if e != nil || state.Status != "under_discussion" {
		t.Fatal(state, e)
	}
	if _, e = s.RecordDecision(ctx, DefaultCompany, d.ID, decisions.Record{Actor: "owner", Version: 1, Status: "rejected", Rationale: "Stale review", RequestID: "stale"}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale review", e)
	}
	rejected, e := s.RecordDecision(ctx, DefaultCompany, d.ID, decisions.Record{Actor: "owner", Version: state.Version, Status: "rejected", Rationale: "Other requirements", RequestID: "reject"})
	if e != nil || rejected.Status != "rejected" || rejected.ApprovedBy != "" {
		t.Fatal(rejected, e)
	}
}
