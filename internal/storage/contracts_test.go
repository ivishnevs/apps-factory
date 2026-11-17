package storage

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/closedset"
	"appfactory/internal/messaging"
	"appfactory/internal/reports"
)

// clearingAnswers change who acts on an escalated task or what they act on (contracts-at-the-boundary §2); retry
// repeats the same thing, cancel throws the work away, and keep_waiting and wait change nothing.
var clearingAnswers = map[string]bool{"reassign": true, ReturnAnswer: true, "accept": true, "spec": true, "new_session": true,
	"continue": true, "drop_dependency": true, "reset_workspace": true, "extend": true, "wake": true, "resume": true}

// TestEveryEscalationCanBeCleared: every escalation reason offers an answer other than retry and cancel that changes
// who acts or what they act on, and every answer it offers is applied.
func TestEveryEscalationCanBeCleared(t *testing.T) {
	for reason, answers := range escalationAnswers {
		clears := false
		for _, a := range answers {
			clears = clears || clearingAnswers[a]
			if _, ok := answerEffect[a]; !ok {
				t.Errorf("%s: answer %s has no effect text for people", reason, a)
			}
		}
		if !clears {
			t.Errorf("%s offers only %v: none changes who acts or what they act on", reason, answers)
		}
		if _, ok := escalationSituation[reason]; !ok {
			t.Errorf("%s has no situation text for people", reason)
		}
	}
}

// TestClosedSetValidators (contracts-at-the-boundary §3): each validator accepts exactly its closed set (the set the
// tool schema's enum is built from) and lists the set when it refuses a value outside it.
func TestClosedSetValidators(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	refused := func(what string, err error, set closedset.Set) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), strings.Join(set[1:], ", ")) {
			t.Errorf("%s: %v", what, err)
		}
	}
	for _, v := range closedset.QuestionTypes {
		q := messaging.Question{ID: "q", Type: v, Question: "?"}
		if strings.HasSuffix(v, "_choice") {
			q.Options = []string{"a", "b"}
		}
		if err := validQuestions([]messaging.Question{q}); err != nil {
			t.Errorf("question type %s: %v", v, err)
		}
	}
	refused("question type", validQuestions([]messaging.Question{{ID: "q", Type: "choice", Question: "?"}}), closedset.QuestionTypes)
	for _, v := range closedset.TaskStatuses {
		if _, err := s.Tasks(ctx, DefaultCompany, "p", v); err != nil {
			t.Errorf("task status %s: %v", v, err)
		}
	}
	_, err := s.Tasks(ctx, DefaultCompany, "p", "open")
	refused("task status", err, closedset.TaskStatuses)
	for _, v := range closedset.DecisionListStatuses {
		if _, err := s.Decisions(ctx, DefaultCompany, "p", v, "", 0, 20); err != nil {
			t.Errorf("decision status %s: %v", v, err)
		}
	}
	_, err = s.Decisions(ctx, DefaultCompany, "p", "pending", "", 0, 20)
	refused("decision status", err, closedset.DecisionListStatuses)
	for _, v := range closedset.ReportListKinds {
		if _, err := s.Reports(ctx, DefaultCompany, "p", v, "", "", 0, 20); err != nil {
			t.Errorf("report list kind %s: %v", v, err)
		}
	}
	for _, v := range closedset.ReportKinds {
		if _, err := s.CreateReport(ctx, DefaultCompany, "p", reports.Create{Kind: v, Summary: "s", Purpose: "p", Result: "r", RequestID: "r-" + v, Actor: "demo"}); err != nil {
			t.Errorf("report kind %s: %v", v, err)
		}
	}
	_, err = s.CreateReport(ctx, DefaultCompany, "p", reports.Create{Kind: "audit", Summary: "s", Purpose: "p", Result: "r", RequestID: "r-audit", Actor: "demo"})
	refused("report kind", err, closedset.ReportKinds)
	for _, v := range closedset.MessageTypes {
		if _, err := s.SendMessage(ctx, DefaultCompany, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "reviewer", MessageType: v, Body: "b", RequestID: "m-" + v}); err != nil {
			t.Errorf("message type %s: %v", v, err)
		}
	}
	_, err = s.SendMessage(ctx, DefaultCompany, messaging.Send{ProjectID: "p", Sender: "demo", Recipient: "reviewer", MessageType: "info", Body: "b", RequestID: "m-info"})
	refused("message type", err, closedset.MessageTypes)
}
