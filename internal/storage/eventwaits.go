package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"appfactory/internal/decisions"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// Event waits (decision-waits): factory_exit_wait parks the task on an event instead of a timer. With decision_id
// the park (kind decision) waits for the decision to be recorded (approved or rejected) or withdrawn (superseded);
// with from (an agent or owner, optional topic) the park (kind message) waits for the next message from that sender
// to the task or its agent in the project. No timer wakes either: the event clears the park and adds a trigger
// whose text (the outcome, the message) is in the next brief. An optional deadline only escalates, once: to the
// decision's addressee (the owner), or for a message to the assignee's manager. Authority input (a directive, an
// authority comment, an escalation answer) overrides the park like any interruptible park. With approval_id
// (approval-gates-merge) the task parks as awaiting_approval on an open approval of a version it produced: only that
// approval's decision (or its leaving pending) wakes it; authority input does not override it.

// EventDeadlineReason is the escalation of an event wait past its deadline: keep_waiting leaves the park, continue
// overrides it and runs the task now, cancel cancels it.
const EventDeadlineReason = "event_wait_deadline"

func init() {
	escalationAnswers[EventDeadlineReason] = []string{"keep_waiting", "continue", "cancel"}
}

// eventWaitKind is the park kind of a wait exit: decision, message, or "" for a timed wait.
func eventWaitKind(a map[string]any) string {
	switch {
	case str(a, "approval_id") != "":
		return tasks.ParkApproval
	case str(a, "decision_id") != "":
		return tasks.ParkDecision
	case str(a, "from") != "":
		return tasks.ParkMessage
	}
	return ""
}

// eventDeadline reads the optional deadline of an event wait (0 when none).
func eventDeadline(a map[string]any, now time.Time) (int64, *ExitRefusal) {
	if str(a, "deadline") == "" {
		return 0, nil
	}
	d, err := time.Parse(time.RFC3339, str(a, "deadline"))
	switch {
	case err != nil:
		return 0, refuse("invalid_exit", "deadline must be RFC 3339, e.g. "+now.Add(48*time.Hour).UTC().Format(time.RFC3339))
	case !d.After(now):
		return 0, refuse("invalid_exit", "deadline must be in the future")
	case d.Sub(now) > MaxWaitAhead:
		return 0, refuse("wait_too_long", "the deadline may be at most 14 days ahead ("+now.Add(MaxWaitAhead).UTC().Format(time.RFC3339)+")")
	}
	return d.Unix(), nil
}

// projectDecision finds a decision of the project by its id or a unique id prefix of at least 8 characters (agents
// quote short ids, as in "decision e4a63d3d").
func projectDecision(ctx context.Context, tx *Tx, project, ref string) (decisions.Decision, error) {
	ref = strings.ToLower(strings.TrimSpace(ref))
	if len(ref) < 8 {
		return decisions.Decision{}, ErrNotFound
	}
	ds, err := listDocs[decisions.Decision](ctx, tx, "SELECT body FROM decisions WHERE company_id=? AND project_id=? AND (id=? OR id LIKE ?) ORDER BY seq LIMIT 2", tx.company, project, ref, ref+"%")
	if err != nil {
		return decisions.Decision{}, err
	}
	if len(ds) != 1 {
		return decisions.Decision{}, ErrNotFound
	}
	return ds[0], nil
}

func decisionOpen(d decisions.Decision) bool {
	return d.Status == "proposed" || d.Status == "under_discussion"
}

// hexRef finds id-like tokens in a wait's reason.
var hexRef = regexp.MustCompile(`\b[0-9a-f]{8,32}\b`)

