package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Owner comments handled (owner-comments-handled). The board's comment on a task is the top authority on it,
// so Core enforces its handling: every board comment gets an outcome (done, forwarded, declined with a reason)
// from the runs it is shown to, and every exit is refused until it has one. A row of comment_outcomes is one
// such obligation, kept on the task whose runs owe it:
//   - assignee: the commented task itself (whoever runs it next answers for it);
//   - forward: the conversation task of the manager a comment was forwarded to (up the chain);
//   - declined, stalled, locked: the project lead's conversation task, when the lead is woken for it.
//
// The lead sees every board comment of its project in its conversation brief without being woken (leadCopy).

// CommentOutcome is one agent's handling of a board comment.
type CommentOutcome struct {
	Agent string `json:"agent"`
	// Reason is why the agent got the comment: assignee, forward, declined, stalled or locked.
	Reason string `json:"reason"`
	// Status is open (not handled yet), done, forwarded or declined.
	Status string `json:"status"`
	// Note is the reason of a decline, the note of a forward, or the notes the comment arrived with.
	Note string `json:"note,omitempty"`
	// To is the manager a forwarded comment went to.
	To string `json:"to,omitempty"`
	// Task is the task a done outcome of a manager or the lead links (the one it created or changed).
	Task string `json:"task,omitempty"`
	At   int64  `json:"at,omitempty"`
}

// UnhandledComment is a board comment an exit is refused for.
type UnhandledComment struct {
	CommentID string `json:"comment_id"`
	TaskID    string `json:"task_id"`
	Text      string `json:"text"`
	// NeedsTask: a done outcome must name the task created or changed for it (task_id).
	NeedsTask bool `json:"needs_task,omitempty"`
}

// ClosedTaskError refuses a comment on a DONE or CANCELLED task (409): nobody runs a closed task again, so the
// comment would go nowhere. The owner reopens the task with the comment or asks the project lead about it.
type ClosedTaskError struct {
	TaskID, Status, Lead string
}

func (e *ClosedTaskError) Error() string {
	return "task " + e.TaskID + " is " + e.Status + ": a comment on a closed task goes nowhere; reopen it with the comment (POST /tasks/" + e.TaskID + "/reopen) or ask the project lead about it (POST /tasks/" + e.TaskID + "/ask-lead)"
}

func (e *ClosedTaskError) Unwrap() error { return ErrConflict }

// Options are the actions the refusal offers.
func (e *ClosedTaskError) Options() []map[string]string {
	return []map[string]string{
		{"action": "reopen", "label": "Reopen with comment", "method": "POST", "path": "/tasks/" + e.TaskID + "/reopen"},
		{"action": "ask_lead", "label": "Ask the lead about it", "method": "POST", "path": "/tasks/" + e.TaskID + "/ask-lead", "lead": e.Lead},
	}
}

// openComment is an open row with its comment.
type openComment struct {
	row, comment, task, agent, reason, note string
	text, commentTask, category, person     string
	conversation                            bool
}

func closedTaskError(ctx context.Context, tx *Tx, t tasks.Task) error {
	return &ClosedTaskError{TaskID: t.ID, Status: t.Status, Lead: leadOf(ctx, tx, t.ProjectID)}
}

// leadOf is the project's lead (its team's lead), "" without a catalog hook.
func leadOf(ctx context.Context, tx *Tx, project string) string {
	if tx.store.LeadOf == nil {
		return ""
	}
	p, err := projectTx(ctx, tx, project)
	if err != nil {
		return ""
	}
	return tx.store.LeadOf(tx.company, p.Team)
}

