package storage

import (
	"context"
	"errors"
	"testing"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func TestCompaniesAreIsolated(t *testing.T) {
	ctx := context.Background()
	s, err := Open(testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	if err = s.CreateCompany(ctx, "acme", "Acme"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate company", err)
	}
	if _, err = s.CreateProject(ctx, "acme", tasks.ProjectCreate{ID: "a", Name: "A", Workspace: "/tmp/tenancy-a", RequestID: "same"}); err != nil {
		t.Fatal(err)
	}
	// Request IDs are scoped per company: the same key in another company is a new mutation.
	if _, err = s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "d", Name: "D", Workspace: "/tmp/tenancy-d", RequestID: "same"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Project(ctx, DefaultCompany, "a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign project visible", err)
	}
	if ps, _ := s.Projects(ctx, DefaultCompany); len(ps) != 1 || ps[0].ID != "d" {
		t.Fatal(ps)
	}
	if _, err = s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "x", ProjectID: "a", Title: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("task in foreign project", err)
	}
	task, err := s.CreateTask(ctx, "acme", tasks.Create{ID: "t", ProjectID: "a", Title: "t", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: "dep", ProjectID: "d", Title: "dep", Dependencies: []string{"t"}}); !errors.Is(err, ErrNotFound) {
		t.Fatal("dependency on foreign task", err)
	}
	if _, err = s.TaskAction(ctx, DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign task mutated", err)
	}
	if ts, _ := s.Tasks(ctx, DefaultCompany, "", ""); len(ts) != 0 {
		t.Fatal(ts)
	}
	if err = s.Create(ctx, "acme", protocol.RunSpec{ID: "r", Project: "a", Agent: "demo", Workspace: "/tmp/tenancy-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Get(ctx, DefaultCompany, "r"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign run visible", err)
	}
	if _, err = s.Claim(ctx, DefaultCompany, "r"); !errors.Is(err, ErrNotFound) {
		t.Fatal("foreign run claimed", err)
	}
	lease, err := s.Claim(ctx, "acme", "r")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Heartbeat(ctx, DefaultCompany, "r", lease); err == nil {
		t.Fatal("foreign heartbeat accepted")
	}
	done := protocol.Wrap("agent.completed", "r", "", protocol.Event{RunID: "r", Lease: lease, Status: "ENDED", Outcome: "succeeded"})
	if err = s.Apply(ctx, DefaultCompany, done); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign event applied", err)
	}
	if err = s.Apply(ctx, "acme", done); err != nil {
		t.Fatal(err)
	}
	if es, _ := s.EntityEvents(ctx, DefaultCompany, "", ""); len(es) != 1 {
		t.Fatal("default sees foreign audit", len(es))
	}
	// Agent slots are per company: the same agent name may run in two companies at once.
	if err = s.Create(ctx, "acme", protocol.RunSpec{ID: "r2", Project: "a", Agent: "demo", Workspace: "/tmp/tenancy-a"}); err != nil {
		t.Fatal(err)
	}
	if err = s.Create(ctx, DefaultCompany, protocol.RunSpec{ID: "r3", Project: "d", Agent: "demo", Workspace: "/tmp/tenancy-d"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, "acme", "r2"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Claim(ctx, DefaultCompany, "r3"); err != nil {
		t.Fatal("agent slot shared across companies", err)
	}
}