// checkEventWait checks an event wait exit (and, for a timed wait, that it does not wait on an open decision); nothing
// changes.
func checkEventWait(ctx context.Context, tx *Tx, t tasks.Task, agent string, a map[string]any, known func(string) bool) error {
	timed := str(a, "wake_at") != "" || str(a, "after") != ""
	n := 0
	for _, set := range []bool{timed, str(a, "decision_id") != "", str(a, "from") != "", str(a, "approval_id") != ""} {
		if set {
			n++
		}
	}
	if n > 1 {
		return refuse("invalid_exit", "wait on one thing: a time (wake_at or after), a decision (decision_id), a message (from) or an approval holding the task (approval_id)")
	}
	if len(str(a, "topic")) > 256 {
		return refuse("invalid_exit", "topic up to 256 bytes")
	}
	switch eventWaitKind(a) {
	case "":
		// A timed wait for an open decision polls it (seen on VP-2: twelve waits, $3 a wake): park on the decision.
		for _, ref := range hexRef.FindAllString(strings.ToLower(str(a, "reason")), 5) {
			d, err := projectDecision(ctx, tx, t.ProjectID, ref)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if decisionOpen(d) {
				return refuse("wait_on_decision", "decision "+d.ID+" ["+d.Topic+"] is still "+strings.ReplaceAll(d.Status, "_", " ")+": do not poll it with a timed wait; park on it with factory_exit_wait decision_id "+d.ID+" (no timer: you run again once it is recorded, with the outcome in your brief)")
			}
		}
		return nil
	case tasks.ParkDecision:
		d, err := projectDecision(ctx, tx, t.ProjectID, str(a, "decision_id"))
		if errors.Is(err, ErrNotFound) {
			return refuse("unknown_decision", "no decision "+str(a, "decision_id")+" in this project (factory_decision_list shows them)")
		}
		if err != nil {
			return err
		}
		if !decisionOpen(d) {
			return refuse("decision_recorded", "decision "+d.ID+" ["+d.Topic+"] is already "+d.Status+"; do not wait, continue with it: "+decisionOutcome(d))
		}
	case tasks.ParkMessage:
		from := str(a, "from")
		if from == agent || from != "owner" && (known == nil || !known(from)) {
			return refuse("invalid_exit", "from must be owner or a configured agent other than you")
		}
	case tasks.ParkApproval:
		if _, err := approvalHold(ctx, tx, t, str(a, "approval_id")); err != nil {
			return err
		}
		return nil // the approval has its own expiry: no deadline
	}
	_, r := eventDeadline(a, tx.now())
	if r != nil {
		return r
	}
	return nil
}

// applyEventWait parks the task on its decision or message (the exit was checked).
func applyEventWait(ctx context.Context, tx *Tx, t *tasks.Task, a map[string]any) error {
	deadline, _ := eventDeadline(a, tx.now())
	t.Status = "READY"
	if eventWaitKind(a) == tasks.ParkApproval {
		h, err := approvalHold(ctx, tx, *t, str(a, "approval_id"))
		if err != nil {
			return err
		}
		return parkOnApproval(ctx, tx, *t, []ApprovalHold{h}, str(a, "reason"))
	}
	if eventWaitKind(a) == tasks.ParkMessage {
		return parkOnMessageTx(ctx, tx, *t, str(a, "from"), str(a, "topic"), str(a, "reason"), deadline)
	}
	d, err := projectDecision(ctx, tx, t.ProjectID, str(a, "decision_id"))
	if err != nil {
		return err
	}
	reason := "waiting for decision " + d.ID + " [" + d.Topic + "]: " + str(a, "reason")
	return parkEventTx(ctx, tx, *t, tasks.ParkDecision, d.ID, d.Topic, reason, deadline, true)
}

// parkEventTx adds an event park with its topic and deadline.
func parkEventTx(ctx context.Context, tx *Tx, t tasks.Task, kind, ref, topic, reason string, deadline int64, attention bool) error {
	if err := addPark(ctx, tx, t, kind, ref, reason, attention); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE task_parks SET topic=?,deadline=?,escalated_at=0 WHERE company_id=? AND task_id=? AND kind=? AND ref_id=? AND status='active'", shortText(topic, 256), deadline, tx.company, t.ID, kind, ref)
	return err
}

