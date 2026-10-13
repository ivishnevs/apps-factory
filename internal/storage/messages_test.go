package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func askOnTask(t *testing.T, s *Store, recipient string) (tasks.Task, protocol.RunSpec, string, messaging.Message) {
	t.Helper()
	ctx := context.Background()
	task := readyEngineTask(t, s, createEngineTask(t, s, "wait-task"))
	run := dispatchEngineTask(t, s, task, "wait-run")
	lease, err := s.Claim(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.SendMessage(ctx, messaging.Send{ProjectID: "p", TaskID: task.ID, Sender: "demo", Recipient: recipient, Body: "Which audience?", Blocking: true, MessageType: "question", RequestID: "ask", RunID: run.ID, Questions: []messaging.Question{{ID: "audience", Type: "single_choice", Question: "Audience?", Options: []string{"Users", "Pros"}}}})
	if err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, task.ID)
	return task, run, lease, message
}
func applyMessageRun(t *testing.T, s *Store, run protocol.RunSpec, lease, status string) {
	t.Helper()
	if err := s.Apply(context.Background(), protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: status, Result: "turn result", SessionID: "thread-" + run.ID})); err != nil {
		t.Fatal(err)
	}
}
func TestBlockingAnswerRestartAndSameThread(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.DB.Close() }()
	if _, err = s.CreateProject(ctx, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/messaging"}); err != nil {
		t.Fatal(err)
	}
	task, run, lease, m := askOnTask(t, s, "owner")
	answerInput := messaging.Reply{Version: m.Version, Sender: "owner", Body: "Professionals", Answers: map[string]any{"audience": "Pros"}, RequestID: "answer"}
	// Before terminal evidence, a reply doesn't release a writer or schedule a duplicate.
	answer, err := s.ReplyMessage(ctx, m.ID, answerInput)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ReplyMessage(ctx, m.ID, answerInput)
	if err != nil || again.ID != answer.ID {
		t.Fatal("reply replay", again, err)
	}
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "IN_PROGRESS" {
		t.Fatal(task)
	}
	if _, err = s.TaskAction(ctx, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal("active writer bypass", err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	applyMessageRun(t, s, run, lease, "COMPLETED")
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "READY" || task.Submission != nil {
		t.Fatal(task)
	}
	resumed, err := s.Dispatch(ctx, task.ID, task.Version, protocol.RunSpec{ID: "resume", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex})
	if err != nil || resumed.SessionID != "thread-"+run.ID {
		t.Fatal("thread lost", resumed, err)
	}
	task, _ = s.Task(ctx, task.ID)
	if task.WaitMessageID != "" || task.Attempt != 2 {
		t.Fatal(task)
	}
	if got, err := s.AnswerFor(ctx, m.ID); err != nil || got.ID != answer.ID {
		t.Fatal(got, err)
	}
}
func TestAnswerDoesNotResumeUnknownOrCancelled(t *testing.T) {
	for _, status := range []string{"UNKNOWN", "CANCELLED", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			s := engineStore(t)
			ctx := context.Background()
			task, run, lease, m := askOnTask(t, s, "owner")
			applyMessageRun(t, s, run, lease, status)
			if _, err := s.ReplyMessage(ctx, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Pros", Answers: map[string]any{"audience": "Pros"}, RequestID: "answer"}); err != nil {
				t.Fatal(err)
			}
			task, _ = s.Task(ctx, task.ID)
			if task.Status == "READY" || task.RetryEligible {
				t.Fatal("unsafe resume", task)
			}
		})
	}
}
func TestPendingQuestionAndReplyValidation(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task, run, lease, m := askOnTask(t, s, "owner")
	applyMessageRun(t, s, run, lease, "COMPLETED")
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "BLOCKED" {
		t.Fatal(task)
	}
	if _, err := s.TaskAction(ctx, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for _, input := range []messaging.Reply{
		{Version: m.Version, Sender: "stranger", Body: "bad", Answers: map[string]any{"audience": "Pros"}, RequestID: "bad-sender"},
		{Version: 0, Sender: "owner", Body: "bad", Answers: map[string]any{"audience": "Pros"}, RequestID: "bad-version"},
		{Version: m.Version, Sender: "owner", Body: "bad", Answers: map[string]any{"audience": "Unknown"}, RequestID: "bad-choice"},
		{Version: m.Version, Sender: "owner", Body: "bad", RequestID: "missing-form"},
	} {
		if _, err := s.ReplyMessage(ctx, m.ID, input); err == nil {
			t.Fatal("bad answer accepted", input)
		}
	}
	if _, err := s.ReplyMessage(ctx, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Pros", Answers: map[string]any{"audience": "Pros"}, RequestID: "good"}); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "READY" {
		t.Fatal(task)
	}
	inbox, err := s.Inbox(ctx, "p")
	if err != nil || len(inbox) != 0 {
		t.Fatal(inbox, err)
	}
}
func TestQuestionLoopGuardTimeoutAndTransactionalOutbox(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	conv, err := s.CreateConversation(ctx, messaging.ConversationCreate{ProjectID: "p", Agent: "manager", Title: "Dialog", RequestID: "conv"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		input := messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "manager", ConversationID: conv.ID, MessageType: "question", Body: fmt.Sprintf("Question %d", i), RequestID: fmt.Sprintf("send-%d", i)}
		m, err := s.SendMessage(ctx, input)
		if err != nil {
			t.Fatal(err)
		}
		if i == 8 && m.Status != "ESCALATED" {
			t.Fatal("unbounded chain", m)
		}
		if i == 0 {
			input.RequestID = "duplicate"
			input.Body = "   QUESTION   0  "
			if _, err = s.SendMessage(ctx, input); !errors.Is(err, ErrConflict) {
				t.Fatal("repeat allowed", err)
			}
		}
	}
	input := messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Deadline question", RequestID: "deadline", TimeoutSeconds: 1}
	m, err := s.SendMessage(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	// Set a persisted past deadline deterministically instead of sleeping.
	m.Deadline = time.Now().Unix() - 1
	b, _ := json.Marshal(m)
	if _, err = s.DB.Exec("UPDATE messages SET body=? WHERE id=?", b, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireMessages(ctx); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Message(ctx, m.ID)
	if m.Status != "ESCALATED" {
		t.Fatal(m)
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM outbox WHERE subject='factory.events.message.created'").Scan(&count); err != nil || count != 10 {
		t.Fatal(count, err)
	}
}
func TestConversationSessionAndUnansweredDeliveryEscalation(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	conv, err := s.CreateConversation(ctx, messaging.ConversationCreate{ProjectID: "p", Agent: "manager", Title: "Owner dialog", RequestID: "conv"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.SendMessage(ctx, messaging.Send{ProjectID: "p", Sender: "owner", Recipient: "manager", ConversationID: conv.ID, MessageType: "question", Body: "First", RequestID: "first"})
	if err != nil {
		t.Fatal(err)
	}
	spec, err := s.DispatchMessage(ctx, m, protocol.RunSpec{ID: "manager-1", Project: "p", Agent: "manager", Workspace: "/tmp/task-engine-p"})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = s.Message(ctx, m.ID)
	if _, err = s.ReplyMessage(ctx, m.ID, messaging.Reply{Version: m.Version, Sender: "manager", Body: "Explicit answer", RequestID: "reply"}); err != nil {
		t.Fatal(err)
	}
	applyMessageRun(t, s, spec, lease, "COMPLETED")
	conv, _ = s.Conversation(ctx, conv.ID)
	if conv.SessionID != "thread-manager-1" {
		t.Fatal(conv)
	}
	m, err = s.SendMessage(ctx, messaging.Send{ProjectID: "p", Sender: "owner", Recipient: "manager", ConversationID: conv.ID, MessageType: "question", Body: "Second", RequestID: "second"})
	if err != nil {
		t.Fatal(err)
	}
	spec, err = s.DispatchMessage(ctx, m, protocol.RunSpec{ID: "manager-2", Project: "p", Agent: "manager", Workspace: "/tmp/task-engine-p"})
	if err != nil || spec.SessionID != "thread-manager-1" {
		t.Fatal(spec, err)
	}
	lease, err = s.Claim(ctx, spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	applyMessageRun(t, s, spec, lease, "COMPLETED")
	m, _ = s.Message(ctx, m.ID)
	if m.Status != "ESCALATED" {
		t.Fatal("final text was treated as answer", m)
	}
}
func TestStructuredAnswerTypes(t *testing.T) {
	qs := []messaging.Question{{ID: "text", Type: "text", Question: "Text?"}, {ID: "confirm", Type: "confirmation", Question: "Confirm?"}, {ID: "multi", Type: "multiple_choice", Question: "Which?", Options: []string{"A", "B"}}}
	if err := validQuestions(qs); err != nil {
		t.Fatal(err)
	}
	if err := validAnswers(qs, map[string]any{"text": "yes", "confirm": true, "multi": []any{"A", "B"}}); err != nil {
		t.Fatal(err)
	}
	if err := validAnswers(qs, map[string]any{"text": "yes", "confirm": "true", "multi": []any{"A", "A"}}); err == nil {
		t.Fatal("invalid form accepted")
	}
}

func TestOwnerEscalationReturnsToManagerAndResumesOrigin(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task, developer, devLease, original := askOnTask(t, s, "manager")
	applyMessageRun(t, s, developer, devLease, "COMPLETED")
	original, _ = s.Message(ctx, original.ID)
	manager, err := s.DispatchMessage(ctx, original, protocol.RunSpec{ID: "manager-escalate", Project: "p", Agent: "manager", Workspace: "/tmp/task-engine-p"})
	if err != nil {
		t.Fatal(err)
	}
	managerLease, err := s.Claim(ctx, manager.ID)
	if err != nil {
		t.Fatal(err)
	}
	escalation, err := s.SendMessage(ctx, messaging.Send{ProjectID: "p", TaskID: task.ID, Sender: "manager", Recipient: "owner", ConversationID: original.ConversationID, MessageType: "question", Body: "Owner advice needed", RequestID: "owner-ask", RunID: manager.ID})
	if err != nil {
		t.Fatal(err)
	}
	applyMessageRun(t, s, manager, managerLease, "COMPLETED")
	original, _ = s.Message(ctx, original.ID)
	if original.Status != "ESCALATED" {
		t.Fatal(original)
	}
	ownerAnswer, err := s.ReplyMessage(ctx, escalation.ID, messaging.Reply{Version: escalation.Version, Sender: "owner", Body: "Choose Pros", RequestID: "owner-answer"})
	if err != nil {
		t.Fatal(err)
	}
	original, _ = s.Message(ctx, original.ID)
	if original.Status != "PENDING" {
		t.Fatal("manager question not reopened", original)
	}
	continuation, err := s.DispatchMessage(ctx, ownerAnswer, protocol.RunSpec{ID: "manager-continue", Project: "p", Agent: "manager", Workspace: "/tmp/task-engine-p"})
	if err != nil {
		t.Fatal(err)
	}
	continuationLease, err := s.Claim(ctx, continuation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplyMessage(ctx, original.ID, messaging.Reply{Version: original.Version, Sender: "manager", Body: "Pros", Answers: map[string]any{"audience": "Pros"}, RequestID: "final-answer"}); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, task.ID)
	if task.Status != "READY" {
		t.Fatal(task)
	}
	if _, err = s.Dispatch(ctx, task.ID, task.Version, protocol.RunSpec{ID: "too-early", Project: "p", Agent: "demo"}); !errors.Is(err, ErrConflict) {
		t.Fatal("manager writer not held", err)
	}
	applyMessageRun(t, s, continuation, continuationLease, "COMPLETED")
	if _, err = s.Dispatch(ctx, task.ID, task.Version, protocol.RunSpec{ID: "safe-resume", Project: "p", Agent: "demo"}); err != nil {
		t.Fatal(err)
	}
}
func TestOwnerNotificationInboxRead(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	m, err := s.SendMessage(ctx, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", MessageType: "notification", Body: "Progress", RequestID: "notify"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := s.Inbox(ctx, "p")
	if err != nil || len(inbox) != 1 {
		t.Fatal(inbox, err)
	}
	if _, err = s.ReadInbox(ctx, m.ID, 0, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ReadInbox(ctx, m.ID, m.Version, "read"); err != nil {
		t.Fatal(err)
	}
	inbox, err = s.Inbox(ctx, "p")
	if err != nil || len(inbox) != 0 {
		t.Fatal(inbox, err)
	}
}

func TestMessageEventsShareConversationCorrelation(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	m, err := s.SendMessage(ctx, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Correlate", RequestID: "correlate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplyMessage(ctx, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Answer", RequestID: "correlate-answer"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.Query("SELECT body FROM outbox WHERE subject LIKE 'factory.events.message.%'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var b []byte
		var e protocol.Envelope
		if err = rows.Scan(&b); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(b, &e); err != nil {
			t.Fatal(err)
		}
		if e.CorrelationID != m.ConversationID {
			t.Fatal("broken chain", e)
		}
		count++
	}
	if err = rows.Err(); err != nil || count != 3 {
		t.Fatal(count, err)
	}
}

func TestOwnerConversationDoesNotLeakThreadToAnotherRecipient(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	conv, err := s.CreateConversation(ctx, messaging.ConversationCreate{ProjectID: "p", Agent: "manager", Title: "Owner", RequestID: "conv"})
	if err != nil {
		t.Fatal(err)
	}
	conv.SessionID = "manager-private-thread"
	b, _ := json.Marshal(conv)
	if _, err = s.DB.Exec("UPDATE conversations SET body=? WHERE id=?", b, conv.ID); err != nil {
		t.Fatal(err)
	}
	question, err := s.SendMessage(ctx, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", ConversationID: conv.ID, MessageType: "question", Body: "Advice", RequestID: "advice"})
	if err != nil {
		t.Fatal(err)
	}
	answer, err := s.ReplyMessage(ctx, question.ID, messaging.Reply{Version: question.Version, Sender: "owner", Body: "Proceed", RequestID: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.DispatchMessage(ctx, answer, protocol.RunSpec{ID: "demo-answer", Project: "p", Agent: "demo", Workspace: "/tmp/task-engine-p"})
	if err != nil || run.SessionID != "" {
		t.Fatal("conversation thread leaked", run, err)
	}
	lease, err := s.Claim(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	applyMessageRun(t, s, run, lease, "COMPLETED")
	conv, _ = s.Conversation(ctx, conv.ID)
	if conv.SessionID != "manager-private-thread" {
		t.Fatal(conv)
	}
}
