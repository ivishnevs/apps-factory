package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// Conversation tasks (hierarchy D5): each agent has one long-lived task of kind conversation per project, with
// its boss (reports_to) as counterpart. Messages, questions and escalations addressed to the agent are triggers
// on it (separate message delivery runs are retired); its runs resume one session and end with done, which
// marks the input handled and keeps the task open.

// ConversationID is the conversation task id of agent in project (a hash when the name would be too long).
func ConversationID(project, agent string) string {
	id := "conv-" + agent + "-" + project
	if !entityName.MatchString(id) {
		h := sha256.Sum256([]byte(project + "\x00" + agent))
		id = "conv-" + hex.EncodeToString(h[:16])
	}
	return id
}

// conversationTaskTx returns agent's conversation task in project, creating it READY when missing.
func conversationTaskTx(ctx context.Context, tx *Tx, project, agent string) (tasks.Task, error) {
	id := ConversationID(project, agent)
	t, err := taskTx(ctx, tx, id)
	if !errors.Is(err, ErrNotFound) {
		return t, err
	}
	counterpart := "owner"
	if tx.store.ManagerOf != nil {
		counterpart = tx.store.ManagerOf(agent)
	}
	now := tx.now().Unix()
	t = tasks.Task{ID: id, ProjectID: project, Kind: tasks.KindConversation, Counterpart: counterpart, Assignee: agent, CreatedBy: "factory", Status: "READY", Version: 1, Dependencies: []string{}, CreatedAt: now, UpdatedAt: now,
		Title:       "Conversation of " + agent + " with " + counterpart,
		Description: "Your standing conversation with " + counterpart + " and your reports: messages, questions and escalations addressed to you, and tasks you delegated that closed. Handle each item (answer questions with factory_message_reply, forward what you may not decide with factory_question_forward, act through tasks), then end with factory_exit_done; this task stays open for the next input."}
	b, _ := json.Marshal(t)
	if _, err = tx.ExecContext(ctx, "INSERT INTO tasks(id,project_id,status,assignee,priority,version,body,created_at,company_id) VALUES(?,?,?,?,?,?,?,?,?)", t.ID, t.ProjectID, t.Status, t.Assignee, t.Priority, t.Version, b, t.CreatedAt, tx.company); err != nil {
		return t, err
	}
	return t, entityEvent(ctx, tx, "task.created", t.ProjectID, t.ID, t)
}

