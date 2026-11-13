package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// Wait deadlocks (wait-deadlock-detection). Active parks whose clearer is an agent are the edges of a wait-for graph
// over agents: "task T of agent A waits on agent X" for a message park (from X), an ask or escalation park (its
// question to X) and an awaiting_approval park (the approval delegated to X, not escalated). An agent is stuck when
// it has no run in the project that is not ENDED (conversation runs included): nothing of it is working that could
// answer. A cycle of stuck agents is a deadlock: nobody is left to write. Core refuses the wait exit that would close
// one (wait_cycle), refuses a message wait on an agent that has nothing pending from the waiter (wait_unasked), and
// sweeps cycles formed after both exits.

// WaitCycleReason is the escalation of a cycle that formed again after its tasks were woken once.
const WaitCycleReason = "wait_cycle"

// waitCycleWindow is how long a woken cycle is remembered: the same cycle again within it escalates.
const waitCycleWindow = 24 * time.Hour

// maxCycleHops bounds the walk: A → X → … → A over at most this many waiting agents.
const maxCycleHops = 4

func init() {
	escalationAnswers[WaitCycleReason] = []string{"wake", "reassign", "cancel"}
}

// waitEdge is an active park of an open task whose clearer is an agent.
type waitEdge struct {
	Task, Title, Agent string // the parked task, its title and assignee
	Kind, Ref          string // the park
	On                 string // the agent that clears it
	Since              int64
}

// waitWords is what the edge's task waits for, in words.
func (e waitEdge) waitWords() string {
	switch e.Kind {
	case tasks.ParkMessage:
		return "a message from " + e.On
	case tasks.ParkApproval:
		return e.On + "'s decision on approval " + e.Ref
	}
	return "the answer of " + e.On + " to its question " + e.Ref
}

// waitsOnAgent tells whether a park clearer is an agent (not the owner, a person or the system).
func waitsOnAgent(who string) bool {
	return isAgent(who) && who != "system" && !strings.HasPrefix(who, "member:")
}

// waitEdges lists the agent-cleared parks of the project's open tasks.
func waitEdges(ctx context.Context, tx *Tx, project string) ([]waitEdge, error) {
	rows, err := tx.QueryContext(ctx, `SELECT p.task_id,coalesce(t.body->>'title',''),t.assignee,p.kind,p.ref_id,p.created_at,
 CASE WHEN p.kind='`+tasks.ParkMessage+`' THEN p.ref_id ELSE `+parkRecipient+` END
 FROM task_parks p JOIN tasks t ON t.company_id=p.company_id AND t.id=p.task_id `+parkJoins+`
 WHERE p.company_id=? AND p.status='active' AND t.project_id=? AND t.status NOT IN ('DONE','CANCELLED') AND p.kind IN (?,?,?,?)
 ORDER BY p.seq`, tx.company, project, tasks.ParkMessage, tasks.ParkAsk, tasks.ParkEscalation, tasks.ParkApproval)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waitEdge
	for rows.Next() {
		var e waitEdge
		if err = rows.Scan(&e.Task, &e.Title, &e.Agent, &e.Kind, &e.Ref, &e.Since, &e.On); err != nil {
			return nil, err
		}
		if waitsOnAgent(e.On) && e.On != e.Agent {
			out = append(out, e)
		}
	}
	return out, rows.Err()
}

// workingAgents are the agents with a run in the project that is not ENDED.
func workingAgents(ctx context.Context, tx *Tx, project string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT DISTINCT agent FROM runs WHERE company_id=? AND project=? AND status!='ENDED'", tx.company, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var a string
		if err = rows.Scan(&a); err != nil {
			return nil, err
		}
		out[a] = true
	}
	return out, rows.Err()
}

// cyclePath finds a chain of edges from stuck agent `from` back to agent `to` (at most maxCycleHops edges), or nil.
// Every agent on the chain before `to` is stuck (not working).
func cyclePath(edges []waitEdge, working map[string]bool, from, to string) []waitEdge {
	type step struct {
		agent string
		path  []waitEdge
	}
	if working[from] {
		return nil
	}
	queue := []step{{from, nil}}
	seen := map[string]bool{from: true}
	for len(queue) > 0 {
		s := queue[0]
		queue = queue[1:]
		if len(s.path) >= maxCycleHops {
			continue
		}
		for _, e := range edges {
			if e.Agent != s.agent {
				continue
			}
			path := append(slices.Clone(s.path), e)
			if e.On == to {
				return path
			}
			if seen[e.On] || working[e.On] {
				continue
			}
			seen[e.On] = true
			queue = append(queue, step{e.On, path})
		}
	}
	return nil
}

