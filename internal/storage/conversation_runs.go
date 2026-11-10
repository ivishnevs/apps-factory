package storage

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"appfactory/internal/tasks"
)

// Conversation runs (work-and-conversation-runs): what a conversation task's run does or waits for, in plain words
// for its clearer and the chat, and the history a conversation starting a fresh session on another node gets.

// liveConversationProjects names the projects of the agent's live conversation runs other than task's, one entry
// per run, oldest first.
func liveConversationProjects(ctx context.Context, tx *Tx, agent, task string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT r.spec->>'project',coalesce(p.body->>'name','') FROM runs r LEFT JOIN projects p ON p.company_id=r.company_id AND p.id=r.spec->>'project'
 WHERE r.company_id=? AND r.agent=? AND r.status!='ENDED' AND coalesce(r.task_id,'')!=? AND `+strings.ReplaceAll(conversationRunSQL, "spec", "r.spec")+` ORDER BY r.created_at,r.seq`, tx.company, agent, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, name string
		if err = rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out = append(out, cmp.Or(name, id))
	}
	return out, rows.Err()
}

// conversationRunState fills snap.Conversation for a conversation task with a live run: the senders a started run
// handles and its start, or why a queued run waits.
func conversationRunState(ctx context.Context, tx *Tx, snap *tasks.Snapshot) error {
	r := snap.CurrentRun
	if snap.Task.Kind != tasks.KindConversation || r == nil || r.Status == "ENDED" {
		return nil
	}
	st := &tasks.ConversationState{}
	if r.Status == "QUEUED" {
		q, err := queuedWhy(ctx, tx, r.ID)
		if err != nil || q == nil {
			return err
		}
		st.Waiting = conversationWaiting(q, snap.Task.Assignee)
		snap.Conversation = st
		return nil
	}
	var from int64
	if err := tx.QueryRowContext(ctx, "SELECT started_at,brief_from,brief_to FROM runs WHERE company_id=? AND id=?", tx.company, r.ID).Scan(&st.Started, &from, &st.BriefTo); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT actor FROM task_triggers WHERE company_id=? AND task_id=? AND seq>? AND seq<=? AND actor!='' ORDER BY seq", tx.company, snap.Task.ID, from, st.BriefTo)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a string
		if err = rows.Scan(&a); err != nil {
			return err
		}
		if !slices.Contains(st.Senders, a) {
			st.Senders = append(st.Senders, a)
		}
	}
	snap.Conversation = st
	return rows.Err()
}

// conversationWaiting says in plain words why agent's queued conversation run is not claimed.
func conversationWaiting(q *QueuedReason, agent string) string {
	label := func(n QueuedNode) string { return cmp.Or(n.Name, n.Node) }
	at := func(t int64) string { return time.Unix(t, 0).UTC().Format("15:04 UTC") }
	switch q.Reason {
	case QueuedPaused:
		return "paused: " + q.Detail
	case QueuedAgentBusy:
		if strings.Contains(q.Detail, "conversation runs") {
			return "waiting for a free conversation run: " + q.Detail
		}
		return "waiting for " + agent + "'s work run (conversations share its limit): " + q.Detail
	case QueuedAffinity:
		state := "on " + q.Holder + ", which has no free slot"
		if q.Inactive != "" {
			state = "on the " + q.Inactive + " node " + q.Holder
		}
		s := "waiting for a node: its session is " + state
		if q.Until > 0 {
			s += "; another node starts it fresh at " + at(q.Until)
		}
		return s
	case QueuedNoSlot, QueuedResourceBusy:
		var full []string
		for _, n := range q.Nodes {
			if n.Reason == QueuedNoSlot || n.Reason == QueuedResourceBusy {
				full = append(full, label(n))
			}
		}
		if len(full) == 0 {
			return "waiting for a node: " + q.Detail
		}
		verb := " is full"
		if len(full) > 1 {
			verb = " are full"
		}
		return "waiting for a node: " + joinAnd(full) + verb
	case QueuedUnclaimed:
		return "starting: " + q.Detail
	}
	return "waiting for a node: " + q.Detail
}

// joinAnd joins names as "a, b and c".
func joinAnd(xs []string) string {
	if len(xs) <= 1 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

// conversationHistory is the brief's opening for a conversation starting a fresh session on another node: the
// conversation's recent input (triggers up to from) and what its earlier runs reported, oldest first.
func conversationHistory(ctx context.Context, tx *Tx, task, run string, from int64) (string, error) {
	type entry struct {
		at   int64
		seq  int64
		text string
	}
	var es []entry
	rows, err := tx.QueryContext(ctx, "SELECT seq,created_at,kind,actor,body FROM task_triggers WHERE company_id=? AND task_id=? AND seq<=? ORDER BY seq DESC LIMIT 20", tx.company, task, from)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var seq, at int64
		var kind, actor, body string
		if err = rows.Scan(&seq, &at, &kind, &actor, &body); err != nil {
			rows.Close()
			return "", err
		}
		es = append(es, entry{at, seq, fmt.Sprintf("- %s %s by %s: %s", time.Unix(at, 0).UTC().Format("2006-01-02 15:04"), kind, cmp.Or(actor, "factory"), briefExcerpt(body, 1024))})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return "", err
	}
	rows, err = tx.QueryContext(ctx, "SELECT finished_at,result FROM runs WHERE company_id=? AND task_id=? AND id!=? AND status='ENDED' AND result!='' ORDER BY finished_at DESC LIMIT 10", tx.company, task, run)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var at int64
		var result string
		if err = rows.Scan(&at, &result); err != nil {
			rows.Close()
			return "", err
		}
		es = append(es, entry{at, 1 << 62, fmt.Sprintf("- %s you (run summary): %s", time.Unix(at, 0).UTC().Format("2006-01-02 15:04"), briefExcerpt(result, 2048))})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return "", err
	}
	if len(es) == 0 {
		return "", nil
	}
	slices.SortStableFunc(es, func(a, b entry) int { return cmp.Or(cmp.Compare(a.at, b.at), cmp.Compare(a.seq, b.seq)) })
	var b strings.Builder
	b.WriteString("Your conversation continues in a new session (its earlier session is on another node). Recent history, oldest first; read more through Factory's tools when you need it:\n")
	for _, e := range es {
		b.WriteString(e.text + "\n")
	}
	b.WriteString("\n")
	return b.String(), nil
}
