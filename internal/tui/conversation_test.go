package tui

import (
	"testing"

	"appfactory/internal/tasks"
)

// work-and-conversation-runs 3.2: the chat names why a report has not answered, from its conversation task.
func TestConversationPending(t *testing.T) {
	m := &Model{}
	if got := m.conversationPending("net", "app"); got != "" {
		t.Fatal(got)
	}
	m.ov.Tasks = []Task{{Task: tasks.Task{ID: "conv-net-app", Kind: tasks.KindConversation, Assignee: "net", ProjectID: "app"}, Reason: tasks.ReasonLiveRun,
		Clearer: tasks.Clearer{Who: "run:r1", Action: "answering now: messages from producer (started 2 min ago); newer messages are next"}}}
	if got := m.conversationPending("net", "app"); got != "answering now: messages from producer (started 2 min ago); newer messages are next" {
		t.Fatal(got)
	}
	m.ov.Tasks[0].Reason = tasks.ReasonIdle
	if got := m.conversationPending("net", "app"); got != "" {
		t.Fatal("idle conversation:", got)
	}
}
