package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Duplicate warnings (task-duplicate-warnings): a task records the work it covers (covers, inferred from its text
// when it names none) and the tasks it replaces; after a create or a relevant update Core compares it with the
// open tasks of its project and answers warnings. A warning never refuses or delays the mutation. Unanswered
// strong warnings are kept (task_duplicates) and shown on both tasks and in the creator's feed until one task
// closes or a side marks the pair not_duplicate_of.

// openTaskSQL selects the stored statuses of an open task.
const openTaskSQL = "status NOT IN ('DONE','CANCELLED')"

// knownChanges lists the OpenSpec changes Core knows for the project: its tasks' links and the changes done
// exits reported from the checkout.
func knownChanges(ctx context.Context, tx *Tx, project string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT c FROM (
 SELECT body->>'openspec_change' AS c FROM tasks WHERE company_id=? AND project_id=? AND COALESCE(body->>'openspec_change','')<>''
 UNION SELECT change AS c FROM openspec_changes WHERE company_id=? AND project_id=?) k ORDER BY c`, tx.company, project, tx.company, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// coverTask sets the task's covered keys: the explicit ones normalized, or (none given) the keys inferred from its
// title and description. When the task links no change and exactly one known change is named (in its text or
// its keys), inferChange links it, marked inferred.
func coverTask(ctx context.Context, tx *Tx, t *tasks.Task, explicit []string, inferChange bool) error {
	if t.Kind == tasks.KindConversation {
		return nil
	}
	known, err := knownChanges(ctx, tx, t.ProjectID)
	if err != nil {
		return err
	}
	if len(explicit) > 0 {
		if t.Covers, err = tasks.NormalizeCovers(explicit); err != nil {
			return inputError(err.Error())
		}
		t.CoversInferred = false
	}
	infer := len(explicit) == 0
	keys, named := tasks.InferCovers(t.Title, t.Description, t.OpenSpecChange, known)
	if inferChange && t.OpenSpecChange == "" {
		for _, k := range append(slices.Clone(keys), t.Covers...) {
			if c := tasks.CoverChange(k); c != "" && slices.Contains(known, c) && !slices.Contains(named, c) {
				named = append(named, c)
			}
		}
		if len(named) == 1 {
			t.OpenSpecChange, t.OpenSpecChangeInferred = named[0], true
			if infer {
				keys, _ = tasks.InferCovers(t.Title, t.Description, t.OpenSpecChange, known)
			}
		}
	}
	if infer {
		t.Covers, t.CoversInferred = keys, len(keys) > 0
	}
	return nil
}

// replaceable lists the tasks a create or update names in replaces after checking them, and adds them to
// t.Replaces. A done task cannot be replaced.
func replaceable(ctx context.Context, tx *Tx, t *tasks.Task, ids []string) ([]tasks.Task, error) {
	if len(ids) > 32 {
		return nil, inputError("replaces: at most 32 tasks")
	}
	var out []tasks.Task
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == t.ID || id == "" {
			return nil, inputError("replaces: a task does not replace itself")
		}
		p, err := taskTx(ctx, tx, id)
		if err != nil {
			return nil, fmt.Errorf("replaces %s: %w", id, err)
		}
		if p.ProjectID != t.ProjectID || p.Kind == tasks.KindConversation {
			return nil, inputError("replaces: " + id + " is not a task of this project")
		}
		if p.Status == "DONE" {
			return nil, inputError("replaces: " + id + " is done; link follow-up work with covers instead")
		}
		if !slices.Contains(t.Replaces, id) {
			t.Replaces = append(t.Replaces, id)
		}
		out = append(out, p)
	}
	return out, nil
}

// replaceTasks cancels each replaced open task for t when the actor may cancel it (it created the task or has
// authority over it, its policy allows cancel, the task is not running, has no open children and is not the
// run's own task), links both ways and moves the replaced task's dependents to t. A task cancelled earlier for
// another replacement gets t as one more replacement. A task it may not cancel stays open (original_open).
func replaceTasks(ctx context.Context, tx *Tx, t tasks.Task, replaced []tasks.Task, actor string, cancelAllowed bool) error {
	for _, p := range replaced {
		switch {
		case p.Status == "CANCELLED":
			if len(p.ReplacedBy) == 0 || slices.Contains(p.ReplacedBy, t.ID) {
				continue // cancelled for its own reasons: nothing to move
			}
			from := append([]string{p.ID}, p.ReplacedBy...)
			p.ReplacedBy = append(p.ReplacedBy, t.ID)
			if err := comment(ctx, tx, p.ID, actorName(actor), "also replaced by "+t.ID+" ("+t.Title+")"); err != nil {
				return err
			}
			if err := saveTaskNote(ctx, tx, &p, "task.updated", "replaced by "+strings.Join(p.ReplacedBy, ", ")); err != nil {
				return err
			}
			if err := moveDependents(ctx, tx, from, p.ID, t); err != nil {
				return err
			}
		default:
			ok, err := mayReplace(ctx, tx, t, p, actor, cancelAllowed)
			if err != nil {
				return err
			}
			if !ok {
				continue // it stays open: original_open
			}
			if p, err = cancelTx(ctx, tx, p, actorName(actor), "replaced by "+t.ID+" ("+t.Title+")", false); err != nil {
				return err
			}
			p.ReplacedBy = append(p.ReplacedBy, t.ID)
			if err = saveTaskNote(ctx, tx, &p, "task.updated", "replaced by "+t.ID); err != nil {
				return err
			}
			if err = moveDependents(ctx, tx, []string{p.ID}, p.ID, t); err != nil {
				return err
			}
		}
	}
	return nil
}

// mayReplace: the actor may cancel p for t now.
func mayReplace(ctx context.Context, tx *Tx, t, p tasks.Task, actor string, cancelAllowed bool) (bool, error) {
	if !isOwner(actor) && (!cancelAllowed || p.CreatedBy != actor && !hasAuthorityTx(ctx, tx, p, actor)) {
		return false, nil
	}
	if p.ID == t.OriginTask || p.CurrentRunID != "" && editableRun(ctx, tx, p) != nil {
		return false, nil
	}
	switch p.Status {
	case "BACKLOG", "READY", "BLOCKED", "REWORK":
	default:
		return false, nil
	}
	children, err := openChildren(ctx, tx, p.ID)
	return len(children) == 0, err
}

// moveDependents makes the open tasks that wait on any of from (the replaced task and its earlier replacements)
// wait on t instead of the replaced task, unless that would be a cycle.
func moveDependents(ctx context.Context, tx *Tx, from []string, replaced string, t tasks.Task) error {
	list, _ := json.Marshal(from)
	dependents, err := listDocs[tasks.Task](ctx, tx, `SELECT t.body FROM tasks t WHERE t.company_id=? AND t.`+openTaskSQL+` AND t.id IN
 (SELECT d.task_id FROM task_dependencies d WHERE d.company_id=? AND d.depends_on IN (SELECT jsonb_array_elements_text(?::jsonb))) ORDER BY t.created_at,t.id`, tx.company, tx.company, string(list))
	if err != nil {
		return err
	}
	for _, d := range dependents {
		if d.ID == t.ID {
			continue
		}
		cycle, err := waitsFor(ctx, tx, t.ID, d.ID)
		if err != nil {
			return err
		}
		deps := slices.DeleteFunc(slices.Clone(d.Dependencies), func(id string) bool { return id == replaced })
		if !cycle && !slices.Contains(deps, t.ID) {
			deps = append(deps, t.ID)
		}
		if slices.Equal(deps, d.Dependencies) {
			continue
		}
		d.Dependencies = deps
		if err = dependencies(ctx, tx, d); err != nil {
			return err
		}
		if err = saveTaskNote(ctx, tx, &d, "task.updated", "dependency "+replaced+" replaced by "+t.ID); err != nil {
			return err
		}
	}
	return nil
}

// duplicateCheck compares t with the open tasks of its project and returns its warnings, strong ones first. The
// strong ones are recorded (an attention feed row for each new pair); a pair marked not duplicate never warns.
// Tasks created by the same run or under the same parent are not compared, except for original_open.
func duplicateCheck(ctx context.Context, tx *Tx, t tasks.Task) ([]tasks.DuplicateWarning, error) {
	if t.Kind == tasks.KindConversation || t.Status == "DONE" || t.Status == "CANCELLED" {
		return nil, nil
	}
	open, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND project_id=? AND id<>? AND "+openTaskSQL+" AND COALESCE(body->>'kind','')<>'conversation' ORDER BY created_at,id", tx.company, t.ProjectID, t.ID)
	if err != nil {
		return nil, err
	}
	pairs, err := duplicatePairs(ctx, tx, t.ID)
	if err != nil {
		return nil, err
	}
	title := tasks.DuplicateTitle(t.Title)
	var strong, related []tasks.DuplicateWarning
	for _, o := range open {
		if pairs[o.ID] == "not_duplicate" {
			continue
		}
		sibling := t.OriginRun != "" && o.OriginRun == t.OriginRun || t.ParentTaskID != "" && o.ParentTaskID == t.ParentTaskID
		w := tasks.DuplicateWarning{TaskID: o.ID, Title: o.Title, Status: o.Status, Assignee: o.Assignee}
		if !sibling {
			if w.Keys = tasks.SharedCovers(t.Covers, o.Covers); len(w.Keys) > 0 {
				w.Reasons = append(w.Reasons, tasks.ReasonSameWorkItem)
			}
			if title != "" && title == tasks.DuplicateTitle(o.Title) && t.Assignee == o.Assignee {
				w.Reasons = append(w.Reasons, tasks.ReasonSameTitle)
			}
		}
		original, err := originalOpen(ctx, tx, t, o)
		if err != nil {
			return nil, err
		}
		if original {
			w.Reasons = append(w.Reasons, tasks.ReasonOriginalOpen)
		}
		if len(w.Reasons) > 0 {
			strong = append(strong, w)
		} else if !sibling && t.OpenSpecChange != "" && o.OpenSpecChange == t.OpenSpecChange && t.Assignee != "" && o.Assignee == t.Assignee && len(related) < tasks.MaxSameChangeOpen {
			w.Reasons = []string{tasks.ReasonSameChangeOpen}
			related = append(related, w)
		}
	}
	if err = recordDuplicates(ctx, tx, t, strong, pairs); err != nil {
		return nil, err
	}
	return append(strong, related...), nil
}

// originalOpen: o is a task t replaces that stayed open, or t's parent, READY to run again without waiting for
// its children, that t's run is not working on (a split that left the original to run). One task of a run warns
// about a given parent.
func originalOpen(ctx context.Context, tx *Tx, t, o tasks.Task) (bool, error) {
	if slices.Contains(t.Replaces, o.ID) {
		return true, nil
	}
	if o.ID != t.ParentTaskID || o.ID == t.OriginTask || o.Status != "READY" {
		return false, nil
	}
	var held bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_parks WHERE company_id=? AND task_id=? AND status='active' AND kind='children')
 OR EXISTS(SELECT 1 FROM task_duplicates d JOIN tasks s ON s.company_id=d.company_id AND s.id=d.task_id WHERE d.company_id=? AND d.other_id=? AND d.task_id<>?
  AND d.status='open' AND ?<>'' AND (s.body->>'origin_run')=?)`, tx.company, o.ID, tx.company, o.ID, t.ID, t.OriginRun, t.OriginRun).Scan(&held)
	return !held, err
}

