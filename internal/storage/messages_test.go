package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	lease, err := s.Claim(ctx, DefaultCompany, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	message, err := s.SendMessage(ctx, DefaultCompany, messaging.Send{ProjectID: "p", TaskID: task.ID, Sender: "demo", Recipient: recipient, Body: "Which audience?", Blocking: true, MessageType: "question", RequestID: "ask", RunID: run.ID, Questions: []messaging.Question{{ID: "audience", Type: "single_choice", Question: "Audience?", Options: []string{"Users", "Pros"}}}})
	if err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, DefaultCompany, task.ID)
	return task, run, lease, message
}
func applyMessageRun(t *testing.T, s *Store, run protocol.RunSpec, lease, outcome string) {
	t.Helper()
	if err := s.Apply(context.Background(), DefaultCompany, protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: "ENDED", Outcome: outcome, Result: "turn result", SessionID: "thread-" + run.ID})); err != nil {
		t.Fatal(err)
	}
}
func TestBlockingAnswerRestartAndSameThread(t *testing.T) {
	ctx := context.Background()
	path := testURL(t)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.DB.Close() }()
	if _, err = s.CreateProject(ctx, DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/messaging"}); err != nil {
		t.Fatal(err)
	}
	task, run, lease, m := askOnTask(t, s, "owner")
	answerInput := messaging.Reply{Version: m.Version, Sender: "owner", Body: "Professionals", Answers: map[string]any{"audience": "Pros"}, RequestID: "answer"}
	// Before terminal evidence, a reply doesn't release a writer or schedule a duplicate.
	answer, err := s.ReplyMessage(ctx, DefaultCompany, m.ID, answerInput)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.ReplyMessage(ctx, DefaultCompany, m.ID, answerInput)
	if err != nil || again.ID != answer.ID {
		t.Fatal("reply replay", again, err)
	}
	task, _ = s.Task(ctx, DefaultCompany, task.ID)
	if task.Status != "IN_PROGRESS" {
		t.Fatal(task)
	}
	if _, err = s.TaskAction(ctx, DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal("active writer bypass", err)
	}
	s.DB.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	applyMessageRun(t, s, run, lease, "succeeded")
	task, _ = s.Task(ctx, DefaultCompany, task.ID)
	if task.Status != "READY" || task.Submission != nil {
		t.Fatal(task)
	}
	resumed, err := s.Dispatch(ctx, DefaultCompany, task.ID, task.Version, protocol.RunSpec{ID: "resume", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex})
	if err != nil || resumed.SessionID != "thread-"+run.ID {
		t.Fatal("thread lost", resumed, err)
	}
	task, _ = s.Task(ctx, DefaultCompany, task.ID)
	if task.WaitMessageID != "" || task.Attempt != 2 {
		t.Fatal(task)
	}
	if got, err := s.AnswerFor(ctx, DefaultCompany, m.ID); err != nil || got.ID != answer.ID {
		t.Fatal(got, err)
	}
}