// lastMessage is the newest message from sender to recipient in the project ("" id when none).
func lastMessage(ctx context.Context, tx *Tx, project, sender, recipient string) (messaging.Message, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM messages WHERE company_id=? AND project_id=? AND sender=? AND recipient=? ORDER BY seq DESC LIMIT 1", tx.company, project, sender, recipient).Scan(&id)
	if err != nil {
		if isNoRows(err) {
			return messaging.Message{}, nil
		}
		return messaging.Message{}, err
	}
	return messageTx(ctx, tx, id)
}

func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// quoteMessage is a message reference for a refusal: id, time and the start of its text.
func quoteMessage(m messaging.Message) string {
	if m.ID == "" {
		return ""
	}
	return fmt.Sprintf("%s at %s: %q", m.ID, time.Unix(m.CreatedAt, 0).UTC().Format("2006-01-02 15:04 UTC"), shortText(strings.Join(strings.Fields(m.Body), " "), 200))
}

// cycleHint words a wait_cycle refusal for agent `me` about to wait on path[0].Agent, whose chain leads back to me.
func cycleHint(ctx context.Context, tx *Tx, project, me string, path []waitEdge) (string, error) {
	var b strings.Builder
	for i, e := range path {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s's task %s (%s) waits for %s since %s", e.Agent, e.Task, shortText(e.Title, 80), e.waitWords(), time.Unix(e.Since, 0).UTC().Format("2006-01-02 15:04 UTC"))
	}
	last := path[len(path)-1] // the edge that waits on me
	b.WriteString(". Waiting on " + path[0].Agent + " now puts these tasks and yours to sleep with nobody left to write. ")
	switch last.Kind {
	case tasks.ParkApproval:
		fmt.Fprintf(&b, "Decide approval %s now: if %s must do something first, decide it as changes requested and name exactly what is missing; a pending approval never wakes its task.", last.Ref, last.Agent)
	case tasks.ParkAsk, tasks.ParkEscalation:
		fmt.Fprintf(&b, "Answer %s's question %s first, then wait.", last.Agent, last.Ref)
	default:
		m, err := lastMessage(ctx, tx, project, last.Agent, me)
		if err != nil {
			return "", err
		}
		if q := quoteMessage(m); q != "" {
			b.WriteString("Its last message to you: " + q + ". ")
		}
		fmt.Fprintf(&b, "Send %s what it waits for first (factory_message_send recipient=%s task_id=%s), then wait.", last.Agent, last.Agent, last.Task)
	}
	b.WriteString(" If you cannot give it anything, say so in that message, or wait on a time or block instead of waiting on " + path[0].Agent + ".")
	return b.String(), nil
}

// checkWaitCycle refuses agent me's wait on agent on when on is stuck waiting, through at most maxCycleHops agents,
// on me (wait-deadlock-detection D1, D2).
func checkWaitCycle(ctx context.Context, tx *Tx, project, me, on string) error {
	if !waitsOnAgent(on) || on == me {
		return nil
	}
	edges, err := waitEdges(ctx, tx, project)
	if err != nil || len(edges) == 0 {
		return err
	}
	working, err := workingAgents(ctx, tx, project)
	if err != nil {
		return err
	}
	path := cyclePath(edges, working, on, me)
	if path == nil {
		return nil
	}
	hint, err := cycleHint(ctx, tx, project, me, path)
	if err != nil {
		return err
	}
	return refuse("wait_cycle", hint)
}

// checkWaitAsked refuses agent me's wait on a message from agent from when from has nothing pending from me: the
// newest message between them in the project is from `from`, or there is none (wait-deadlock-detection D2). Read
// status is not used: agents act on messages without marking them read. expects_unprompted overrides it.
func checkWaitAsked(ctx context.Context, tx *Tx, project, me, from string, unprompted bool) error {
	if unprompted || !waitsOnAgent(from) {
		return nil
	}
	var sender, id string
	err := tx.QueryRowContext(ctx, `SELECT sender,id FROM messages WHERE company_id=? AND project_id=? AND ((sender=? AND recipient=?) OR (sender=? AND recipient=?))
 ORDER BY seq DESC LIMIT 1`, tx.company, project, me, from, from, me).Scan(&sender, &id)
	if err != nil && !isNoRows(err) {
		return err
	}
	if sender == me {
		return nil
	}
	hint := from + " has nothing pending from you: you have sent it no message in this project"
	if id != "" {
		m, err := messageTx(ctx, tx, id)
		if err != nil {
			return err
		}
		hint = from + " has nothing pending from you: its last message (" + quoteMessage(m) + ") is newer than anything you sent it"
	}
	hint += ". It does not know you are waiting, so no message will come. Send your request first (factory_message_send recipient=" + from +
		", with your task id so the answer wakes this task), then wait. If " + from + " said it will write without being asked, exit again with expects_unprompted: true."
	return refuse("wait_unasked", hint)
}