// boardComment adds an owner comment's obligations: the task's runs owe its outcome; a comment in a locked
// category also wakes the project lead at once. Conversation tasks take the owner's chat as messages, and
// without explicit exits there is no exit to refuse: neither keeps outcomes.
func boardComment(ctx context.Context, tx *Tx, t tasks.Task, commentID, category string) error {
	if !tx.store.ExplicitExits || !isAgent(t.Assignee) || t.Kind == tasks.KindConversation || t.Status == "DONE" || t.Status == "CANCELLED" {
		return nil
	}
	shown := ""
	if t.CurrentRunID != "" {
		// A live run sees the comment as authority input in its tool results and the refusals of its exits.
		var live bool
		if err := tx.QueryRowContext(ctx, "SELECT status IN ('CLAIMED','RUNNING') FROM runs WHERE company_id=? AND id=?", tx.company, t.CurrentRunID).Scan(&live); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if live {
			shown = t.CurrentRunID
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO comment_outcomes(id,company_id,comment_id,task_id,agent,reason,shown_run,created_at) VALUES(?,?,?,?,?,'assignee',?,?)",
		protocol.ID(), tx.company, commentID, t.ID, t.Assignee, shown, tx.now().Unix()); err != nil {
		return err
	}
	if category == "" {
		return nil
	}
	lead := leadOf(ctx, tx, t.ProjectID)
	if lead == "" || lead == t.Assignee {
		return nil // the lead has it already as the task's assignee
	}
	return giveComment(ctx, tx, commentID, lead, "locked", "locked category "+category+": it goes to the project lead at once")
}

// giveComment gives a board comment to agent on its conversation task in the comment's project: a new row and
// one trigger (the forward and the lead's copy are one message when the manager is the lead). An agent that
// already has the comment open only gets the note added; one that handled it is not asked again.
func giveComment(ctx context.Context, tx *Tx, commentID, agent, reason, note string) error {
	if !isAgent(agent) {
		return nil
	}
	var taskID, text, person string
	if err := tx.QueryRowContext(ctx, "SELECT task_id,text,person FROM task_comments WHERE company_id=? AND id=?", tx.company, commentID).Scan(&taskID, &text, &person); err != nil {
		return err
	}
	src, err := taskTx(ctx, tx, taskID)
	if err != nil {
		return err
	}
	conv, err := conversationTaskTx(ctx, tx, src.ProjectID, agent)
	if err != nil {
		return err
	}
	var status string
	err = tx.QueryRowContext(ctx, "SELECT status FROM comment_outcomes WHERE company_id=? AND comment_id=? AND task_id=?", tx.company, commentID, conv.ID).Scan(&status)
	switch {
	case err == nil && status == "open":
		_, err = tx.ExecContext(ctx, "UPDATE comment_outcomes SET note=CASE WHEN note='' THEN ? ELSE note||'; '||? END WHERE company_id=? AND comment_id=? AND task_id=?", note, note, tx.company, commentID, conv.ID)
		return err
	case err == nil:
		return nil
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO comment_outcomes(id,company_id,comment_id,task_id,agent,reason,note,created_at) VALUES(?,?,?,?,?,?,?,?)",
		protocol.ID(), tx.company, commentID, conv.ID, agent, reason, note, tx.now().Unix()); err != nil {
		return err
	}
	by := "the owner"
	if person != "" {
		by += " (" + person + ")"
	}
	body := fmt.Sprintf("board comment %s by %s on task %s (%s, assignee %s, %s) [%s: %s]: %s", commentID, by, src.ID, src.Title, src.Assignee, src.Status, reason, note, text)
	return addTrigger(ctx, tx, &conv, "board_comment", true, "owner", commentID, body)
}

// openComments are the open rows of task with their comments.
func openComments(ctx context.Context, tx *Tx, task string) ([]openComment, error) {
	rows, err := tx.QueryContext(ctx, `SELECT o.id,o.comment_id,o.task_id,o.agent,o.reason,o.note,c.text,c.task_id,c.category,c.person,coalesce(t.body->>'kind','')='conversation'
 FROM comment_outcomes o JOIN task_comments c ON c.company_id=o.company_id AND c.id=o.comment_id JOIN tasks t ON t.company_id=o.company_id AND t.id=o.task_id
 WHERE o.company_id=? AND o.task_id=? AND o.status='open' ORDER BY o.seq`, tx.company, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []openComment
	for rows.Next() {
		var o openComment
		if err = rows.Scan(&o.row, &o.comment, &o.task, &o.agent, &o.reason, &o.note, &o.text, &o.commentTask, &o.category, &o.person, &o.conversation); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// commentsBrief tells a run the board comments it owes an outcome for, and how to record each.
func commentsBrief(ctx context.Context, tx *Tx, b *strings.Builder, t tasks.Task, agent string) error {
	open, err := openComments(ctx, tx, t.ID)
	if err != nil || len(open) == 0 {
		return err
	}
	lead := leadOf(ctx, tx, t.ProjectID)
	b.WriteString("Board comments that need your outcome (the owner's word: Core refuses every exit of this run until each has one):\n")
	conversation := false
	for _, o := range open {
		line := "- comment " + o.comment
		if o.commentTask != t.ID {
			line += " on task " + o.commentTask
		}
		if o.category != "" {
			line += " (locked category " + o.category + ")"
		}
		if o.note != "" {
			line += " [" + o.note + "]"
		}
		b.WriteString(line + ": " + briefExcerpt(o.text, 2*1024) + "\n")
		conversation = conversation || o.conversation
	}
	b.WriteString("Record them on your exit with comments: [{comment_id, outcome: \"done\"}] when you did what it asks, or {comment_id, outcome: \"declined\", reason} with a reason the owner reads.")
	if conversation {
		b.WriteString(" Here done needs task_id: the task you created or changed for it.")
	}
	if agent == lead {
		b.WriteString(" You are the project lead: the board wrote it, so it stops with you; act through a task (done with task_id) or decline it with a reason.\n")
		return nil
	}
	b.WriteString(" When it is not yours to do (another role, a release, money), forward it up the chain with factory_comment_forward {comment_id, note} before your exit: your manager gets it verbatim with the task link. A decline wakes the project lead.\n")
	return nil
}

// leadCopy is the project lead's quiet copy (it does not wake the lead): the board comments of the project
// since its last run with their outcomes, so the lead never asks the board for a word it already gave.
func leadCopy(ctx context.Context, tx *Tx, b *strings.Builder, t tasks.Task, agent string, since int64) error {
	if t.Kind != tasks.KindConversation || agent == "" || agent != leadOf(ctx, tx, t.ProjectID) {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.task_id,c.text,coalesce(k.body->>'title',''),k.assignee FROM task_comments c JOIN tasks k ON k.company_id=c.company_id AND k.id=c.task_id
 WHERE c.company_id=? AND k.project_id=? AND c.author='owner' AND coalesce(k.body->>'kind','')!='conversation'
 AND (c.created_at>=? OR EXISTS (SELECT 1 FROM comment_outcomes o WHERE o.company_id=c.company_id AND o.comment_id=c.id AND o.decided_at>=?))
 AND NOT EXISTS (SELECT 1 FROM comment_outcomes o WHERE o.company_id=c.company_id AND o.comment_id=c.id AND o.task_id=? AND o.status='open')
 ORDER BY c.created_at,c.seq LIMIT 20`, tx.company, t.ProjectID, since, since, t.ID)
	if err != nil {
		return err
	}
	type copyLine struct{ id, task, text, title, assignee string }
	var lines []copyLine
	for rows.Next() {
		var l copyLine
		if err = rows.Scan(&l.id, &l.task, &l.text, &l.title, &l.assignee); err != nil {
			rows.Close()
			return err
		}
		lines = append(lines, l)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if len(lines) == 0 {
		return nil
	}
	b.WriteString("Board comments in this project since your last run (for your information; the assignee handles each, and you are woken only for a decline, a stall or a locked category):\n")
	for _, l := range lines {
		outcomes, err := commentOutcomes(ctx, tx, tx.company, l.id)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "- %s on task %s (%s, assignee %s): %s — %s\n", l.id, l.task, l.title, l.assignee, briefExcerpt(l.text, 512), DescribeOutcomes(outcomes))
	}
	return nil
}

// DescribeOutcomes is a comment's handling in words ("not handled yet", "forwarded to ceo by developer", ...).
func DescribeOutcomes(outcomes []CommentOutcome) string {
	if len(outcomes) == 0 {
		return "not handled yet"
	}
	var parts []string
	for _, o := range outcomes {
		switch o.Status {
		case "done":
			s := "done by " + o.Agent
			if o.Task != "" {
				s += " (task " + o.Task + ")"
			}
			parts = append(parts, s)
		case "forwarded":
			parts = append(parts, "forwarded to "+o.To+" by "+o.Agent)
		case "declined":
			parts = append(parts, "declined by "+o.Agent+": "+o.Note)
		default:
			parts = append(parts, "not handled yet by "+o.Agent)
		}
	}
	return strings.Join(parts, "; ")
}

// commentOutcomes lists a comment's rows in order.
func commentOutcomes(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, company, id string) ([]CommentOutcome, error) {
	rows, err := q.QueryContext(ctx, "SELECT agent,reason,status,note,forwarded_to,linked_task,decided_at FROM comment_outcomes WHERE company_id=? AND comment_id=? ORDER BY seq", company, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommentOutcome{}
	for rows.Next() {
		var o CommentOutcome
		if err = rows.Scan(&o.Agent, &o.Reason, &o.Status, &o.Note, &o.To, &o.Task, &o.At); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// givenOutcome is one entry of an exit's comments argument.
type givenOutcome struct {
	comment, outcome, reason, task string
}

// checkComments checks an exit's comment outcomes against the run task's open board comments: each open one
// needs an outcome (done, or declined with a reason; a manager's or the lead's done names its task); an outcome
// for a comment that is not open here is refused.
func checkComments(ctx context.Context, tx *Tx, t tasks.Task, args map[string]any) ([]givenOutcome, *ExitRefusal, error) {
	open, err := openComments(ctx, tx, t.ID)
	if err != nil {
		return nil, nil, err
	}
	raw, _ := args["comments"].([]any)
	given := map[string]givenOutcome{}
	var out []givenOutcome
	for _, item := range raw {
		m, _ := item.(map[string]any)
		g := givenOutcome{comment: str(m, "comment_id"), outcome: str(m, "outcome"), reason: str(m, "reason"), task: str(m, "task_id")}
		var row *openComment
		for i := range open {
			if open[i].comment == g.comment {
				row = &open[i]
			}
		}
		switch {
		case row == nil:
			return nil, refuse("invalid_exit", "comment "+g.comment+" has no open outcome for this task: record only the board comments your brief or a refusal lists"), nil
		case g.outcome != "done" && g.outcome != "declined":
			return nil, refuse("invalid_exit", "comment "+g.comment+": outcome must be done or declined (forward it with factory_comment_forward)"), nil
		case g.outcome == "declined" && g.reason == "":
			return nil, refuse("invalid_exit", "comment "+g.comment+": a decline needs a reason the owner reads"), nil
		case g.outcome == "done" && row.conversation && g.task == "":
			return nil, refuse("invalid_exit", "comment "+g.comment+": done needs task_id, the task you created or changed for it"), nil
		}
		if g.task != "" {
			linked, err := taskTx(ctx, tx, g.task)
			if errors.Is(err, ErrNotFound) || err == nil && (linked.ProjectID != t.ProjectID || linked.Kind == tasks.KindConversation) {
				return nil, refuse("invalid_exit", "comment "+g.comment+": task_id "+g.task+" is not a task of this project"), nil
			}
			if err != nil {
				return nil, nil, err
			}
		}
		given[g.comment] = g
		out = append(out, g)
	}
	var missing []UnhandledComment
	for _, o := range open {
		if _, ok := given[o.comment]; !ok {
			missing = append(missing, UnhandledComment{CommentID: o.comment, TaskID: o.commentTask, Text: shortText(o.text, 512), NeedsTask: o.conversation})
		}
	}
	if len(missing) == 0 {
		return out, nil, nil
	}
	var list []string
	for _, m := range missing {
		list = append(list, m.CommentID+" ("+shortText(m.Text, 200)+")")
	}
	hint := "board comments without an outcome: " + strings.Join(list, "; ") + ". Handle each in this run, then exit again with comments: [{comment_id, outcome: \"done\"}] or [{comment_id, outcome: \"declined\", reason}] (the owner reads the reason)"
	if missing[0].NeedsTask {
		hint += "; done needs task_id, the task you created or changed for it"
	}
	if lead := leadOf(ctx, tx, t.ProjectID); lead != t.Assignee {
		hint += "; forward one that is not yours with factory_comment_forward {comment_id, note} first"
	}
	return nil, &ExitRefusal{Status: 409, Reason: "comments_unhandled", Hint: hint, Comments: missing}, nil
}

// recordComments saves an accepted exit's comment outcomes. A decline by anyone but the project lead wakes the
// lead to confirm or override it.
func recordComments(ctx context.Context, tx *Tx, t tasks.Task, agent string, given []givenOutcome) error {
	lead := leadOf(ctx, tx, t.ProjectID)
	for _, g := range given {
		note := ""
		if g.outcome == "declined" {
			note = g.reason
		}
		if _, err := tx.ExecContext(ctx, "UPDATE comment_outcomes SET status=?,agent=?,note=?,linked_task=?,decided_at=? WHERE company_id=? AND comment_id=? AND task_id=? AND status='open'",
			g.outcome, agent, note, g.task, tx.now().Unix(), tx.company, g.comment, t.ID); err != nil {
			return err
		}
		summary := "board comment " + g.comment + " " + g.outcome + " by " + agent
		if note != "" {
			summary += ": " + note
		}
		if err := feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "comment." + g.outcome, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: agent, Actor: agent, Status: t.Status, Summary: summary, Ref: g.comment, Attention: g.outcome == "declined" && agent == lead}); err != nil {
			return err
		}
		if g.outcome == "declined" && agent != lead {
			if err := giveComment(ctx, tx, g.comment, lead, "declined", "declined by "+agent+": "+g.reason+"; confirm it (declined with the reason for the owner) or override it (done with a task)"); err != nil {
				return err
			}
		}
	}
	return nil
}

// ForwardComment is factory_comment_forward: run's agent passes an open board comment of its task up the chain
// to its manager, verbatim with the task link, and records the outcome forwarded. The project lead cannot
// forward (the board wrote the comment): it acts through a task or declines.
func (s *Store) ForwardComment(ctx context.Context, company, runID, commentID, note string) (CommentOutcome, error) {
	var out CommentOutcome
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var agent, taskID, status string
	if err = tx.QueryRowContext(ctx, "SELECT agent,coalesce(task_id,''),status FROM runs WHERE company_id=? AND id=?", company, runID).Scan(&agent, &taskID, &status); err != nil {
		return out, err
	}
	if taskID == "" || status != "CLAIMED" && status != "RUNNING" {
		return out, fmt.Errorf("%w: this run has no task", ErrConflict)
	}
	t, err := taskTx(ctx, tx, taskID)
	if err != nil {
		return out, err
	}
	var row string
	if err = tx.QueryRowContext(ctx, "SELECT id FROM comment_outcomes WHERE company_id=? AND comment_id=? AND task_id=? AND status='open'", company, commentID, taskID).Scan(&row); errors.Is(err, sql.ErrNoRows) {
		return out, fmt.Errorf("%w: comment %s has no open outcome for your task", ErrNotFound, commentID)
	} else if err != nil {
		return out, err
	}
	lead := leadOf(ctx, tx, t.ProjectID)
	manager := "owner"
	if s.ManagerOf != nil {
		manager = s.ManagerOf(company, agent)
	}
	if agent == lead || !isAgent(manager) {
		return out, inputError("you are the project lead (or report to the board): a board comment stops with you; act through a task and record done with its task_id on your exit, or decline it with a reason the owner reads")
	}
	if strings.TrimSpace(note) == "" {
		return out, inputError("note required: what you did and why it is not yours to do")
	}
	now := tx.now().Unix()
	if _, err = tx.ExecContext(ctx, "UPDATE comment_outcomes SET status='forwarded',agent=?,note=?,forwarded_to=?,decided_at=? WHERE company_id=? AND id=?", agent, note, manager, now, company, row); err != nil {
		return out, err
	}
	if err = giveComment(ctx, tx, commentID, manager, "forward", "forwarded by "+agent+": "+note); err != nil {
		return out, err
	}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "comment.forwarded", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: agent, Actor: agent, Status: t.Status, Summary: "board comment " + commentID + " forwarded to " + manager + ": " + note, Ref: commentID}); err != nil {
		return out, err
	}
	out = CommentOutcome{Agent: agent, Status: "forwarded", Note: note, To: manager, At: now}
	return out, tx.Commit()
}

// commentRunEnded counts a run that ended without an exit (a crash, a timeout, no exit) against the board
// comments it was shown; they stay open for the next run. A stop by the owner or Core does not count.
func commentRunEnded(ctx context.Context, tx *Tx, runID string) error {
	if !tx.store.ExplicitExits {
		return nil
	}
	hasExit, err := runHasExit(ctx, tx, runID)
	if err != nil || hasExit {
		return err
	}
	var outcome string
	if err = tx.QueryRowContext(ctx, "SELECT outcome FROM runs WHERE company_id=? AND id=?", tx.company, runID).Scan(&outcome); err != nil {
		return err
	}
	if rule, ok := Outcomes[outcome]; !ok || !rule.Attempt && outcome != "lost" {
		return nil
	}
	_, err = tx.ExecContext(ctx, "UPDATE comment_outcomes SET missed=missed+1,counted_run=? WHERE company_id=? AND shown_run=? AND status='open' AND counted_run!=?", runID, tx.company, runID, runID)
	return err
}

// UnhandledCommentRuns is how many runs in a row may end without an exit after being shown a board comment
// before the owner is alerted.
const UnhandledCommentRuns = 2

// UnhandledComments are the alerts for board comments that UnhandledCommentRuns runs ended without handling
// (Core syncs them as kind comment_unhandled: an outcome clears the alert).
func (s *Store) UnhandledComments(ctx context.Context, company string) ([]Alert, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT c.task_id,c.id,c.text,o.agent,o.missed FROM comment_outcomes o JOIN task_comments c ON c.company_id=o.company_id AND c.id=o.comment_id
 WHERE o.company_id=? AND o.status='open' AND o.missed>=? ORDER BY o.seq`, company, UnhandledCommentRuns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var task, id, text, agent string
		var missed int
		if err = rows.Scan(&task, &id, &text, &agent, &missed); err != nil {
			return nil, err
		}
		out = append(out, Alert{TaskID: task, RefID: id, Summary: fmt.Sprintf("board comment %s has no outcome: %d runs of %s ended without an exit after it was shown (%s)", id, missed, agent, shortText(text, 200))})
	}
	return out, rows.Err()
}

// CommentStalls wakes the project lead for each board comment on an active task that has gone without the
// outcome done for longer than the team's limit (limit of the project's team), once per comment.
func (s *Store) CommentStalls(ctx context.Context, company string, limit func(team string) time.Duration) (int, error) {
	if !s.ExplicitExits || s.LeadOf == nil {
		return 0, nil
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT c.id,c.created_at,coalesce(p.body->>'team','') FROM task_comments c
 JOIN tasks k ON k.company_id=c.company_id AND k.id=c.task_id JOIN projects p ON p.company_id=k.company_id AND p.id=k.project_id
 WHERE c.company_id=? AND k.status NOT IN ('DONE','CANCELLED','BACKLOG')
 AND EXISTS (SELECT 1 FROM comment_outcomes o WHERE o.company_id=c.company_id AND o.comment_id=c.id AND o.reason='assignee')
 AND NOT EXISTS (SELECT 1 FROM comment_outcomes o WHERE o.company_id=c.company_id AND o.comment_id=c.id AND (o.status='done' OR o.reason IN ('stalled','locked','declined')))
 ORDER BY c.created_at,c.seq`, company)
	if err != nil {
		return 0, err
	}
	type stall struct {
		id, team string
		created  int64
	}
	var stalls []stall
	for rows.Next() {
		var st stall
		if err = rows.Scan(&st.id, &st.created, &st.team); err != nil {
			rows.Close()
			return 0, err
		}
		stalls = append(stalls, st)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	n := 0
	now := tx.now()
	for _, st := range stalls {
		d := config.DefaultCommentOutcomeWithin
		if limit != nil {
			if v := limit(st.team); v > 0 {
				d = v
			}
		}
		if now.Sub(time.Unix(st.created, 0)) < d {
			continue
		}
		lead := s.LeadOf(company, st.team)
		var leadRow int
		var task string
		if err = tx.QueryRowContext(ctx, "SELECT task_id FROM task_comments WHERE company_id=? AND id=?", company, st.id).Scan(&task); err != nil {
			return n, err
		}
		if t, err := taskTx(ctx, tx, task); err != nil {
			return n, err
		} else if t.Assignee == lead {
			continue // the lead runs the task itself
		}
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM comment_outcomes WHERE company_id=? AND comment_id=? AND agent=?", company, st.id, lead).Scan(&leadRow); err != nil {
			return n, err
		}
		if leadRow > 0 {
			continue // the lead has it already (a forward reached it)
		}
		if err = giveComment(ctx, tx, st.id, lead, "stalled", "no outcome done after "+d.String()+" while the task is active"); err != nil {
			return n, err
		}
		n++
	}
	return n, tx.Commit()
}

// ReopenTask reopens a DONE or CANCELLED task with an owner comment (the closed task's "Reopen with comment"):
// the task goes back to its assignee READY, the comment is its board comment and wakes it.
func (s *Store) ReopenTask(ctx context.Context, company, id string, in tasks.Comment) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.reopen:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return t, err
		}
		if strings.TrimSpace(in.Text) == "" || len(in.Text) > 16*1024 {
			return t, inputError("comment text up to 16 KiB required")
		}
		if t.Status != "DONE" && t.Status != "CANCELLED" {
			return t, fmt.Errorf("%w: task %s is %s, not closed: comment on it instead", ErrConflict, t.ID, t.Status)
		}
		if t.Kind == tasks.KindConversation || t.Assignee == "" {
			return t, inputError("only a closed task with an assignee can be reopened")
		}
		was := t.Status
		t.Status, t.BlockReason, t.CancelRequested = "READY", "", false
		if err = resetRetry(ctx, tx, &t); err != nil {
			return t, err
		}
		if err = saveTaskNote(ctx, tx, &t, "task.updated", "reopened from "+was+" by the owner: "+shortText(in.Text, 200)); err != nil {
			return t, err
		}
		category := in.Category
		if !config.LockedCategory(category) {
			category = config.LockedCategoryOf(in.Text)
		}
		cid, err := commentWith(ctx, tx, t.ID, "owner", in.Text, category)
		if err != nil {
			return t, err
		}
		if err = boardComment(ctx, tx, t, cid, category); err != nil {
			return t, err
		}
		return t, addTrigger(ctx, tx, &t, "owner_comment", true, "owner", cid, "reopened from "+was+": "+in.Text)
	})
}
