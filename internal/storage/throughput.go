package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Throughput signals (throughput-signals): one sweep compares, per project, queued work with idle node capacity. It
// takes its causes from what Core already knows, and only from there: the reason of every QUEUED run (queuedWhy,
// with each node's answer) and the dependency graph. Runs are grouped by their queued reason and its subject (the
// agent, node, pause or project that holds them), so a reason added to queuedWhy is picked up without a change here.
// A cause that lasts longer than the company's threshold (default 30 minutes) is one episode: one notice to the
// project's manager and one owner alert (task alert kind throughput) until the cause is gone. The sweep never changes
// anything else.

// DefaultThroughputAfter is how long work may wait while capacity is idle or unusable before the notice.
const DefaultThroughputAfter = 30 * time.Minute

// GateMin is how many released tasks must wait on one open task for it to count as a gate.
const GateMin = 3

// ReasonGate is the cause of tasks waiting behind one open dependency.
const ReasonGate = "gate"

// Bottleneck is one cause of queued work while capacity is idle or unusable.
type Bottleneck struct {
	Project string `json:"project"`
	// Key is the cause, stable while it lasts: "<reason>:<subject>" (gate:<task> for a gate).
	Key     string `json:"key"`
	Reason  string `json:"reason"`
	Subject string `json:"subject,omitempty"`
	// Summary says what waits and on what; Fix what would release it.
	Summary string `json:"summary"`
	Fix     string `json:"fix"`
	// Count is the queued runs (the tasks behind a gate); Tasks the affected tasks, the first is the alert's anchor.
	Count int      `json:"count"`
	Tasks []string `json:"tasks"`
	Runs  []string `json:"runs,omitempty"`
	// FreeSlots are the free work slots on usable nodes when the sweep looked.
	FreeSlots int `json:"free_slots"`
	// Since is when the sweep first saw the cause (0: not yet recorded); Due that it lasted past the threshold, so
	// the manager has the notice and the owner the alert.
	Since int64 `json:"since,omitempty"`
	Due   bool  `json:"due,omitempty"`
}

// capacityBound are the reasons that only mean the capacity is in use: they count only while slots are idle.
// Every other reason (a paused, offline, disk-critical or unready node, a pause, a tie to an inactive node, a reason
// added later) is capacity that cannot be used and counts as it is.
func capacityBound(q *QueuedReason) bool {
	switch q.Reason {
	case QueuedNoSlot, QueuedResourceBusy, QueuedAgentBusy:
		return true
	case QueuedAffinity:
		return q.Inactive == ""
	}
	return false
}

// throughputFix is what releases a cause, in words; the reason's own detail says the rest.
func throughputFix(reason, node string) string {
	switch reason {
	case QueuedAgentBusy:
		return "raise the agent's limit (execution.max_concurrent_runs, or its role in company.yaml concurrency) or reassign some of its tasks"
	case QueuedAffinity, QueuedPinned:
		return "bring " + node + " back, or Release now so another node starts the runs fresh"
	case QueuedNodeDiskCritical:
		return "free disk on " + node + " (it resumes by itself), or Release now its tied runs"
	case QueuedNodePaused:
		return "resume " + node + ", or Release now its tied runs"
	case QueuedNodeOffline:
		return "start " + node + " or remove it"
	case QueuedDriverDisabled:
		return "fix the driver on " + node
	case QueuedLabels:
		return "give a node the labels or change the agent's labels"
	case QueuedNoReadyNode, QueuedNotReady:
		return "fix the node's readiness for the project"
	case QueuedNoSlot:
		return "add node slots, or move work off the nodes it can use"
	case QueuedResourceBusy:
		return "the runs take turns on the resource; add capacity of it if the queue is too long"
	case QueuedPaused:
		return "resume when the pause is no longer needed"
	case QueuedNoNode:
		return "enroll a node"
	case QueuedPlanLimit:
		return "upgrade the plan or remove a node: " + node + " is over the account's plan and takes no work"
	case ReasonGate:
		return "specs and designs depend on decisions and earlier specs, not on verifications: drop or move the dependency where the work does not need it, or split the gate"
	}
	return "see the queued reason on the tasks"
}

// ThroughputAfter is the company's threshold.
func (s *Store) ThroughputAfter(ctx context.Context, company string) (time.Duration, error) {
	return throughputAfter(ctx, s.DB, company)
}