// approvalDelegate is the agent deciding approval id ("" when people decide it or it is not pending).
func approvalDelegate(ctx context.Context, tx *Tx, id string) (string, error) {
	a, err := getDoc[approvals.Approval](ctx, tx, "SELECT body FROM approvals WHERE company_id=? AND id=?", tx.company, id)
	if errors.Is(err, ErrNotFound) {
		return "", nil
	}
	if err != nil || a.Status != "PENDING" || a.Escalated {
		return "", err
	}
	return a.Delegate, nil
}

// heldTaskHears wakes a task parked as awaiting_approval for one turn on a message filed under it from the approval's
// delegate side: the delegate, or an agent the delegate messaged about the task (its reviewer). The hold stays: done
// and merge are refused until the decision (wait-deadlock-detection D4).
func heldTaskHears(ctx context.Context, tx *Tx, task, recipient, sender, ref, body string) error {
	if task == "" || !waitsOnAgent(sender) {
		return nil
	}
	var approvalID string
	err := tx.QueryRowContext(ctx, "SELECT ref_id FROM task_parks WHERE company_id=? AND task_id=? AND kind=? AND status='active' ORDER BY seq LIMIT 1", tx.company, task, tasks.ParkApproval).Scan(&approvalID)
	if err != nil {
		if isNoRows(err) {
			return nil
		}
		return err
	}
	t, err := taskTx(ctx, tx, task)
	if err != nil || t.Assignee != recipient {
		return err
	}
	delegate, err := approvalDelegate(ctx, tx, approvalID)
	if err != nil || delegate == "" {
		return err
	}
	if ref != "" {
		// The decision itself is a reply to the approval's question: it moves the task on its own.
		m, err := messageTx(ctx, tx, ref)
		if err != nil {
			return err
		}
		if m.InReplyTo != "" {
			q, err := messageTx(ctx, tx, m.InReplyTo)
			if err != nil {
				return err
			}
			if q.ApprovalID == approvalID {
				return nil
			}
		}
	}
	if sender != delegate {
		var asked int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE company_id=? AND task_id=? AND sender=? AND recipient=?", tx.company, task, delegate, sender).Scan(&asked); err != nil {
			return err
		}
		if asked == 0 {
			return nil
		}
	}
	if err = clearParks(ctx, tx, t, tasks.ParkApproval, approvalID, "overridden"); err != nil {
		return err
	}
	return addTrigger(ctx, tx, &t, "message_received", false, sender, ref, "a message from "+sender+" while approval "+approvalID+" (decided by "+delegate+") is still pending: "+body+
		"\nDo what it asks (push, add what is missing, answer), then wait on the approval again (factory_exit_wait approval_id "+approvalID+"); done and the merge stay refused until the decision.")
}