// TestAnswerAfterAFailedOrCancelledRun: an answer does not resume a run that ended without completing; a
// cancelled task stays cancelled; after a failed run the owner's answer is authority input, which counts as
// progress: the task runs again without the backoff (explicit-exits-authority D7).
func TestAnswerAfterAFailedOrCancelledRun(t *testing.T) {
	for _, outcome := range []string{"revoked", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			s := engineStore(t)
			ctx := context.Background()
			task, run, lease, m := askOnTask(t, s, "owner")
			applyMessageRun(t, s, run, lease, outcome)
			// The question of a cancelled task is cancelled with it (hierarchy D3).
			if _, err := s.ReplyMessage(ctx, DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Pros", Answers: map[string]any{"audience": "Pros"}, RequestID: "answer"}); (err != nil) != (outcome == "revoked") {
				t.Fatal(err)
			}
			task, _ = s.Task(ctx, DefaultCompany, task.ID)
			if outcome == "revoked" && task.Status != "CANCELLED" || outcome == "failed" && (task.Status != "READY" || task.FailStreak != 0 || task.NotBefore != 0) {
				t.Fatal("unsafe resume", task)
			}
		})
	}
}
func TestPendingQuestionAndReplyValidation(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	task, run, lease, m := askOnTask(t, s, "owner")
	applyMessageRun(t, s, run, lease, "succeeded")
	task, _ = s.Task(ctx, DefaultCompany, task.ID)
	if task.Status != "BLOCKED" {
		t.Fatal(task)
	}
	if _, err := s.TaskAction(ctx, DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	for _, input := range []messaging.Reply{
		{Version: m.Version, Sender: "stranger", Body: "bad", Answers: map[string]any{"audience": "Pros"}, RequestID: "bad-sender"},
		{Version: 0, Sender: "owner", Body: "bad", Answers: map[string]any{"audience": "Pros"}, RequestID: "bad-version"},
		{Version: m.Version, Sender: "owner", Body: "bad", Answers: map[string]any{"audience": "Unknown"}, RequestID: "bad-choice"},
		{Version: m.Version, Sender: "owner", Body: "bad", Answers: map[string]any{"other": "x"}, RequestID: "wrong-form"},
	} {
		if _, err := s.ReplyMessage(ctx, DefaultCompany, m.ID, input); err == nil {
			t.Fatal("bad answer accepted", input)
		}
	}
	if _, err := s.ReplyMessage(ctx, DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Pros", Answers: map[string]any{"audience": "Pros"}, RequestID: "good"}); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, DefaultCompany, task.ID)
	if task.Status != "READY" {
		t.Fatal(task)
	}
	inbox, err := s.Inbox(ctx, DefaultCompany, "p")
	if err != nil || len(inbox) != 0 {
		t.Fatal(inbox, err)
	}
}
func TestQuestionLoopGuardTimeoutAndTransactionalOutbox(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	conv, err := s.CreateConversation(ctx, DefaultCompany, messaging.ConversationCreate{ProjectID: "p", Agent: "manager", Title: "Dialog", RequestID: "conv"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		input := messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "manager", ConversationID: conv.ID, MessageType: "question", Body: fmt.Sprintf("Question %d", i), RequestID: fmt.Sprintf("send-%d", i)}
		m, err := s.SendMessage(ctx, DefaultCompany, input)
		if err != nil {
			t.Fatal(err)
		}
		if i == 8 && m.Status != "ESCALATED" {
			t.Fatal("unbounded chain", m)
		}
		if i == 0 {
			input.RequestID = "duplicate"
			input.Body = "   QUESTION   0  "
			if _, err = s.SendMessage(ctx, DefaultCompany, input); !errors.Is(err, ErrConflict) {
				t.Fatal("repeat allowed", err)
			}
		}
	}
	input := messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Deadline question", RequestID: "deadline", TimeoutSeconds: 1}
	m, err := s.SendMessage(ctx, DefaultCompany, input)
	if err != nil {
		t.Fatal(err)
	}
	// Set a persisted past deadline deterministically instead of sleeping.
	m.Deadline = time.Now().Unix() - 1
	b, _ := json.Marshal(m)
	if _, err = s.DB.Exec("UPDATE messages SET body=? WHERE id=?", b, m.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireMessages(ctx, DefaultCompany); err != nil {
		t.Fatal(err)
	}
	m, _ = s.Message(ctx, DefaultCompany, m.ID)
	if m.Status != "ESCALATED" {
		t.Fatal(m)
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM outbox WHERE subject='factory.default.events.message.created'").Scan(&count); err != nil || count != 10 {
		t.Fatal(count, err)
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

func TestOwnerNotificationInboxRead(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	m, err := s.SendMessage(ctx, DefaultCompany, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", MessageType: "notification", Body: "Progress", RequestID: "notify"})
	if err != nil {
		t.Fatal(err)
	}
	inbox, err := s.Inbox(ctx, DefaultCompany, "p")
	if err != nil || len(inbox) != 1 {
		t.Fatal(inbox, err)
	}
	if _, err = s.ReadInbox(ctx, DefaultCompany, m.ID, 0, "stale"); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if _, err = s.ReadInbox(ctx, DefaultCompany, m.ID, m.Version, "read"); err != nil {
		t.Fatal(err)
	}
	inbox, err = s.Inbox(ctx, DefaultCompany, "p")
	if err != nil || len(inbox) != 0 {
		t.Fatal(inbox, err)
	}
}

func TestMessageEventsShareConversationCorrelation(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	m, err := s.SendMessage(ctx, DefaultCompany, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Correlate", RequestID: "correlate"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReplyMessage(ctx, DefaultCompany, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Answer", RequestID: "correlate-answer"}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.DB.Query("SELECT body FROM outbox WHERE subject LIKE 'factory.default.events.message.%'")
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