// duplicatePairs maps the tasks paired with id (either direction) to the pair's status: open or not_duplicate.
func duplicatePairs(ctx context.Context, tx *Tx, id string) (map[string]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT task_id,other_id,status FROM task_duplicates WHERE company_id=? AND (task_id=? OR other_id=?)", tx.company, id, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var a, b, status string
		if err = rows.Scan(&a, &b, &status); err != nil {
			return nil, err
		}
		other := b
		if b == id {
			other = a
		}
		if out[other] != "not_duplicate" {
			out[other] = status
		}
	}
	return out, rows.Err()
}

// recordDuplicates replaces t's open warnings with strong; a pair new to it gets an attention row in the feed
// of t's creator (its subtree feed), which ends with the warning.
func recordDuplicates(ctx context.Context, tx *Tx, t tasks.Task, strong []tasks.DuplicateWarning, pairs map[string]string) error {
	current := map[string]bool{}
	for _, w := range strong {
		current[w.TaskID] = true
	}
	rows, err := tx.QueryContext(ctx, "SELECT other_id FROM task_duplicates WHERE company_id=? AND task_id=? AND status='open'", tx.company, t.ID)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var o string
		if err = rows.Scan(&o); err != nil {
			rows.Close()
			return err
		}
		if !current[o] {
			gone = append(gone, o)
		}
	}
	rows.Close()
	for _, o := range gone {
		if _, err = tx.ExecContext(ctx, "DELETE FROM task_duplicates WHERE company_id=? AND task_id=? AND other_id=? AND status='open'", tx.company, t.ID, o); err != nil {
			return err
		}
		if err = quietDuplicateFeed(ctx, tx, t.ID, o); err != nil {
			return err
		}
	}
	for _, w := range strong {
		reasons, keys := strings.Join(w.Reasons, ","), strings.Join(w.Keys, ",")
		res, err := tx.ExecContext(ctx, "UPDATE task_duplicates SET reasons=?,keys=? WHERE company_id=? AND status='open' AND (task_id=? AND other_id=? OR task_id=? AND other_id=?)", reasons, keys, tx.company, t.ID, w.TaskID, w.TaskID, t.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO task_duplicates(company_id,task_id,other_id,reasons,keys,status,created_by,created_at) VALUES(?,?,?,?,?,'open',?,?)", tx.company, t.ID, w.TaskID, reasons, keys, t.CreatedBy, tx.now().Unix()); err != nil {
			return err
		}
		agent := t.CreatedBy
		if isOwner(agent) {
			agent = ""
		}
		why := strings.Join(w.Reasons, ", ")
		if len(w.Keys) > 0 {
			why += " (" + strings.Join(w.Keys, ", ") + ")"
		}
		summary := fmt.Sprintf("possible duplicate: %s %q and %s %q (%s, %s): %s. Cancel one, update the existing task, or mark them not_duplicate_of", t.ID, t.Title, w.TaskID, w.Title, w.Status, dashed(w.Assignee), why)
		if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.possible_duplicate", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: agent, Actor: "system", Status: t.Status, Summary: summary, Attention: true, Ref: duplicateRef(t.ID, w.TaskID)}); err != nil {
			return err
		}
	}
	return nil
}

