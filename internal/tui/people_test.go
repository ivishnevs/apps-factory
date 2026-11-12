package tui

import (
	"strings"
	"testing"

	"appfactory/internal/tasks"
)

// TestPeopleOnTheBoard (people-assignees 4.1): a person's task shows the person, and a:people / a:member:USER keep
// people's tasks on the board.
func TestPeopleOnTheBoard(t *testing.T) {
	m, _ := demoModel(t, 120, 40)
	m.ov.Tasks = append(m.ov.Tasks, Task{Task: tasks.Task{ID: "mine", ProjectID: "project-1", Title: "Menu", Status: "READY", Assignee: "member:ilya"}})
	if chip := agentChip("member:ilya"); !strings.Contains(chip, "@ilya") {
		t.Fatal(chip)
	}
	for _, filter := range []string{"a:people", "a:member:ilya"} {
		m.board.filter = filter
		got := m.boardTasks()
		if len(got) != 1 || got[0].ID != "mine" {
			t.Fatal(filter, got)
		}
	}
}