func throughputAfter(ctx context.Context, q affinityQuerier, company string) (time.Duration, error) {
	var minutes int64
	err := q.QueryRowContext(ctx, "SELECT throughput_after_minutes FROM companies WHERE id=?", company).Scan(&minutes)
	if errors.Is(err, sql.ErrNoRows) || err == nil && minutes <= 0 {
		return DefaultThroughputAfter, nil
	}
	return time.Duration(minutes) * time.Minute, err
}

// SetThroughputAfter sets the threshold (5 minutes to 24 hours).
func (s *Store) SetThroughputAfter(ctx context.Context, company string, d time.Duration) error {
	if d < 5*time.Minute || d > 24*time.Hour {
		return inputError("throughput_after must be between 5m and 24h")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET throughput_after_minutes=? WHERE id=?", int64(d/time.Minute), company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// Bottlenecks are the company's current causes with when each was first seen (read only; the stall sweep records
// them).
func (s *Store) Bottlenecks(ctx context.Context, company string, waits []TaskWait) ([]Bottleneck, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	bs, err := bottlenecks(ctx, tx, waits)
	if err != nil {
		return nil, err
	}
	seen, err := throughputSeen(ctx, tx)
	if err != nil {
		return nil, err
	}
	after, err := throughputAfter(ctx, tx, company)
	if err != nil {
		return nil, err
	}
	now := tx.now().Unix()
	for i := range bs {
		if r, ok := seen[bs[i].Project+"\x00"+bs[i].Key]; ok {
			bs[i].Since, bs[i].Due = r.since, now-r.since >= int64(after/time.Second)
		}
	}
	return bs, nil
}

type throughputRow struct {
	task  string
	since int64
}

func throughputSeen(ctx context.Context, tx *Tx) (map[string]throughputRow, error) {
	rows, err := tx.QueryContext(ctx, "SELECT project_id,key,task_id,since FROM throughput_causes WHERE company_id=?", tx.company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]throughputRow{}
	for rows.Next() {
		var project, key string
		var r throughputRow
		if err = rows.Scan(&project, &key, &r.task, &r.since); err != nil {
			return nil, err
		}
		out[project+"\x00"+key] = r
	}
	return out, rows.Err()
}

// SweepThroughput records the company's causes: a new cause starts its episode, a gone one ends it (its alert
// clears). A cause past the threshold raises its owner alert and, once per episode, goes into one notice per project
// to the project's manager (its team's lead; the owner's alert alone when there is none). It returns the causes.
func (s *Store) SweepThroughput(ctx context.Context, company string, waits []TaskWait) ([]Bottleneck, error) {
	// The causes are read like QueuedWhy reads a reason, in a transaction of their own that is rolled back: looking
	// changes nothing (a session lock's start is recorded by the claim, not by the sweep).
	bs, err := s.Bottlenecks(ctx, company, waits)
	if err != nil {
		return nil, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	seen, err := throughputSeen(ctx, tx)
	if err != nil {
		return nil, err
	}
	after, err := throughputAfter(ctx, tx, company)
	if err != nil {
		return nil, err
	}
	now := tx.now()
	anchor := map[string]string{} // project/key → the alert's task
	for i := range bs {
		b := &bs[i]
		k := b.Project + "\x00" + b.Key
		r, ok := seen[k]
		delete(seen, k)
		if !ok {
			if len(b.Tasks) == 0 {
				continue // nothing to anchor an alert on (runs without a task): shown, not alerted
			}
			r = throughputRow{task: b.Tasks[0], since: now.Unix()}
			if _, err = tx.ExecContext(ctx, "INSERT INTO throughput_causes(company_id,project_id,key,task_id,since) VALUES(?,?,?,?,?)", company, b.Project, b.Key, r.task, r.since); err != nil {
				return nil, err
			}
		}
		b.Since, b.Due = r.since, now.Unix()-r.since >= int64(after/time.Second)
		anchor[b.Project+"/"+b.Key] = r.task
	}
	for k := range seen {
		project, key, _ := strings.Cut(k, "\x00")
		if _, err = tx.ExecContext(ctx, "DELETE FROM throughput_causes WHERE company_id=? AND project_id=? AND key=?", company, project, key); err != nil {
			return nil, err
		}
	}
	// The owner's alerts: one per due cause, kept while the cause lasts.
	rows, err := tx.QueryContext(ctx, "SELECT id,task_id,ref_id,summary FROM task_alerts WHERE company_id=? AND kind='throughput' AND status='active'", company)
	if err != nil {
		return nil, err
	}
	type active struct{ id, task, summary string }
	current := map[string]active{}
	for rows.Next() {
		var a active
		var ref string
		if err = rows.Scan(&a.id, &a.task, &ref, &a.summary); err != nil {
			rows.Close()
			return nil, err
		}
		current[ref] = a
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	notify := func(taskID, ref, typ, summary string, attention bool) error {
		t, err := taskTx(ctx, tx, taskID)
		if err != nil {
			return err
		}
		return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: typ, EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: now, TaskID: t.ID, Agent: t.Assignee, Status: t.Status, Summary: summary, Attention: attention, Ref: ref})
	}
	fresh := map[string][]Bottleneck{} // project → causes newly due
	for _, b := range bs {
		ref := b.Project + "/" + b.Key
		task, ok := anchor[ref]
		if !b.Due || !ok {
			continue
		}
		if _, ok := current[ref]; ok {
			delete(current, ref)
			continue
		}
		summary := b.Summary + "; " + b.Fix
		if _, err = tx.ExecContext(ctx, "INSERT INTO task_alerts(id,company_id,task_id,kind,ref_id,summary,status,raised_at) VALUES(?,?,?,'throughput',?,?,'active',?)", protocol.ID(), company, task, ref, summary, now.Unix()); err != nil {
			return nil, err
		}
		if err = notify(task, ref, "task.alert.throughput", summary, true); err != nil {
			return nil, err
		}
		fresh[b.Project] = append(fresh[b.Project], b)
	}
	for ref, a := range current {
		if _, err = tx.ExecContext(ctx, "UPDATE task_alerts SET status='cleared',cleared_at=? WHERE id=?", now.Unix(), a.id); err != nil {
			return nil, err
		}
		if err = notify(a.task, ref, "task.alert_cleared.throughput", a.summary, false); err != nil {
			return nil, err
		}
	}
	for _, project := range slices.Sorted(maps.Keys(fresh)) {
		lead := leadOf(ctx, tx, project)
		if !isAgent(lead) {
			continue
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Throughput: work in project %s has waited for %d minutes while node capacity is idle or unusable. Factory changes nothing itself; act within your authority (raise limits, reorder, split gates) or tell the owner what needs them:\n", project, int(after/time.Minute))
		for _, c := range fresh[project] {
			fmt.Fprintf(&b, "- %s: %s\n", c.Summary, c.Fix)
		}
		if _, err = sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, Sender: "factory", Recipient: lead, MessageType: "notification", Body: strings.TrimSpace(b.String()), RequestID: "throughput-" + project}); err != nil {
			return nil, err
		}
	}
	return bs, tx.Commit()
}

// TaskWait is a task that waits for capacity without a queued run (its verdict is capacity: the agent's limit, the
// project workspace, a busy tied node, full nodes); Core evaluates it with the catalog and passes it in.
type TaskWait struct {
	Project string
	TaskID  string
	Wait    tasks.Wait
	Detail  string
}

// waitReason maps a capacity wait to the queued reason of the same cause and its subject, so a task waiting for the
// agent's limit and a run queued for it are one cause.
func waitReason(w TaskWait) (reason, subject string) {
	switch w.Wait.Kind {
	case tasks.WaitAgent, tasks.WaitAgentProject, tasks.WaitAgentConversation:
		return QueuedAgentBusy, w.Wait.Agent
	case tasks.WaitPinnedNode:
		return QueuedPinned, w.Wait.Node
	case tasks.WaitNodes:
		return QueuedNoSlot, ""
	}
	return "capacity_" + w.Wait.Kind, w.Project
}

// bottlenecks computes the company's causes now from the queued runs, the capacity waits and the dependency graph.
func bottlenecks(ctx context.Context, tx *Tx, waits []TaskWait) ([]Bottleneck, error) {
	free, err := idleSlots(ctx, tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id,coalesce(task_id,''),project,spec FROM runs WHERE company_id=? AND status='QUEUED' AND revoked=0 ORDER BY created_at,seq", tx.company)
	if err != nil {
		return nil, err
	}
	type queued struct {
		id, task, project string
		spec              protocol.RunSpec
	}
	var runs []queued
	for rows.Next() {
		var r queued
		var b []byte
		if err = rows.Scan(&r.id, &r.task, &r.project, &b); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(b, &r.spec)
		runs = append(runs, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	type group struct {
		b      Bottleneck
		first  *QueuedReason // the first queued run's reason (nil: only task waits)
		detail string        // the first task wait's detail
		limit  int
		runs   int
	}
	groups := map[string]*group{}
	var order []string
	add := func(project, reason, subject, task, run string) *group {
		key := reason
		if subject != "" {
			key += ":" + subject
		}
		k := project + "\x00" + key
		g, ok := groups[k]
		if !ok {
			g = &group{b: Bottleneck{Project: project, Key: key, Reason: reason, Subject: subject, Tasks: []string{}, FreeSlots: free}}
			groups[k] = g
			order = append(order, k)
		}
		if task == "" || !slices.Contains(g.b.Tasks, task) {
			g.b.Count++
		}
		if run != "" {
			g.b.Runs = append(g.b.Runs, run)
			g.runs++
		}
		if task != "" && !slices.Contains(g.b.Tasks, task) {
			g.b.Tasks = append(g.b.Tasks, task)
		}
		return g
	}
	for _, r := range runs {
		q, err := queuedWhy(ctx, tx, r.id)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if q == nil || q.Reason == QueuedUnclaimed || capacityBound(q) && free == 0 {
			continue
		}
		g := add(r.project, q.Reason, q.Subject, r.task, r.id)
		if g.first == nil {
			g.first = q
			if q.Reason == QueuedAgentBusy {
				g.limit, _, _ = AgentRunCaps(r.spec.ConfigSnapshot).For(r.spec.Conversation)
			}
		}
	}
	// Capacity waits count only while slots are idle: otherwise the capacity is simply in use.
	for _, w := range waits {
		if free == 0 || w.TaskID == "" {
			continue
		}
		reason, subject := waitReason(w)
		g := add(w.Project, reason, subject, w.TaskID, "")
		if g.detail == "" {
			g.detail = w.Detail
		}
		if g.limit == 0 {
			g.limit = w.Wait.Limit
		}
	}
	var out []Bottleneck
	for _, k := range order {
		g := groups[k]
		b, q := g.b, g.first
		if q == nil {
			q = &QueuedReason{Reason: b.Reason, Detail: g.detail, Subject: b.Subject}
		}
		node := b.Subject
		if q.Tie != nil {
			node = q.Tie.Label()
		} else if i := slices.IndexFunc(q.Nodes, func(n QueuedNode) bool { return n.Node == b.Subject }); i >= 0 {
			node = nodeLabel(q.Nodes[i].Name, q.Nodes[i].Node)
		}
		what := plural(b.Count, "run", "runs")
		if g.runs == 0 {
			what = plural(b.Count, "task", "tasks")
		}
		switch {
		case q.Reason == QueuedAgentBusy:
			b.Summary = fmt.Sprintf("%s: %d queued, limit %d, %s", b.Subject, b.Count, max(g.limit, 1), plural(free, "free slot", "free slots"))
		case q.Tie != nil:
			state := q.Inactive
			if i := slices.IndexFunc(q.Nodes, func(n QueuedNode) bool { return n.Node == q.Tie.Node }); i >= 0 && q.Nodes[i].Reason != "" {
				state = strings.Join(nonEmpty(q.Inactive, q.Nodes[i].Detail), ", ")
			}
			b.Summary = fmt.Sprintf("%s wait for %s", what, node)
			if state != "" {
				b.Summary += ": " + state
			}
			if q.Until > 0 {
				b.Summary += "; released by itself at " + time.Unix(q.Until, 0).UTC().Format("15:04 UTC")
			}
		default:
			b.Summary = fmt.Sprintf("%s wait (%s): %s", what, strings.ReplaceAll(q.Reason, "_", " "), q.Detail)
		}
		b.Summary += taskList(b.Tasks)
		fix := q.Reason
		if q.Tie != nil {
			// A tie is released by what holds its node: a critical disk, a pause, an offline node.
			if i := slices.IndexFunc(q.Nodes, func(n QueuedNode) bool { return n.Node == q.Tie.Node }); i >= 0 && q.Nodes[i].Reason != "" && !capacityBound(&QueuedReason{Reason: q.Nodes[i].Reason}) {
				fix = q.Nodes[i].Reason
			}
		}
		b.Fix = throughputFix(fix, node)
		out = append(out, b)
	}
	if free > 0 {
		gates, err := gateBottlenecks(ctx, tx, free)
		if err != nil {
			return nil, err
		}
		out = append(out, gates...)
	}
	slices.SortStableFunc(out, func(a, b Bottleneck) int {
		if a.Count != b.Count {
			return b.Count - a.Count
		}
		return strings.Compare(a.Project+a.Key, b.Project+b.Key)
	})
	return out, nil
}

// gateBottlenecks are the open tasks at least GateMin released tasks wait on directly, with every task behind them.
func gateBottlenecks(ctx context.Context, tx *Tx, free int) ([]Bottleneck, error) {
	rows, err := tx.QueryContext(ctx, `SELECT d.task_id,d.depends_on,w.status FROM task_dependencies d
 JOIN tasks w ON w.company_id=d.company_id AND w.id=d.task_id JOIN tasks g ON g.company_id=d.company_id AND g.id=d.depends_on
 WHERE d.company_id=? AND w.status NOT IN ('DONE','CANCELLED') AND g.status NOT IN ('DONE','CANCELLED') ORDER BY d.task_id,d.depends_on`, tx.company)
	if err != nil {
		return nil, err
	}
	behind := map[string][]string{} // dependency → open tasks waiting on it
	direct := map[string][]string{} // dependency → released tasks waiting on it
	for rows.Next() {
		var task, dep, status string
		if err = rows.Scan(&task, &dep, &status); err != nil {
			rows.Close()
			return nil, err
		}
		behind[dep] = append(behind[dep], task)
		if status != "BACKLOG" {
			direct[dep] = append(direct[dep], task)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	var out []Bottleneck
	for _, gate := range slices.Sorted(maps.Keys(direct)) {
		waiting := direct[gate]
		if len(waiting) < GateMin {
			continue
		}
		g, err := taskTx(ctx, tx, gate)
		if err != nil {
			return nil, err
		}
		// Everything behind the gate, transitively.
		all, seen := []string{}, map[string]bool{gate: true}
		for queue := slices.Clone(behind[gate]); len(queue) > 0; queue = queue[1:] {
			if t := queue[0]; !seen[t] {
				seen[t] = true
				all = append(all, t)
				queue = append(queue, behind[t]...)
			}
		}
		who := g.Status
		if g.Assignee != "" {
			who += ", " + g.Assignee
		}
		summary := fmt.Sprintf("%d tasks wait behind %s %q (%s)", len(waiting), gate, g.Title, who)
		if len(all) > len(waiting) {
			summary += fmt.Sprintf(", %d in all", len(all))
		}
		out = append(out, Bottleneck{Project: g.ProjectID, Key: ReasonGate + ":" + gate, Reason: ReasonGate, Subject: gate, Summary: summary + taskList(waiting), Fix: throughputFix(ReasonGate, ""),
			Count: len(waiting), Tasks: append([]string{gate}, waiting...), FreeSlots: free})
	}
	return out, nil
}

// idleSlots are the free work slots on the nodes that take work now: online, not paused by the owner, disk not
// critical, within the account's plan.
func idleSlots(ctx context.Context, tx *Tx) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT n.id,n.epoch,n.slots,n.health FROM nodes n WHERE n.company_id=? AND n.online=1
 AND NOT EXISTS (SELECT 1 FROM pauses p WHERE p.company_id=n.company_id AND p.scope='node' AND p.target=n.id) ORDER BY n.id`, tx.company)
	if err != nil {
		return 0, err
	}
	type node struct {
		id     string
		epoch  int64
		slots  int
		health []byte
	}
	var nodes []node
	for rows.Next() {
		var n node
		if err = rows.Scan(&n.id, &n.epoch, &n.slots, &n.health); err != nil {
			rows.Close()
			return 0, err
		}
		nodes = append(nodes, n)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return 0, err
	}
	free := 0
	for _, n := range nodes {
		if diskPaused(n.health, false) {
			continue
		}
		// A node over the account's plan (saas-billing) takes no work: its slots are not idle capacity.
		if over, err := tx.store.nodeOverPlan(ctx, tx, tx.company, n.id); err != nil {
			return 0, err
		} else if over != "" {
			continue
		}
		l, err := nodeLoad(ctx, tx, n.id, n.epoch, n.slots)
		if err != nil {
			return 0, err
		}
		free += max(n.slots-l.Held, 0)
	}
	return free, nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func nonEmpty(xs ...string) []string {
	var out []string
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// taskList names the affected tasks, at most five.
func taskList(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	if len(ids) > 5 {
		return fmt.Sprintf(" (tasks %s and %d more)", strings.Join(ids[:5], ", "), len(ids)-5)
	}
	return " (tasks " + strings.Join(ids, ", ") + ")"
}