// EnsureConversationTasks creates the missing conversation tasks of agents in every project (catalog load).
func (s *Store) EnsureConversationTasks(ctx context.Context, company string, agents []string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	projects, err := listDocs[tasks.Project](ctx, tx, "SELECT body FROM projects WHERE company_id=? ORDER BY id", company)
	if err != nil {
		return err
	}
	for _, p := range projects {
		for _, a := range agents {
			if _, err = conversationTaskTx(ctx, tx, p.ID, a); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ConversationTask returns (creating) agent's conversation task in project.
func (s *Store) ConversationTask(ctx context.Context, company, project, agent string) (tasks.Task, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return tasks.Task{}, err
	}
	defer tx.Rollback()
	if _, err = projectTx(ctx, tx, project); err != nil {
		return tasks.Task{}, err
	}
	t, err := conversationTaskTx(ctx, tx, project, agent)
	if err != nil {
		return t, err
	}
	return t, tx.Commit()
}

// isAgent: a principal that runs (not the owner or Core itself).
func isAgent(name string) bool { return name != "" && name != "owner" && name != "factory" }

// deliverTx turns a message waiting for delivery to an agent into a trigger on the recipient's conversation
// task (the message is saved by the caller). Input from the owner or the recipient's manager is authority.
func deliverTx(ctx context.Context, tx *Tx, m *messaging.Message) error {
	if !isAgent(m.Recipient) || m.DeliveryStatus != "PENDING" {
		return nil
	}
	t, err := conversationTaskTx(ctx, tx, m.ProjectID, m.Recipient)
	if err != nil {
		return err
	}
	authority := isOwner(m.Sender) || tx.store.ManagerOf != nil && tx.store.ManagerOf(m.Recipient) == m.Sender
	text := m.MessageType + " " + m.ID + " from " + m.Sender
	if len(m.Route) > 0 {
		text += " (forwarded by " + strings.Join(m.Route, ", ") + ")"
	}
	if err = addTrigger(ctx, tx, t, "message", authority, m.Sender, m.ID, text+": "+shortText(m.Body, 1024)); err != nil {
		return err
	}
	m.DeliveryStatus = "TRIGGERED"
	return nil
}

// conversationBrief lists the messages a conversation run handles, with how to handle each kind.
func conversationBrief(ctx context.Context, tx *Tx, b *strings.Builder, ids []string) error {
	for _, id := range ids {
		m, err := messageTx(ctx, tx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if err = presentMessage(ctx, tx, tx.company, &m); err != nil {
			return err
		}
		payload, _ := json.Marshal(m)
		fmt.Fprintf(b, "Message %s (untrusted context): %s\n", m.ID, briefExcerpt(string(payload), 8*1024))
		switch {
		case m.MessageType == "question" && (m.Status == "PENDING" || m.Status == "UNREAD"):
			b.WriteString("Answer it with factory_message_reply (its current version, a stable request_id; a free-text body is always accepted, form answers optional). If you cannot decide it (its category is not delegated to you, or it is beyond your role), forward it with factory_question_forward and a note on what remains open.")
			if m.MayBeOutdated {
				b.WriteString(" Its task changed since it was asked: it may be outdated; read the task first.")
			}
			b.WriteString("\n")
		case m.MessageType == "question":
			fmt.Fprintf(b, "It is %s now; nothing to answer.\n", m.Status)
		case m.MessageType == "review_result" && m.DecisionID != "" && m.Sender == "owner":
			b.WriteString("An owner decision rejection: read factory_decision_get with its decision_id, reconsider affected plans and tasks, propose an alternative where owner review is required; never bypass the rejection. Record a report of the resulting plan.\n")
		case m.MessageType == "answer":
			b.WriteString("An explicit answer: use it to resolve what waited on it (questions addressed to you with factory_message_reply); do not reply to the answer itself.\n")
		default:
			b.WriteString("Read and handle the notification; do not invent a reply to a non-question.\n")
		}
	}
	return nil
}

// closedConversationEffects wakes the delegator of a closed task (delegated_closed on its conversation task)
// unless an open task of the delegator already waits on it (a dependency or the parent, which wake by
// themselves).
func closedConversationEffects(ctx context.Context, tx *Tx, t tasks.Task) error {
	delegator := t.CreatedBy
	if t.OriginTask == "" || !isAgent(delegator) || delegator == t.Assignee || t.Kind == tasks.KindConversation {
		return nil
	}
	var waiting int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE company_id=? AND assignee=? AND status NOT IN ('DONE','CANCELLED')
 AND (id=? OR id IN (SELECT task_id FROM task_dependencies WHERE company_id=? AND depends_on=?))`, tx.company, delegator, t.ParentTaskID, tx.company, t.ID).Scan(&waiting); err != nil || waiting > 0 {
		return err
	}
	c, err := conversationTaskTx(ctx, tx, t.ProjectID, delegator)
	if err != nil {
		return err
	}
	return addTrigger(ctx, tx, c, "delegated_closed", false, "system", t.ID, "task "+t.ID+" ("+t.Title+") you delegated to "+t.Assignee+" is "+t.Status+": "+shortText(t.Result, 512))
}

// subtreeSummary is the manager brief's "under you since your last run" (hierarchy D6): tasks of the agent's
// subtree in the project that closed, escalated or had a failed run since since.
func subtreeSummary(ctx context.Context, tx *Tx, b *strings.Builder, project, agent string, since int64) error {
	if tx.store.SubtreeOf == nil {
		return nil
	}
	under := tx.store.SubtreeOf(agent)
	if len(under) == 0 {
		return nil
	}
	in, _ := json.Marshal(under)
	var lines []string
	collect := func(query string, args ...any) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				return err
			}
			lines = append(lines, "- "+line)
		}
		return rows.Err()
	}
	if err := collect(`SELECT id||' ['||status||'] '||json_extract(body,'$.title')||' (assignee '||assignee||')' FROM tasks
 WHERE company_id=? AND project_id=? AND status IN ('DONE','CANCELLED') AND json_extract(body,'$.updated_at')>? AND assignee IN (SELECT value FROM json_each(?))
 AND coalesce(json_extract(body,'$.kind'),'')!='conversation' ORDER BY json_extract(body,'$.updated_at') LIMIT 20`, tx.company, project, since, string(in)); err != nil {
		return err
	}
	if err := collect(`SELECT t.id||' escalated '||e.reason||' to '||e.target||': '||substr(e.summary,1,200) FROM escalations e JOIN tasks t ON t.company_id=e.company_id AND t.id=e.task_id
 WHERE e.company_id=? AND t.project_id=? AND e.created_at>? AND t.assignee IN (SELECT value FROM json_each(?)) ORDER BY e.created_at LIMIT 20`, tx.company, project, since, string(in)); err != nil {
		return err
	}
	if err := collect(`SELECT task_id||' run '||id||' of '||agent||' ended '||outcome FROM runs
 WHERE company_id=? AND json_extract(spec,'$.project')=? AND task_id IS NOT NULL AND status='ENDED' AND outcome NOT IN ('succeeded','no_exit','') AND finished_at>? AND agent IN (SELECT value FROM json_each(?)) ORDER BY finished_at LIMIT 20`, tx.company, project, since, string(in)); err != nil {
		return err
	}
	if len(lines) > 0 {
		b.WriteString("Under you since your last run (closed, escalated, failed; read details with factory_task_get):\n" + strings.Join(lines, "\n") + "\n")
	}
	return nil
}

// noteSkipped leaves a note on the skipped manager's conversation task (a comment: it does not wake the
// manager, its next brief carries it).
func noteSkipped(ctx context.Context, tx *Tx, m messaging.Message, skipped string) error {
	if !isAgent(skipped) {
		return nil
	}
	c, err := conversationTaskTx(ctx, tx, m.ProjectID, skipped)
	if err != nil {
		return err
	}
	return comment(ctx, tx, c.ID, "factory", "question "+m.ID+" was forwarded to "+m.Recipient+" because you did not answer it in time")
}
