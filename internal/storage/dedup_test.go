package storage

import (
	"context"
	"errors"
	"testing"

	"appfactory/internal/tasks"
)

func TestAgentCreatedTasksAreDeduplicated(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	roles := map[string]string{"ios-1": "ios", "ios-2": "ios", "qa": "qa"}
	create := func(id, title, assignee, origin string) tasks.Task {
		t.Helper()
		v, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: id, ProjectID: "p", Title: title, Assignee: assignee, OriginTask: origin, OriginRun: "run-" + origin, Roles: roles})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	first := create("a", "Write  login tests", "ios-1", "parent")
	if first.Deduplicated || first.OriginTask != "parent" || first.OriginRun != "run-parent" {
		t.Fatal(first)
	}
	if again := create("b", " write login TESTS ", "ios-2", "parent"); again.ID != "a" || !again.Deduplicated {
		t.Fatal("same origin, title and role created another task", again)
	}
	if stored, _ := s.Task(ctx, DefaultCompany, "a"); stored.Deduplicated {
		t.Fatal("dedup flag persisted")
	}
	if other := create("c", "Write login tests", "qa", "parent"); other.ID != "c" {
		t.Fatal("different role deduplicated", other)
	}
	if other := create("d", "Write login tests", "ios-1", "elsewhere"); other.ID != "d" {
		t.Fatal("different origin deduplicated", other)
	}
	if owner := create("e", "Write login tests", "ios-1", ""); owner.ID != "e" {
		t.Fatal("task without origin deduplicated", owner)
	}
	if _, err := s.TaskAction(ctx, DefaultCompany, "a", tasks.Action{Version: first.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	if fresh := create("f", "Write login tests", "ios-2", "parent"); fresh.ID != "f" || fresh.Deduplicated {
		t.Fatal("closed task reused", fresh)
	}
}

func TestParentAndDependenciesMustNotFormCycle(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	create := func(id, parent string, deps ...string) error {
		_, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: id, ProjectID: "p", Title: id, ParentTaskID: parent, Dependencies: deps})
		return err
	}
	if err := create("parent", ""); err != nil {
		t.Fatal(err)
	}
	if err := create("child", "parent", "parent"); !errors.Is(err, ErrCycle) {
		t.Fatal("dependency on own parent accepted", err)
	}
	if err := create("after-parent", "", "parent"); err != nil {
		t.Fatal(err)
	}
	if err := create("child2", "parent", "after-parent"); !errors.Is(err, ErrCycle) {
		t.Fatal("dependency reaching the parent accepted", err)
	}
	if err := create("sibling", "parent"); err != nil {
		t.Fatal(err)
	}
	if err := create("child3", "parent", "sibling"); err != nil {
		t.Fatal("independent sibling dependency refused", err)
	}
	if _, err := s.Task(ctx, DefaultCompany, "child"); !errors.Is(err, ErrNotFound) {
		t.Fatal("refused task persisted", err)
	}
}
