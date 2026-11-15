package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Refused calls that must not strand what they carried (forward-refusal-guard). A run that called a tool sending or
// forwarding something to someone and was refused for a mistake it can fix (a missing brief, an invalid or too-long
// field) cannot end its work by an exit until the call succeeds; a refusal that is Core's decision (the question limit,
// a conflict, missing authority) lets the exit through and sends it to the agent's manager to check.

// refusalTools are the tools whose refusal is recorded.
var refusalTools = map[string]bool{
	"factory_question_forward":          true,
	"factory_owner_ask":                 true,
	"factory_message_ask":               true,
	"factory_message_send":              true,
	"factory_message_reply":             true,
	"factory_owner_request_approval":    true,
	"factory_artifact_request_approval": true,
	"factory_decision_propose":          true,
}

// guardedExits are the exits that say the run's work is done; ask and block hand the problem up and stay allowed.
var guardedExits = map[string]bool{"done": true, "handoff": true, "submit": true, "wait": true}

// RunRefusal is a recorded refusal of a run's call.
type RunRefusal struct {
	Tool      string `json:"tool"`
	RequestID string `json:"request_id,omitempty"`
	Status    int    `json:"status"`
	Reason    string `json:"reason,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Fixable   bool   `json:"fixable"`
}

func (r RunRefusal) describe() string {
	s := r.Tool
	if r.RequestID != "" {
		s += " (request_id " + r.RequestID + ")"
	}
	why := r.Reason
	if why == "" {
		why = fmt.Sprintf("HTTP %d", r.Status)
	}
	if r.Detail != "" {
		why += ": " + r.Detail
	}
	return s + " was refused: " + why
}

// fixableRefusal: an input refusal (400, 422) the agent fixes and sends again. 403, 404, 409 and 410 are Core's
// decisions (missing authority, a closed or unknown target, the question limit, a conflict).
func fixableRefusal(status int) bool { return status == 400 || status == 422 }

// refusalReason reads the reason and a short detail of a refusal body: JSON {reason, error, hint} or plain text.
func refusalReason(body []byte) (reason, detail string) {
	var v struct {
		Reason string `json:"reason"`
		Error  string `json:"error"`
		Hint   string `json:"hint"`
	}
	if json.Unmarshal(body, &v) == nil && (v.Reason != "" || v.Error != "") {
		detail = v.Error
		if detail == "" {
			detail = v.Hint
		}
		return v.Reason, shortText(detail, 500)
	}
	text := strings.TrimSpace(string(body))
	if i := strings.Index(text, ":"); i > 0 && !strings.Contains(text[:i], " ") {
		reason = text[:i]
	}
	return reason, shortText(text, 500)
}

// RecordToolResult records the outcome of a run's tool call (forward-refusal-guard): a refusal of a tool that sends or
// forwards to someone is kept for the run; a success clears the run's earlier refusals of the same call (same tool and
// request_id, or the same tool when the call had none).
func (s *Store) RecordToolResult(ctx context.Context, company, runID, tool, requestID string, status int, body []byte) error {
	if !refusalTools[tool] || status >= 500 {
		return nil
	}
	now := s.Now().Unix()
	if status < 300 {
		_, err := s.DB.ExecContext(ctx, "UPDATE run_refusals SET cleared_at=? WHERE company_id=? AND run_id=? AND tool=? AND (request_id=? OR ?='') AND cleared_at=0", now, company, runID, tool, requestID, requestID)
		return err
	}
	if status < 400 {
		return nil
	}
	reason, detail := refusalReason(body)
	_, err := s.DB.ExecContext(ctx, "INSERT INTO run_refusals(company_id,run_id,tool,request_id,status,reason,detail,fixable,created_at) VALUES(?,?,?,?,?,?,?,?,?)",
		company, runID, tool, requestID, status, reason, detail, fixableRefusal(status), now)
	return err
}

// openRefusals are the run's refusals not cleared by a later success, oldest first.
func openRefusals(ctx context.Context, tx *Tx, runID string) ([]RunRefusal, error) {
	rows, err := tx.QueryContext(ctx, "SELECT tool,request_id,status,reason,detail,fixable FROM run_refusals WHERE company_id=? AND run_id=? AND cleared_at=0 ORDER BY id", tx.company, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunRefusal
	for rows.Next() {
		var r RunRefusal
		if err = rows.Scan(&r.Tool, &r.RequestID, &r.Status, &r.Reason, &r.Detail, &r.Fixable); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// exitRefusalHold refuses a done/handoff/submit/wait exit while the run has a fixable refusal it has not fixed.
func exitRefusalHold(ctx context.Context, tx *Tx, runID, kind string) error {
	if !guardedExits[kind] {
		return nil
	}
	open, err := openRefusals(ctx, tx, runID)
	if err != nil {
		return err
	}
	for _, r := range open {
		if r.Fixable {
			return refuse("unresolved_refusal", r.describe()+". Nothing was sent: fix it and call "+r.Tool+
				" again with the same request_id, then exit; or, if you cannot, hand the problem up with factory_exit_ask or factory_exit_block.")
		}
	}
	return nil
}

// noticeDecisionRefusals marks an accepted done/handoff/submit/wait exit of a run that had a refusal Core decided
// (forward-refusal-guard): the task shows it, and the agent's manager gets a non-blocking notice to check that the
// agent did not just give up; when the manager is a person it is an attention item in the feed.
func noticeDecisionRefusals(ctx context.Context, tx *Tx, runID, agent string, t tasks.Task, kind string) error {
	if !guardedExits[kind] {
		return nil
	}
	open, err := openRefusals(ctx, tx, runID)
	if err != nil {
		return err
	}
	var refused []string
	for _, r := range open {
		if !r.Fixable {
			refused = append(refused, r.describe())
		}
	}
	if len(refused) == 0 {
		return nil
	}
	manager := "owner"
	if tx.store.ManagerOf != nil {
		if m := tx.store.ManagerOf(tx.company, agent); m != "" {
			manager = m
		}
	}
	text := fmt.Sprintf("%s exited %s on %q after a call Core refused: %s. Check that the work is really settled: reopen the task, answer what %s wanted to ask, or leave it.",
		agent, kind, t.Title, strings.Join(refused, "; "), agent)
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.exit_after_refusal", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: agent, Actor: "factory", Status: t.Status,
		Summary: text, Attention: !isAgent(manager) || tasks.IsPerson(manager)}); err != nil {
		return err
	}
	if !isAgent(manager) || tasks.IsPerson(manager) {
		return nil // a person sees the attention item; no inbox question
	}
	_, err = sendMessageTx(ctx, tx, messaging.Send{ProjectID: t.ProjectID, TaskID: t.ID, Sender: "factory", Recipient: manager, MessageType: "notification", Body: text, RequestID: "exit-after-refusal-" + runID})
	return err
}

// MaxQuestionRewakes bounds how often a pending question wakes its recipient again after runs that did not act on
// it; past it the question stays pending and an attention item names it.
const MaxQuestionRewakes = 3

// rewakePendingQuestions keeps a pending question waking its recipient (forward-refusal-guard). When a conversation
// run of an agent ends with questions addressed to it that its triggers count as handled but that are still pending
// (not answered, forwarded, withdrawn or expired), each gets a new trigger naming it and the run's last refusal, so
// the agent runs again. A conversation parked on its own wait, ask or block is left alone: it comes back by itself.
func rewakePendingQuestions(ctx context.Context, tx *Tx, runID string) error {
	var taskID, agent string
	if err := tx.QueryRowContext(ctx, "SELECT coalesce(task_id,''),agent FROM runs WHERE id=?", runID).Scan(&taskID, &agent); err != nil || taskID == "" {
		return err
	}
	t, err := taskTx(ctx, tx, taskID)
	if err != nil || t.Kind != tasks.KindConversation || t.Status == "DONE" || t.Status == "CANCELLED" {
		return err
	}
	var parked int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM task_parks WHERE company_id=? AND task_id=? AND status='active'", tx.company, t.ID).Scan(&parked); err != nil || parked > 0 {
		return err
	}
	var handled int64
	if err = tx.QueryRowContext(ctx, "SELECT handled_seq FROM tasks WHERE company_id=? AND id=?", tx.company, t.ID).Scan(&handled); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT g.ref_id, count(*), max(g.seq) FROM task_triggers g
		JOIN messages m ON m.company_id=g.company_id AND m.id=g.ref_id
		WHERE g.company_id=? AND g.task_id=? AND g.kind='message' AND m.recipient=? AND m.message_type='question' AND m.status='PENDING'
		GROUP BY g.ref_id HAVING max(g.seq)<=? ORDER BY max(g.seq)`, tx.company, t.ID, agent, handled)
	if err != nil {
		return err
	}
	type pending struct {
		id       string
		triggers int
	}
	var stranded []pending
	for rows.Next() {
		var p pending
		var last int64
		if err = rows.Scan(&p.id, &p.triggers, &last); err != nil {
			rows.Close()
			return err
		}
		stranded = append(stranded, p)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(stranded) == 0 {
		return err
	}
	open, err := openRefusals(ctx, tx, runID)
	if err != nil {
		return err
	}
	last := ""
	if len(open) > 0 {
		last = " The last attempt in your previous run: " + open[len(open)-1].describe() + "."
	}
	for _, p := range stranded {
		if p.triggers > MaxQuestionRewakes {
			var reported int
			if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM feed_events WHERE company_id=? AND kind='message.question_stranded' AND entity_id=?", tx.company, p.id).Scan(&reported); err != nil {
				return err
			}
			if reported == 0 { // once: the item says it stays pending
				if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "message.question_stranded", EntityID: p.id, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: agent, Actor: "factory", Attention: true,
					Summary: fmt.Sprintf("question %s is still pending with %s after %d runs that did not answer or forward it.%s", p.id, agent, MaxQuestionRewakes+1, last)}); err != nil {
					return err
				}
			}
			continue
		}
		body := "question " + p.id + " is still pending with you: your previous run ended without answering or forwarding it. Answer it, or forward it with what the forward needs." + last
		if err = addTrigger(ctx, tx, &t, "message", false, "factory", p.id, body); err != nil {
			return err
		}
	}
	return nil
}
