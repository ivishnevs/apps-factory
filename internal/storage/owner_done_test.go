package storage

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/tasks"
)

// TestOwnerTaskDoneNotice: a task the owner created reaches DONE → one inbox notification with its result;
// a task an agent created does not notify the owner.
func TestOwnerTaskDoneNotice(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	finish := func(id, by string) {
		v, err := s.CreateTask(ctx, DefaultCompany, tasks.Create{ID: id, ProjectID: "p", Title: "Research " + id, Assignee: "demo", CreatedBy: by, RequestID: "create-" + id})
		if err != nil {
			t.Fatal(err)
		}
		v = readyEngineTask(t, s, v)
		finishEngineRun(t, s, dispatchEngineTask(t, s, v, id+"-run"), "succeeded")
		v, _ = s.Task(ctx, DefaultCompany, id)
		if v, err = s.ReviewTask(ctx, DefaultCompany, id, tasks.Review{Version: v.Version, Accept: true, RequestID: "accept-" + id}); err != nil || v.Status != "DONE" {
			t.Fatal(v, err)
		}
	}
	finish("mine", "owner")
	finish("theirs", "ceo")
	inbox, err := s.Inbox(ctx, DefaultCompany, "")
	if err != nil {
		t.Fatal(err)
	}
	var notes []string
	for _, m := range inbox {
		if m.MessageType == "notification" && strings.HasPrefix(m.Body, "Done: ") {
			notes = append(notes, m.TaskID)
		}
	}
	if len(notes) != 1 || notes[0] != "mine" {
		t.Fatal(notes)
	}
}