// ParkOnMessage parks an open task on the next message from `from` (an agent or owner), optionally about a topic:
// that message wakes it with the message in its brief, and nothing wakes it before (no timer). A question answered
// with "I'll send details" parks the asking task this way on the owner (inbox-badge-parity 3.3); park after the
// answer's own trigger, which would otherwise override the park.
func (s *Store) ParkOnMessage(ctx context.Context, company, task, from, topic, reason string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t, err := taskTx(ctx, tx, task)
	if err != nil {
		return err
	}
	if err = parkOnMessageTx(ctx, tx, t, from, topic, reason, 0); err != nil {
		return err
	}
	return tx.Commit()
}

// parkOnMessageTx parks t on the next message from `from`.
func parkOnMessageTx(ctx context.Context, tx *Tx, t tasks.Task, from, topic, reason string, deadline int64) error {
	if t.Status == "DONE" || t.Status == "CANCELLED" {
		return fmt.Errorf("%w: task is %s", ErrConflict, t.Status)
	}
	if from == "" || from == "factory" || from == "system" || from == t.Assignee {
		return inputError("from must be owner or an agent other than the assignee")
	}
	text := "waiting for a message from " + from
	if topic != "" {
		text += " about " + topic
	}
	if reason = strings.TrimSpace(reason); reason != "" {
		text += ": " + reason
	}
	return parkEventTx(ctx, tx, t, tasks.ParkMessage, from, topic, text, deadline, from == "owner")
}

// decisionOutcome is a recorded decision in the words of the trigger that wakes its waiting tasks.
func decisionOutcome(d decisions.Decision) string {
	head := "decision " + d.ID + " [" + d.Topic + "] "
	switch d.Status {
	case "approved":
		s := head + "approved: " + shortText(d.Value, 2048)
		if strings.TrimSpace(d.ReviewRationale) != "" {
			s += "\nNote: " + shortText(d.ReviewRationale, 1024)
		}
		return s
	case "rejected":
		return head + "rejected. Reason: " + shortText(d.ReviewRationale, 2048) + "\nRespect the rejection: revert or replan what depended on it; never bypass it."
	case "superseded":
		return head + "was withdrawn (superseded by " + d.SupersededBy + "); read the current one with factory_decision_get " + d.SupersededBy
	}
	return head + "is " + d.Status
}

// decisionRecorded wakes every task parked on d once it is decided or withdrawn: the park clears and a
// decision_recorded trigger carries the outcome into the next brief. A decision still proposed or under discussion
// wakes nobody.
func decisionRecorded(ctx context.Context, tx *Tx, d decisions.Decision) error {
	if decisionOpen(d) {
		return nil
	}
	ids, err := activeEventParks(ctx, tx, tasks.ParkDecision, d.ID)
	if err != nil || len(ids) == 0 {
		return err
	}
	actor := d.RecordedBy
	if d.Status == "superseded" || actor == "" {
		actor = "system"
	}
	outcome := decisionOutcome(d)
	for _, id := range ids {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkDecision, d.ID, "cleared"); err != nil {
			return err
		}
		if err = addTrigger(ctx, tx, &t, "decision_recorded", false, actor, d.ID, outcome); err != nil {
			return err
		}
	}
	return nil
}