func dashed(s string) string {
	if s == "" {
		return "unassigned"
	}
	return s
}

func duplicateRef(a, b string) string { return "duplicate:" + a + ":" + b }

// quietDuplicateFeed ends the attention of the pair's feed rows (either direction).
func quietDuplicateFeed(ctx context.Context, tx *Tx, a, b string) error {
	_, err := tx.ExecContext(ctx, "UPDATE feed_events SET attention=0 WHERE company_id=? AND kind='task.possible_duplicate' AND ref IN (?,?)", tx.company, duplicateRef(a, b), duplicateRef(b, a))
	return err
}

// markNotDuplicate records that t and each of ids are not duplicates: their warnings end and never return.
func markNotDuplicate(ctx context.Context, tx *Tx, t tasks.Task, ids []string, actor string) error {
	if len(ids) > 64 {
		return inputError("not_duplicate_of: at most 64 tasks")
	}
	for _, id := range ids {
		if id == t.ID {
			return inputError("not_duplicate_of: a task is not compared with itself")
		}
		o, err := taskTx(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("not_duplicate_of %s: %w", id, err)
		}
		if o.ProjectID != t.ProjectID {
			return inputError("not_duplicate_of: " + id + " is not a task of this project")
		}
		res, err := tx.ExecContext(ctx, "UPDATE task_duplicates SET status='not_duplicate',decided_by=?,decided_at=? WHERE company_id=? AND (task_id=? AND other_id=? OR task_id=? AND other_id=?)", actorName(actor), tx.now().Unix(), tx.company, t.ID, id, id, t.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err = tx.ExecContext(ctx, "INSERT INTO task_duplicates(company_id,task_id,other_id,status,created_by,created_at,decided_by,decided_at) VALUES(?,?,?,'not_duplicate',?,?,?,?)", tx.company, t.ID, id, actorName(actor), tx.now().Unix(), actorName(actor), tx.now().Unix()); err != nil {
				return err
			}
		}
		if err = quietDuplicateFeed(ctx, tx, t.ID, id); err != nil {
			return err
		}
		if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.not_duplicate", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: t.Assignee, Actor: actorName(actor), Status: t.Status,
			Summary: fmt.Sprintf("%s and %s are not duplicates (marked by %s)", t.ID, id, actorName(actor)), Ref: duplicateRef(t.ID, id)}); err != nil {
			return err
		}
	}
	return nil
}

