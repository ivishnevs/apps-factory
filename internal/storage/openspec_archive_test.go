package storage

import (
	"context"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/tasks"
)

// finishLinked runs a task linked to change feature through review to DONE.
func finishLinked(t *testing.T, s *Store, id string) tasks.Task {
	t.Helper()
	ctx := context.Background()
	v, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: id, ProjectID: "p", Title: id, Assignee: "demo", OpenSpecChange: "feature", RequestID: "create-" + id})
	if err != nil {
		t.Fatal(err)
	}
	v = readyEngineTask(t, s, v)
	finishEngineRun(t, s, dispatchEngineTask(t, s, v, id+"-run"), "succeeded")
	v, _ = s.Task(ctx, DefaultCompany, id)
	if v, err = s.ReviewTask(ctx, DefaultCompany, id, tasks.Review{Version: v.Version, Accept: true, RequestID: "accept-" + id}); err != nil || v.Status != "DONE" {
		t.Fatal(v, err)
	}
	return v
}

func archiveTasks(t *testing.T, s *Store) []tasks.Task {
	t.Helper()
	out, err := listDocs[tasks.Task](context.Background(), s.DB, "SELECT body FROM tasks WHERE company_id=? AND body->>'created_by'='system' AND body->>'title' LIKE 'Archive OpenSpec change %' ORDER BY id", DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestArchiveTaskOncePerChange (core-owned-project-data 3.3): while another linked task is open nothing
// happens; when the last linked task is DONE, exactly one READY archive task for the change is created,
// assigned to the implementer's manager; repeated evaluations (another linked task finishing, the archive
// task itself finishing) create no second one.
func TestArchiveTaskOncePerChange(t *testing.T) {
	s := specStore(t)
	s.ManagerOf = func(agent string) string {
		if agent == "demo" {
			return "lead"
		}
		return "owner"
	}
	ctx := context.Background()
	open, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "open", ProjectID: "p", Title: "open", Assignee: "demo", OpenSpecChange: "feature", RequestID: "create-open"})
	if err != nil {
		t.Fatal(err)
	}
	finishLinked(t, s, "first")
	if got := archiveTasks(t, s); len(got) != 0 {
		t.Fatal("archive task while a linked task is open:", got)
	}
	if _, err = s.TaskAction(ctx, DefaultCompany, open.ID, tasks.Action{Version: open.Version, Action: "cancel", Reason: "not needed"}); err != nil {
		t.Fatal(err)
	}
	finishLinked(t, s, "last")
	got := archiveTasks(t, s)
	if len(got) != 1 || got[0].Assignee != "lead" || got[0].Status != "READY" || got[0].OpenSpecChange != "feature" {
		t.Fatalf("%+v", got)
	}
	// Re-evaluation: a late linked task finishing does not add another archive task.
	finishLinked(t, s, "late")
	if again := archiveTasks(t, s); len(again) != 1 {
		t.Fatalf("%+v", again)
	}
}

// TestOrphanChangeReportedOnce: the daily check reports an active change without any (recent) task once, to
// the project's manager; a change with an open task is not reported.
func TestOrphanChangeReportedOnce(t *testing.T) {
	s := specStore(t) // change feature, nobody works on it
	fake := clock.NewFake(time.Now())
	s.Clock = fake
	ctx := context.Background()
	putSpec(t, s, "p", "changes/busy/proposal.md", "# Busy\n")
	if _, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ProjectID: "p", Title: "busy", Assignee: "demo", OpenSpecChange: "busy", RequestID: "busy"}); err != nil {
		t.Fatal(err)
	}
	putSpec(t, s, "p", "changes/archive/2026-01-01-old/proposal.md", "# Old\n")
	n, err := s.ReportOrphanChanges(ctx, DefaultCompany, "lead")
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	fake.Advance(24 * time.Hour)
	if n, err = s.ReportOrphanChanges(ctx, DefaultCompany, "lead"); err != nil || n != 0 {
		t.Fatal("reported twice", n, err)
	}
	var events int
	var agent, ref string
	if err = s.DB.QueryRow("SELECT count(*),max(agent),max(ref) FROM feed_events WHERE company_id=? AND kind='openspec.change_orphaned'", DefaultCompany).Scan(&events, &agent, &ref); err != nil || events != 1 || agent != "lead" || ref != "openspec/changes/feature" {
		t.Fatal(events, agent, ref, err)
	}
}
