package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/readiness"
	"appfactory/internal/tasks"
)

// Node readiness (node-readiness): a node with a readiness fault for a project (it cannot clone the repository, lacks
// a required tool, or a run of the project failed there before its agent started) gets no runs of that project
// until its check passes again. Core asks for the check in the heartbeat answer (checks) and the node reports the
// results in its heartbeat (readiness). A node Core has no row for counts as ready: an older node never reports.

// ReadinessRecheck is how often Core asks a faulted node to check again by itself; "Check again" asks at once.
var ReadinessRecheck = 10 * time.Minute

// ProjectReadiness is a node's readiness for one project as the Nodes page shows it.
type ProjectReadiness struct {
	Project   string               `json:"project"`
	Ready     bool                 `json:"ready"`
	Faults    []protocol.NodeFault `json:"faults"`
	Source    string               `json:"source"` // probe: the node's check; run: a failed run (RunID)
	RunID     string               `json:"run_id,omitempty"`
	Since     int64                `json:"since,omitempty"`
	CheckedAt int64                `json:"checked_at,omitempty"`
	// Pending: Core asked for a check the node has not answered yet.
	Pending bool `json:"pending,omitempty"`
}

type readinessQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// nodeNotReady says why node is not ready for project ("" when it is, or Core knows of no fault).
func nodeNotReady(ctx context.Context, q readinessQuerier, company, node, project string) (string, error) {
	if project == "" {
		return "", nil
	}
	var b []byte
	err := q.QueryRowContext(ctx, "SELECT faults FROM node_readiness WHERE company_id=? AND node_id=? AND project_id=? AND faults!='[]'::jsonb", company, node, project).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var fs []protocol.NodeFault
	_ = json.Unmarshal(b, &fs)
	return faultText(fs), nil
}

// faultText is the faults with their fixes in one line.
func faultText(fs []protocol.NodeFault) string {
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		p := f.Detail
		if f.Fix != "" {
			p += " (fix: " + f.Fix + ")"
		}
		parts = append(parts, p)
	}
	if len(parts) == 0 {
		return "not ready"
	}
	return strings.Join(parts, "; ")
}

// recordRunFault charges a run's node-side failure to its node: the project's runs stay off the node until a check
// newer than this run passes. A repository or tool fault is re-checked at once (the check tests exactly that); a
// runtime, sandbox or worktree fault only after ReadinessRecheck or "Check again", since the project's check cannot
// see it and an early pass would hand the node the next run to fail.
func recordRunFault(ctx context.Context, tx *Tx, node, project, runID string, f protocol.NodeFault) error {
	if node == "" || project == "" {
		return nil
	}
	now := tx.now().Unix()
	recheck := now + int64(ReadinessRecheck/time.Second)
	if f.Kind == protocol.FaultRepoAccess || f.Kind == protocol.FaultTool {
		recheck = now
	}
	var was []byte
	err := tx.QueryRowContext(ctx, "SELECT faults FROM node_readiness WHERE company_id=? AND node_id=? AND project_id=?", tx.company, node, project).Scan(&was)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	ready := errors.Is(err, sql.ErrNoRows) || string(was) == "[]"
	faults, _ := json.Marshal([]protocol.NodeFault{f})
	if _, err = tx.ExecContext(ctx, `INSERT INTO node_readiness(company_id,node_id,project_id,generation,min_generation,recheck_at,faults,source,run_id,since)
 VALUES(?,?,?,1,2,?,?,'run',?,?) ON CONFLICT(company_id,node_id,project_id) DO UPDATE SET min_generation=node_readiness.generation+1,recheck_at=excluded.recheck_at,
 faults=excluded.faults,source='run',run_id=excluded.run_id,since=CASE WHEN node_readiness.faults='[]'::jsonb THEN excluded.since ELSE node_readiness.since END`,
		tx.company, node, project, recheck, string(faults), runID, now); err != nil {
		return err
	}
	if !ready {
		return nil
	}
	name := nodeName(ctx, tx, node)
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "node.not_ready", EntityID: "node:" + node, ProjectID: project, CreatedAt: tx.now(), Actor: "node", RunID: runID, Ref: project, Attention: true,
		Summary: fmt.Sprintf("node %s is not ready for project %s: %s; its runs of the project go to other nodes until it passes its check", nodeLabel(name, node), project, faultText([]protocol.NodeFault{f}))})
}