// closedDuplicates ends the open warnings of a task that closed.
func closedDuplicates(ctx context.Context, tx *Tx, id string) error {
	rows, err := tx.QueryContext(ctx, "DELETE FROM task_duplicates WHERE company_id=? AND status='open' AND (task_id=? OR other_id=?) RETURNING task_id,other_id", tx.company, id, id)
	if err != nil {
		return err
	}
	var pairs [][2]string
	for rows.Next() {
		var a, b string
		if err = rows.Scan(&a, &b); err != nil {
			rows.Close()
			return err
		}
		pairs = append(pairs, [2]string{a, b})
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, p := range pairs {
		if err = quietDuplicateFeed(ctx, tx, p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}

// presentDuplicates sets possible_duplicates (open warnings with another open task) and not_duplicate_of on
// open tasks, with one query for the list.
func presentDuplicates(ctx context.Context, q querier, company string, ts []*tasks.Task) error {
	ids := []string{}
	byID := map[string][]*tasks.Task{}
	for _, t := range ts {
		if t.Status == "DONE" || t.Status == "CANCELLED" {
			continue
		}
		t.PossibleDuplicates, t.NotDuplicateOf = nil, nil
		if _, ok := byID[t.ID]; !ok {
			ids = append(ids, t.ID)
		}
		byID[t.ID] = append(byID[t.ID], t)
	}
	if len(ids) == 0 {
		return nil
	}
	list, _ := json.Marshal(ids)
	rows, err := q.QueryContext(ctx, `SELECT d.task_id,d.other_id,d.reasons,d.keys,d.status,a.body->>'title',a.status,a.assignee,b.body->>'title',b.status,b.assignee
 FROM task_duplicates d JOIN tasks a ON a.company_id=d.company_id AND a.id=d.task_id JOIN tasks b ON b.company_id=d.company_id AND b.id=d.other_id
 WHERE d.company_id=? AND (d.task_id IN (SELECT jsonb_array_elements_text(?::jsonb)) OR d.other_id IN (SELECT jsonb_array_elements_text(?::jsonb)))
 ORDER BY d.created_at,d.task_id,d.other_id`, company, string(list), string(list))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, reasons, keys, status, aTitle, aStatus, aAssignee, bTitle, bStatus, bAssignee string
		if err = rows.Scan(&a, &b, &reasons, &keys, &status, &aTitle, &aStatus, &aAssignee, &bTitle, &bStatus, &bAssignee); err != nil {
			return err
		}
		open := aStatus != "DONE" && aStatus != "CANCELLED" && bStatus != "DONE" && bStatus != "CANCELLED"
		side := func(self, other, title, st, assignee string) {
			for _, t := range byID[self] {
				if status == "not_duplicate" {
					if !slices.Contains(t.NotDuplicateOf, other) {
						t.NotDuplicateOf = append(t.NotDuplicateOf, other)
					}
					continue
				}
				if open {
					t.PossibleDuplicates = append(t.PossibleDuplicates, tasks.DuplicateWarning{TaskID: other, Title: title, Status: st, Assignee: assignee, Reasons: splitList(reasons), Keys: splitList(keys)})
				}
			}
		}
		side(a, b, bTitle, bStatus, bAssignee)
		side(b, a, aTitle, aStatus, aAssignee)
	}
	return rows.Err()
}

func splitList(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
