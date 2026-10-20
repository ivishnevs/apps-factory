package storage

import (
	"context"
	"errors"
	"testing"

	"appfactory/internal/tasks"
)

// TestTaskParks (owner-ui 6.5): a task's parks as Core keeps them: the active ones by default, cleared
// ones with all; an unknown task is not found.
func TestTaskParks(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	a := readyEngineTask(t, s, createEngineTask(t, s, "a"))
	if list, err := s.TaskParks(ctx, co, "a", false); err != nil || len(list) != 0 {
		t.Fatal(list, err)
	}
	a, err := s.TaskAction(ctx, co, "a", tasks.Action{Version: a.Version, Action: "block", Reason: "waiting for keys"})
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.TaskParks(ctx, co, "a", false)
	if err != nil || len(list) != 1 || list[0].Kind != tasks.ParkBlock || list[0].Status != "active" || list[0].Who != "owner" || list[0].CreatedAt == 0 {
		t.Fatalf("%+v %v", list, err)
	}
	readyEngineTask(t, s, a)
	if list, _ = s.TaskParks(ctx, co, "a", false); len(list) != 0 {
		t.Fatalf("cleared park listed as active: %+v", list)
	}
	if list, _ = s.TaskParks(ctx, co, "a", true); len(list) != 1 || list[0].Status != "cleared" || list[0].ClearedAt == 0 {
		t.Fatalf("%+v", list)
	}
	if _, err = s.TaskParks(ctx, co, "nope", false); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}
