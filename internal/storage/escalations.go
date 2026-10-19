package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Escalations (reliable-recovery D5): an escalation is a park of kind escalation on the task plus a
// question to its target with the allowed answers as a single choice. A manager that does not answer by
// the deadline loses it to the owner (the question escalates as any other). Answering the question
// applies the answer and clears the park; a condition that goes away clears the escalation by itself.

// EscalationAnswerID is the id of the escalation question's single-choice field.
const EscalationAnswerID = "answer"

// escalationTimeout is the answer deadline of an escalation question (then the owner answers).
const escalationTimeout = 24 * 3600

type Escalation struct {
	ID        string   `json:"id"`
	TaskID    string   `json:"task_id"` // "" for an escalation of a quota key (quota_paused, ref_id names the key)
	Reason    string   `json:"reason"`
	RefID     string   `json:"ref_id,omitempty"`
	Summary   string   `json:"summary"`
	Answers   []string `json:"answers"`
	Target    string   `json:"target"`
	MessageID string   `json:"message_id"`
	Status    string   `json:"status"`
	Answer    string   `json:"answer,omitempty"`
	Actor     string   `json:"actor,omitempty"`
	CreatedAt int64    `json:"created_at"`
	ClosedAt  int64    `json:"closed_at,omitempty"`
}

// EscalationWant is a condition that needs an escalation; Target is a manager agent or "owner".
type EscalationWant struct {
	TaskID, RefID, Summary, Target string
}

// escalationAnswers are the allowed answers per reason; their effects are in applyEscalationAnswer.
var escalationAnswers = map[string][]string{
	"stalled":                 {"unblock", "cancel"},
	"pinned_node_unavailable": {"reset_workspace", "cancel"},
	"workspace_diverged":      {"reset_workspace", "cancel"},
}