// SweepWaitCycles finds cycles of stuck agents over the company's active parks (wait-deadlock-detection D5). Each task
// of a new cycle wakes once with a wait_cycle trigger naming the other side; a cycle with a task woken that way
// within the last 24 h escalates once instead, to the agents' nearest common manager.
func (s *Store) SweepWaitCycles(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT t.project_id FROM task_parks p JOIN tasks t ON t.company_id=p.company_id AND t.id=p.task_id
 WHERE p.company_id=? AND p.status='active' AND p.kind IN (?,?,?,?) AND t.status NOT IN ('DONE','CANCELLED') ORDER BY t.project_id`,
		company, tasks.ParkMessage, tasks.ParkAsk, tasks.ParkEscalation, tasks.ParkApproval)
	if err != nil {
		return err
	}
	var projects []string
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			rows.Close()
			return err
		}
		projects = append(projects, p)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, p := range projects {
		if err = sweepProjectCycles(ctx, tx, p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func sweepProjectCycles(ctx context.Context, tx *Tx, project string) error {
	edges, err := waitEdges(ctx, tx, project)
	if err != nil || len(edges) < 2 {
		return err
	}
	working, err := workingAgents(ctx, tx, project)
	if err != nil {
		return err
	}
	done := map[string]bool{}
	for _, e := range edges {
		if done[e.Task] || working[e.Agent] {
			continue
		}
		path := cyclePath(edges, working, e.On, e.Agent)
		if path == nil {
			continue
		}
		cycle := append([]waitEdge{e}, path...)
		if slices.ContainsFunc(cycle, func(c waitEdge) bool { return done[c.Task] }) {
			continue
		}
		for _, c := range cycle {
			done[c.Task] = true
		}
		if err = handleCycle(ctx, tx, cycle); err != nil {
			return err
		}
	}
	return nil
}

// handleCycle wakes the cycle's tasks once, or escalates a cycle that formed again within the window.
func handleCycle(ctx context.Context, tx *Tx, cycle []waitEdge) error {
	ids := make([]string, len(cycle))
	for i, c := range cycle {
		ids[i] = c.Task
	}
	since := tx.now().Add(-waitCycleWindow).Unix()
	var again int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM task_triggers WHERE company_id=? AND kind=? AND created_at>=? AND task_id=ANY(?)", tx.company, WaitCycleReason, since, ids).Scan(&again); err != nil {
		return err
	}
	words := make([]string, len(cycle))
	for i, c := range cycle {
		words[i] = fmt.Sprintf("%s's task %s (%s) waits for %s", c.Agent, c.Task, shortText(c.Title, 80), c.waitWords())
	}
	chain := strings.Join(words, "; ")
	if again > 0 {
		var open int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM escalations WHERE company_id=? AND reason=? AND status='open' AND task_id=ANY(?)", tx.company, WaitCycleReason, ids).Scan(&open); err != nil {
			return err
		}
		if open > 0 {
			return nil
		}
		agents := make([]string, len(cycle))
		for i, c := range cycle {
			agents[i] = c.Agent
		}
		summary := "these tasks wait on each other again after they were woken once: " + chain + ". wake runs " + cycle[0].Task + " now without its wait; reassign or cancel it otherwise"
		return openEscalation(ctx, tx, WaitCycleReason, escalationAnswers[WaitCycleReason], EscalationWant{TaskID: cycle[0].Task, RefID: cycle[0].Ref, Summary: summary, Target: commonManager(tx, agents)})
	}
	for _, c := range cycle {
		t, err := taskTx(ctx, tx, c.Task)
		if err != nil {
			return err
		}
		if err = clearParks(ctx, tx, t, c.Kind, c.Ref, "overridden"); err != nil {
			return err
		}
		body := "wait cycle: " + chain + ". Nobody in this chain can move, so Core woke each of you once. Give the agent waiting on you what it waits for (factory_message_send with its task id), or tell it you cannot; do not wait on the same agent again until it has something pending from you."
		if c.Kind == tasks.ParkApproval {
			body += " Your approval " + c.Ref + " is still pending: done and the merge stay refused; wait on it again (factory_exit_wait approval_id " + c.Ref + ") once you have done your part."
		}
		if err = addTrigger(ctx, tx, &t, WaitCycleReason, false, "system", c.Ref, body); err != nil {
			return err
		}
	}
	return nil
}

// commonManager is the nearest manager the agents share (escalation targets up the chain), else the owner.
func commonManager(tx *Tx, agents []string) string {
	chains := make([][]string, 0, len(agents))
	for _, a := range agents {
		var chain []string
		for cur, i := a, 0; i < 8; i++ {
			next := retryTarget(tx, cur)
			if next == "" || next == "owner" || slices.Contains(chain, next) {
				break
			}
			chain = append(chain, next)
			cur = next
		}
		chains = append(chains, chain)
	}
	if len(chains) == 0 {
		return "owner"
	}
	for _, m := range chains[0] {
		shared := true
		for _, c := range chains[1:] {
			if !slices.Contains(c, m) {
				shared = false
				break
			}
		}
		if shared {
			return m
		}
	}
	return "owner"
}

// waitCycleAnswer applies wake to a wait_cycle escalation (handled): the task's agent-cleared parks go and it runs
// now; reassign and cancel continue as for other escalations.
func waitCycleAnswer(ctx context.Context, tx *Tx, t tasks.Task, e Escalation, answer, actor string) (bool, error) {
	if e.Reason != WaitCycleReason || answer != "wake" {
		return false, nil
	}
	for _, kind := range []string{tasks.ParkMessage, tasks.ParkApproval} {
		if err := clearParks(ctx, tx, t, kind, anyRef, "overridden"); err != nil {
			return true, err
		}
	}
	return true, comment(ctx, tx, t.ID, actor, "escalation "+e.Reason+": run now without the wait")
}