func activeEventParks(ctx context.Context, tx *Tx, kind, ref string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT task_id FROM task_parks WHERE company_id=? AND kind=? AND ref_id=? AND status='active' ORDER BY seq", tx.company, kind, ref)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// messageArrived wakes the open tasks of project parked on a message from sender that this message reaches: it is
// about the task (task: only that task), or, about no task, addressed to the task's assignee (recipient): "your turn"
// to one of six parked tasks wakes that one. The park clears and a message_received
// trigger carries the message (ref, its id; "" for a comment) into the next brief.
func messageArrived(ctx context.Context, tx *Tx, project, task, recipient, sender, ref, body string) error {
	if sender == "" || sender == "factory" || sender == "system" || project == "" || task == "" && !isAgent(recipient) {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.task_id FROM task_parks p JOIN tasks t ON t.company_id=p.company_id AND t.id=p.task_id
 WHERE p.company_id=? AND p.kind=? AND p.ref_id=? AND p.status='active' AND t.project_id=? AND t.status NOT IN ('DONE','CANCELLED')
 AND t.assignee!=? AND (p.task_id=? OR (?='' AND t.assignee=?)) ORDER BY p.seq`, tx.company, tasks.ParkMessage, sender, project, sender, task, task, recipient)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkMessage, sender, "cleared"); err != nil {
			return err
		}
		if err = addTrigger(ctx, tx, &t, "message_received", false, sender, ref, "the message you waited for, from "+sender+": "+body); err != nil {
			return err
		}
		// The owner's message is the details the owner promised (inbox-badge-parity): the question closes.
		if sender == "owner" {
			if err = settlePromisedDetails(ctx, tx, id, messaging.DetailsSent, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// parkWho is who clears a park: the recipient of an ask or escalation question, the owner for a decision (its
// addressee), the awaited sender for a message, else the owner.
func parkWho(kind, ref, recipient string) string {
	switch {
	case kind == tasks.ParkMessage && ref != "owner":
		return "agent:" + ref
	case recipient != "" && recipient != "owner":
		return "agent:" + recipient
	}
	return "owner"
}

// eventAwaits is the event of the task's active decision or message park (nil when none).
func eventAwaits(ctx context.Context, tx *Tx, task string) (*tasks.Awaits, error) {
	var kind, ref, topic string
	var deadline int64
	err := tx.QueryRowContext(ctx, "SELECT kind,ref_id,topic,deadline FROM task_parks WHERE company_id=? AND task_id=? AND status='active' AND kind IN (?,?) ORDER BY kind,created_at,seq LIMIT 1", tx.company, task, tasks.ParkDecision, tasks.ParkMessage).Scan(&kind, &ref, &topic, &deadline)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a := &tasks.Awaits{Kind: kind, Topic: topic, Deadline: deadline, Who: parkWho(kind, ref, "")}
	if kind == tasks.ParkMessage {
		a.From = ref
		return a, nil
	}
	a.DecisionID = ref
	if d, err := getDoc[decisions.Decision](ctx, tx, "SELECT body FROM decisions WHERE company_id=? AND id=?", tx.company, ref); err == nil {
		a.MessageID, a.Topic = d.MessageID, d.Topic
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	return a, nil
}

// rootAwaits is the event task waits for through its open dependencies (itself first), up to depth levels down.
func rootAwaits(ctx context.Context, tx *Tx, task string, depth int, seen map[string]bool) (*tasks.Awaits, error) {
	if seen[task] {
		return nil, nil
	}
	seen[task] = true
	a, err := eventAwaits(ctx, tx, task)
	if err != nil || a != nil {
		if a != nil {
			a.TaskID = task
		}
		return a, err
	}
	if depth == 0 {
		return nil, nil
	}
	t, err := taskTx(ctx, tx, task)
	if err != nil {
		return nil, err
	}
	for _, dep := range t.Dependencies {
		var status string
		if err = tx.QueryRowContext(ctx, "SELECT status FROM tasks WHERE company_id=? AND id=?", tx.company, dep).Scan(&status); err != nil {
			return nil, err
		}
		if status == "DONE" || status == "CANCELLED" {
			continue
		}
		if a, err = rootAwaits(ctx, tx, dep, depth-1, seen); err != nil || a != nil {
			return a, err
		}
	}
	return nil, nil
}

// decisionWaiters are the open tasks parked on decision id.
func decisionWaiters(ctx context.Context, q querier, company, id string) ([]decisions.Waiter, error) {
	rows, err := q.QueryContext(ctx, `SELECT p.task_id,coalesce(t.body->>'title','') FROM task_parks p JOIN tasks t ON t.company_id=p.company_id AND t.id=p.task_id
 WHERE p.company_id=? AND p.kind=? AND p.ref_id=? AND p.status='active' ORDER BY p.seq`, company, tasks.ParkDecision, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []decisions.Waiter
	for rows.Next() {
		var w decisions.Waiter
		if err = rows.Scan(&w.TaskID, &w.Title); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// expireEventDeadlines escalates event waits past their deadline, once each: a decision to its addressee (the
// owner), a message to the owner when it waits on the owner, else to the assignee's manager. The park stays.
func expireEventDeadlines(ctx context.Context, tx *Tx) error {
	now := tx.now().Unix()
	type due struct{ id, task, kind, ref, topic string }
	rows, err := tx.QueryContext(ctx, "SELECT id,task_id,kind,ref_id,topic FROM task_parks WHERE company_id=? AND kind IN (?,?) AND status='active' AND deadline>0 AND deadline<=? AND escalated_at=0 ORDER BY seq", tx.company, tasks.ParkDecision, tasks.ParkMessage, now)
	if err != nil {
		return err
	}
	var list []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.id, &d.task, &d.kind, &d.ref, &d.topic); err != nil {
			rows.Close()
			return err
		}
		list = append(list, d)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, d := range list {
		if _, err = tx.ExecContext(ctx, "UPDATE task_parks SET escalated_at=? WHERE company_id=? AND id=?", now, tx.company, d.id); err != nil {
			return err
		}
		t, err := taskTx(ctx, tx, d.task)
		if err != nil {
			return err
		}
		target, what := "owner", "decision "+d.ref
		if d.topic != "" {
			what += " [" + d.topic + "]"
		}
		if d.kind == tasks.ParkMessage {
			what = "a message from " + d.ref
			if d.topic != "" {
				what += " about " + d.topic
			}
			if d.ref != "owner" {
				target = retryTarget(tx, t.Assignee)
			}
		}
		summary := "still waiting for " + what + " at its deadline; keep_waiting leaves it parked, continue runs it now without it"
		if err = openEscalation(ctx, tx, EventDeadlineReason, escalationAnswers[EventDeadlineReason], EscalationWant{TaskID: t.ID, RefID: d.ref, Summary: summary, Target: target}); err != nil {
			return err
		}
	}
	return nil
}

// eventDeadlineAnswer applies keep_waiting and continue to an event_wait_deadline escalation (handled); cancel
// continues as for other escalations.
func eventDeadlineAnswer(ctx context.Context, tx *Tx, t tasks.Task, e Escalation, answer, actor string) (bool, error) {
	if e.Reason != EventDeadlineReason {
		return false, nil
	}
	switch answer {
	case "keep_waiting":
		return true, comment(ctx, tx, t.ID, actor, "escalation "+e.Reason+": keeps waiting for "+e.RefID)
	case "continue":
		for _, kind := range []string{tasks.ParkDecision, tasks.ParkMessage} {
			if err := clearParks(ctx, tx, t, kind, e.RefID, "overridden"); err != nil {
				return true, err
			}
		}
		return true, comment(ctx, tx, t.ID, actor, "escalation "+e.Reason+": continue without waiting for "+e.RefID)
	}
	return false, nil
}

// feedEventWait is the exit feed summary of an event wait.
func feedEventWait(agent string, a map[string]any) string {
	if eventWaitKind(a) == tasks.ParkMessage {
		s := agent + " waiting for a message from " + str(a, "from")
		if str(a, "topic") != "" {
			s += " about " + str(a, "topic")
		}
		return s + ": " + str(a, "reason")
	}
	return agent + " waiting for decision " + str(a, "decision_id") + ": " + str(a, "reason")
}