// SyncEscalations makes the open escalations of a reason equal to want: a new condition opens one
// (park + question), a condition that went away clears it (park cleared, question withdrawn).
func (s *Store) SyncEscalations(ctx context.Context, company, reason string, want []EscalationWant) error {
	answers, ok := escalationAnswers[reason]
	if !ok {
		return inputError("unknown escalation reason " + reason)
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	open, err := listEscalations(ctx, tx, "WHERE company_id=? AND reason=? AND status='open'", company, reason)
	if err != nil {
		return err
	}
	current := map[string]Escalation{}
	for _, e := range open {
		current[e.TaskID+"\x00"+e.RefID] = e
	}
	for _, w := range want {
		key := w.TaskID + "\x00" + w.RefID
		if _, ok := current[key]; ok {
			delete(current, key)
			continue
		}
		if err = openEscalation(ctx, tx, reason, answers, w); err != nil {
			return err
		}
	}
	for _, e := range current {
		if err = closeEscalation(ctx, tx, e, "cleared", "", "system", "condition cleared"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func openEscalation(ctx context.Context, tx *Tx, reason string, answers []string, w EscalationWant) error {
	t, err := taskTx(ctx, tx, w.TaskID)
	if err != nil {
		return err
	}
	if w.Target == "" {
		w.Target = "owner"
	}
	id := protocol.ID()
	body := fmt.Sprintf("Escalation %s on task %s (%s): %s\nAnswer with one of: %s.", reason, t.ID, t.Title, w.Summary, strings.Join(answers, ", "))
	m, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: t.ProjectID, TaskID: t.ID, Sender: "factory", Recipient: w.Target, MessageType: "question", Body: body,
		Questions:      []messaging.Question{{ID: EscalationAnswerID, Type: "single_choice", Question: "How should task " + t.ID + " continue?", Options: answers}},
		TimeoutSeconds: escalationTimeout, RequestID: "escalation-" + id, UpChain: true})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(answers)
	if _, err = tx.ExecContext(ctx, "INSERT INTO escalations(company_id,id,task_id,reason,ref_id,summary,answers,target,message_id,status,created_at) VALUES(?,?,?,?,?,?,?,?,?,'open',?)",
		tx.company, id, t.ID, reason, w.RefID, w.Summary, string(b), w.Target, m.ID, tx.now().Unix()); err != nil {
		return err
	}
	if err = addPark(ctx, tx, t, tasks.ParkEscalation, m.ID, reason+": "+w.Summary, w.Target == "owner"); err != nil {
		return err
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.escalation." + reason, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: t.Assignee, Status: t.Status, Summary: w.Summary + " → " + w.Target, Attention: true, Ref: m.ID})
}

// closeEscalation ends an open escalation: answered (by the question's answer) or cleared (the
// condition went away, or the task closed); a cleared escalation withdraws its unanswered question.
func closeEscalation(ctx context.Context, tx *Tx, e Escalation, status, answer, actor, why string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE escalations SET status=?,answer=?,actor=?,closed_at=? WHERE id=? AND status='open'", status, answer, actor, tx.now().Unix(), e.ID); err != nil {
		return err
	}
	if e.TaskID == "" {
		return closeScopeEscalation(ctx, tx, e, status, answer, actor, why)
	}
	t, err := taskTx(ctx, tx, e.TaskID)
	if err != nil {
		return err
	}
	if err = clearParks(ctx, tx, t, tasks.ParkEscalation, e.MessageID, "cleared"); err != nil {
		return err
	}
	if status == "cleared" {
		m, err := messageTx(ctx, tx, e.MessageID)
		if err != nil {
			return err
		}
		if m.Status == "PENDING" || m.Status == "ESCALATED" {
			m.Status, m.DeliveryStatus, m.EscalationReason = "WITHDRAWN", "NONE", why
			if err = saveMessage(ctx, tx, &m, "message.withdrawn"); err != nil {
				return err
			}
		}
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.escalation_" + status + "." + e.Reason, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: t.Assignee, Status: t.Status, Summary: strings.TrimSpace(answer + " " + why), Actor: actor, Ref: e.MessageID})
}

// answerEscalation applies the answer to an escalation question (called from the reply transaction);
// a question that is not an escalation is left alone.
func answerEscalation(ctx context.Context, tx *Tx, m messaging.Message, in messaging.Reply) error {
	es, err := listEscalations(ctx, tx, "WHERE company_id=? AND message_id=? AND status='open'", tx.company, m.ID)
	if err != nil || len(es) == 0 {
		return err
	}
	e := es[0]
	answer, _ := in.Answers[EscalationAnswerID].(string)
	if !slices.Contains(e.Answers, answer) {
		return inputError("escalation answer must be one of " + strings.Join(e.Answers, ", "))
	}
	if e.TaskID == "" { // an escalation of a quota key: no task to wake
		if err = closeEscalation(ctx, tx, e, "answered", answer, in.Sender, ""); err != nil {
			return err
		}
		return applyScopeAnswer(ctx, tx, e, answer, in.Sender)
	}
	// Closed first: a cancel answer closes the task, which clears its other open escalations.
	if err = closeEscalation(ctx, tx, e, "answered", answer, in.Sender, ""); err != nil {
		return err
	}
	if err = applyEscalationAnswer(ctx, tx, e, answer, in.Sender); err != nil {
		return err
	}
	t, err := taskTx(ctx, tx, e.TaskID)
	if err != nil || t.Status == "DONE" || t.Status == "CANCELLED" || answer == "accept" {
		return err // accept leaves the task idle until new input
	}
	return addTrigger(ctx, tx, t, "answer", isOwner(in.Sender), in.Sender, m.ID, "escalation "+e.Reason+": "+answer)
}

// ErrNeedsCascade: a cancel answer on a task with open children; cancel them first (or cascade).
var ErrNeedsCascade = fmt.Errorf("%w: needs_cascade: the task has open children", ErrConflict)

func applyEscalationAnswer(ctx context.Context, tx *Tx, e Escalation, answer, actor string) error {
	t, err := taskTx(ctx, tx, e.TaskID)
	if err != nil {
		return err
	}
	if e.Reason == "inspect" && answer != "cancel" {
		return inspectAnswer(ctx, tx, t, e, answer, actor)
	}
	switch answer {
	case "retry": // retries_exhausted, budget_exhausted, no_exit: counters and today's budget reset, run now
		return retryAnswer(ctx, tx, t)
	case "accept": // no_exit: take the work as it is
		return acceptAnswer(ctx, tx, t)
	case "reset_workspace":
		if e.Reason == "workspace_diverged" {
			// The node resets the worktree from the remote branch (heartbeat reset); the task runs again on
			// its reset_done.
			if _, err := tx.ExecContext(ctx, "UPDATE workspaces SET reset_pending=1,unpushed=0,updated_at=? WHERE company_id=? AND task_id=?", tx.now().Unix(), tx.company, t.ID); err != nil {
				return err
			}
			return comment(ctx, tx, t.ID, actor, "reset_workspace: the node resets the worktree from the remote branch; local changes stay on refs/factory/reset")
		}
		// pinned_node_unavailable: unpin; another node starts from the remote branch in a new session. A queued
		// run loses its session now; otherwise the next dispatch starts without one.
		if _, err := tx.ExecContext(ctx, "UPDATE workspaces SET unpushed=0,updated_at=? WHERE company_id=? AND task_id=?", tx.now().Unix(), tx.company, t.ID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "UPDATE runs SET session_id='',spec=json_remove(spec,'$.session_id') WHERE company_id=? AND id=? AND status='QUEUED'", tx.company, t.CurrentRunID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 && !t.FreshSession {
			t.FreshSession = true
			if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
				return err
			}
		}
		return comment(ctx, tx, t.ID, actor, "reset_workspace: continue from the remote branch; unpushed work stays on node "+e.RefID)
	case "unblock": // stalled: stop waiting on the stuck dependency
		t.Dependencies = slices.DeleteFunc(slices.Clone(t.Dependencies), func(d string) bool { return d == e.RefID })
		if err = dependencies(ctx, tx, t); err != nil {
			return err
		}
		if err = comment(ctx, tx, t.ID, actor, "escalation stalled: no longer waits on "+e.RefID); err != nil {
			return err
		}
		return saveTask(ctx, tx, &t, "task.updated")
	case "cancel":
		if t.Status == "DONE" || t.Status == "CANCELLED" {
			return nil
		}
		var open int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM tasks WHERE company_id=? AND json_extract(body,'$.parent_task_id')=? AND status NOT IN ('DONE','CANCELLED')", tx.company, t.ID).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return ErrNeedsCascade
		}
		if t.CurrentRunID != "" {
			var status string
			if err = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", t.CurrentRunID).Scan(&status); err != nil {
				return err
			}
			if status == "QUEUED" || status == "RUNNING" || status == "CANCELLING" || status == "UNKNOWN" {
				return fmt.Errorf("%w: the task has a live run; cancel it with factory tasks cancel", ErrConflict)
			}
		}
		t.Status, t.CancelRequested = "CANCELLED", true
		if err = comment(ctx, tx, t.ID, actor, "cancelled by escalation "+e.Reason); err != nil {
			return err
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return err
		}
		return closedTask(ctx, tx, t)
	}
	return inputError("unsupported escalation answer " + answer)
}

// Escalations lists escalations, the open ones when open is set.
func (s *Store) Escalations(ctx context.Context, company string, open bool) ([]Escalation, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	where := "WHERE company_id=?"
	if open {
		where += " AND status='open'"
	}
	return listEscalations(ctx, tx, where+" ORDER BY created_at,id", company)
}

// OpenEscalationsByTarget counts open escalations per target (routing to the least loaded manager).
func (s *Store) OpenEscalationsByTarget(ctx context.Context, company string) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT target,count(*) FROM escalations WHERE company_id=? AND status='open' GROUP BY target", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var target string
		var n int
		if err = rows.Scan(&target, &n); err != nil {
			return nil, err
		}
		out[target] = n
	}
	return out, rows.Err()
}

func listEscalations(ctx context.Context, tx *Tx, where string, args ...any) ([]Escalation, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,coalesce(task_id,''),reason,ref_id,summary,answers,target,message_id,status,answer,actor,created_at,closed_at FROM escalations "+where, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Escalation{}
	for rows.Next() {
		var e Escalation
		var answers string
		if err = rows.Scan(&e.ID, &e.TaskID, &e.Reason, &e.RefID, &e.Summary, &answers, &e.Target, &e.MessageID, &e.Status, &e.Answer, &e.Actor, &e.CreatedAt, &e.ClosedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(answers), &e.Answers)
		out = append(out, e)
	}
	return out, rows.Err()
}