func nodeName(ctx context.Context, tx *Tx, node string) string {
	var name string
	_ = tx.QueryRowContext(ctx, "SELECT name FROM nodes WHERE company_id=? AND id=?", tx.company, node).Scan(&name)
	return name
}

// projectChecks is the heartbeat answer's checks: every live project with a repository or requirements, with the
// generation Core wants checked. A faulted row whose recheck time passed gets a new generation first.
func projectChecks(ctx context.Context, tx *Tx, node string) ([]protocol.ProjectCheck, error) {
	now := tx.now().Unix()
	if _, err := tx.ExecContext(ctx, `UPDATE node_readiness SET generation=GREATEST(generation+1,min_generation),min_generation=GREATEST(generation+1,min_generation),recheck_at=0
 WHERE company_id=? AND node_id=? AND faults!='[]'::jsonb AND recheck_at>0 AND recheck_at<=?`, tx.company, node, now); err != nil {
		return nil, err
	}
	gens := map[string]int64{}
	rows, err := tx.QueryContext(ctx, "SELECT project_id,generation FROM node_readiness WHERE company_id=? AND node_id=?", tx.company, node)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p string
		var g int64
		if err = rows.Scan(&p, &g); err != nil {
			rows.Close()
			return nil, err
		}
		gens[p] = g
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	projects, err := liveProjects(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := []protocol.ProjectCheck{}
	for _, p := range projects {
		if p.RepoURL == "" && len(p.Requires) == 0 {
			continue
		}
		g := gens[p.ID]
		if g == 0 {
			g = 1
		}
		out = append(out, protocol.ProjectCheck{Project: p.ID, Repo: p.RepoURL, Requires: p.Requires, Generation: g})
	}
	return out, nil
}

// liveProjects lists the projects that are not archived.
func liveProjects(ctx context.Context, tx *Tx) ([]tasks.Project, error) {
	all, err := listDocs[tasks.Project](ctx, tx, "SELECT body FROM projects WHERE company_id=? ORDER BY id", tx.company)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, p := range all {
		if !p.Archived {
			out = append(out, p)
		}
	}
	return out, nil
}

// applyReadiness records a node's check results (its heartbeat's readiness). A result older than a run's fault is
// ignored; a change between ready and not ready is one feed event, and a node ready again wakes the polls.
func applyReadiness(ctx context.Context, tx *Tx, node string, reports []protocol.ProjectReadiness) error {
	if reports == nil {
		return nil // an older node: it reports nothing
	}
	if _, err := tx.ExecContext(ctx, "UPDATE nodes SET readiness=true WHERE company_id=? AND id=? AND NOT readiness", tx.company, node); err != nil {
		return err
	}
	var name string
	for _, r := range reports {
		if r.Project == "" || r.Generation < 1 {
			continue
		}
		var was []byte
		var minGen, checkedGen, checkedAt int64
		err := tx.QueryRowContext(ctx, "SELECT faults,min_generation,checked_generation,checked_at FROM node_readiness WHERE company_id=? AND node_id=? AND project_id=?", tx.company, node, r.Project).Scan(&was, &minGen, &checkedGen, &checkedAt)
		known := err == nil
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if known && (r.Generation < minGen || r.Generation == checkedGen && r.CheckedAt == checkedAt) {
			continue // older than a run's fault, or already applied
		}
		faults := r.Faults
		if faults == nil {
			faults = []protocol.NodeFault{}
		}
		fb, _ := json.Marshal(faults)
		wasReady := !known || string(was) == "[]"
		ready := len(faults) == 0
		now := tx.now().Unix()
		recheck := int64(0)
		if !ready {
			recheck = now + int64(ReadinessRecheck/time.Second)
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO node_readiness(company_id,node_id,project_id,generation,checked_generation,recheck_at,faults,source,since,checked_at) VALUES(?,?,?,?,?,?,?,'probe',?,?)
 ON CONFLICT(company_id,node_id,project_id) DO UPDATE SET checked_generation=GREATEST(node_readiness.checked_generation,excluded.checked_generation),recheck_at=excluded.recheck_at,faults=excluded.faults,source='probe',run_id='',checked_at=excluded.checked_at,
 since=CASE WHEN excluded.faults='[]'::jsonb THEN 0 WHEN node_readiness.faults='[]'::jsonb THEN excluded.since ELSE node_readiness.since END`,
			tx.company, node, r.Project, r.Generation, r.Generation, recheck, string(fb), now, cmpNonZero(r.CheckedAt, now)); err != nil {
			return err
		}
		if wasReady == ready || !known && ready {
			continue
		}
		if name == "" {
			name = nodeName(ctx, tx, node)
		}
		item := FeedItem{EventID: protocol.ID(), Type: "node.ready", EntityID: "node:" + node, ProjectID: r.Project, CreatedAt: tx.now(), Actor: "node", Ref: r.Project,
			Summary: fmt.Sprintf("node %s is ready for project %s again", nodeLabel(name, node), r.Project)}
		if ready {
			tx.wakeNodes() // queued runs of the project may go to it now
		} else {
			item.Type, item.Attention = "node.not_ready", true
			item.Summary = fmt.Sprintf("node %s is not ready for project %s: %s", nodeLabel(name, node), r.Project, faultText(faults))
		}
		if err = feed(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

func cmpNonZero(a, b int64) int64 {
	if a != 0 {
		return a
	}
	return b
}

// CheckNodeAgain asks the node to run its project checks now ("Check again"). A node that does not report readiness
// (an older version) cannot answer, so its faults are cleared and its next run tests it.
func (s *Store) CheckNodeAgain(ctx context.Context, company, node string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var reports bool
	err = tx.QueryRowContext(ctx, "SELECT readiness FROM nodes WHERE company_id=? AND id=?", company, node).Scan(&reports)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if reports {
		_, err = tx.ExecContext(ctx, "UPDATE node_readiness SET generation=GREATEST(generation+1,min_generation),min_generation=GREATEST(generation+1,min_generation),recheck_at=0 WHERE company_id=? AND node_id=?", company, node)
	} else {
		_, err = tx.ExecContext(ctx, "UPDATE node_readiness SET faults='[]'::jsonb,since=0,recheck_at=0 WHERE company_id=? AND node_id=?", company, node)
		tx.wakeNodes()
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// NodesReadiness lists each node's per-project readiness (node ID → projects, by project ID).
func (s *Store) NodesReadiness(ctx context.Context, company string) (map[string][]ProjectReadiness, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT node_id,project_id,faults,source,run_id,since,checked_at,generation>checked_generation FROM node_readiness WHERE company_id=? ORDER BY node_id,project_id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]ProjectReadiness{}
	for rows.Next() {
		var node string
		var r ProjectReadiness
		var fb []byte
		if err = rows.Scan(&node, &r.Project, &fb, &r.Source, &r.RunID, &r.Since, &r.CheckedAt, &r.Pending); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(fb, &r.Faults)
		if r.Faults == nil {
			r.Faults = []protocol.NodeFault{}
		}
		r.Ready = len(r.Faults) == 0
		out[node] = append(out[node], r)
	}
	for _, rs := range out {
		sort.Slice(rs, func(i, j int) bool { return rs[i].Project < rs[j].Project })
	}
	return out, rows.Err()
}

// companyFaults loads every node's faulted projects (node → project → fault text) for the task evaluator.
func companyFaults(ctx context.Context, tx *Tx) (map[string]map[string]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT node_id,project_id,faults FROM node_readiness WHERE company_id=? AND faults!='[]'::jsonb", tx.company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]string{}
	for rows.Next() {
		var node, project string
		var fb []byte
		if err = rows.Scan(&node, &project, &fb); err != nil {
			return nil, err
		}
		var fs []protocol.NodeFault
		_ = json.Unmarshal(fb, &fs)
		if out[node] == nil {
			out[node] = map[string]string{}
		}
		out[node][project] = faultText(fs)
	}
	return out, rows.Err()
}

// runFault is the node fault of a run's terminal event: the worker's own, else what the result text shows
// (readiness.Classify; older nodes send text only). nil when the end is not the node's fault.
func runFault(p protocol.Event, outcome, result string, usedTokens bool) *protocol.NodeFault {
	if outcome != "setup_failed" && outcome != "failed" {
		return nil
	}
	if f := p.Fault; f != nil && f.Kind != "" && (outcome == "setup_failed" || !usedTokens) {
		if f.Detail == "" {
			f.Detail = result
		}
		return f
	}
	return readiness.Classify(outcome, result, usedTokens)
}
