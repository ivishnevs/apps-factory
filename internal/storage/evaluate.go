package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"appfactory/internal/tasks"
)

// TaskSnapshot loads what tasks.Evaluate needs about one task in a single transaction.
// AgentConfigured is left for the caller, which owns the agent catalog.
func (s *Store) TaskSnapshot(ctx context.Context, company, id string) (tasks.Snapshot, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return tasks.Snapshot{}, err
	}
	defer tx.Rollback()
	t, err := taskTx(ctx, tx, id)
	if err != nil {
		return tasks.Snapshot{}, err
	}
	nodes, err := nodesTx(ctx, tx)
	if err != nil {
		return tasks.Snapshot{}, err
	}
	snap, err := snapshotTx(ctx, tx, t)
	snap.Nodes = nodes
	if err == nil {
		err = presentTask(ctx, tx, company, &snap.Task)
	}
	return snap, err
}

// TaskSnapshots loads snapshots of every non-terminal task, optionally of one project.
func (s *Store) TaskSnapshots(ctx context.Context, company, project string) ([]tasks.Snapshot, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ts, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND (?='' OR project_id=?) AND status NOT IN ('DONE','CANCELLED') ORDER BY priority DESC,created_at,id", company, project, project)
	if err != nil {
		return nil, err
	}
	nodes, err := nodesTx(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := make([]tasks.Snapshot, 0, len(ts))
	for _, t := range ts {
		snap, err := snapshotTx(ctx, tx, t)
		if err != nil {
			return nil, err
		}
		if err = presentTask(ctx, tx, company, &snap.Task); err != nil {
			return nil, err
		}
		snap.Nodes = nodes
		out = append(out, snap)
	}
	return out, nil
}

// nodesTx loads the company's registered nodes with what their runs hold (node-agent D3).
func nodesTx(ctx context.Context, tx *Tx) ([]tasks.Node, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,name,epoch,online,last_seen,slots,drivers,labels,resources,health FROM nodes WHERE company_id=? ORDER BY name,id", tx.company)
	if err != nil {
		return nil, err
	}
	var out []tasks.Node
	var epochs []int64
	for rows.Next() {
		var n tasks.Node
		var epoch int64
		var drivers, labels, resources, health []byte
		if err = rows.Scan(&n.ID, &n.Name, &epoch, &n.Online, &n.LastSeen, &n.Slots, &drivers, &labels, &resources, &health); err != nil {
			rows.Close()
			return nil, err
		}
		var ds []NodeDriver
		_ = json.Unmarshal(drivers, &ds)
		for _, d := range ds {
			n.Drivers = append(n.Drivers, tasks.NodeDriver{Name: d.Name, OK: d.OK})
		}
		_ = json.Unmarshal(labels, &n.Labels)
		_ = json.Unmarshal(resources, &n.Resources)
		var h struct {
			Disk string `json:"disk"`
		}
		_ = json.Unmarshal(health, &h)
		n.Paused = h.Disk == "critical"
		out = append(out, n)
		epochs = append(epochs, epoch)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for i := range out {
		l, err := nodeLoad(ctx, tx, out[i].ID, epochs[i])
		if err != nil {
			return nil, err
		}
		out[i].Held, out[i].Used = l.Held, l.Used
	}
	return out, nil
}

func snapshotTx(ctx context.Context, tx *Tx, t tasks.Task) (tasks.Snapshot, error) {
	snap := tasks.Snapshot{Task: t, Now: tx.now(), PinTimeout: PinTimeout}
	run := func(query string, args ...any) (*tasks.Run, error) {
		var r tasks.Run
		err := tx.QueryRowContext(ctx, query, args...).Scan(&r.ID, &r.Status, &r.TaskID, &r.Lost)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return &r, err
	}
	var err error
	if t.CurrentRunID != "" {
		if snap.CurrentRun, err = run("SELECT id,status,coalesce(task_id,''),unreleased FROM runs WHERE company_id=? AND id=?", tx.company, t.CurrentRunID); err != nil {
			return snap, err
		}
	}
	if err = tx.QueryRowContext(ctx, "SELECT wake_seq,handled_seq FROM tasks WHERE company_id=? AND id=?", tx.company, t.ID).Scan(&snap.WakeSeq, &snap.HandledSeq); err != nil {
		return snap, err
	}
	// Unpushed work pins the task to its node (task-worktrees D8).
	if err = tx.QueryRowContext(ctx, "SELECT node_id FROM workspaces WHERE company_id=? AND task_id=? AND unpushed=1 AND node_id!=''", tx.company, t.ID).Scan(&snap.PinnedNode); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return snap, err
	}
	parks, err := tx.QueryContext(ctx, "SELECT p.id,p.kind,p.ref_id,p.reason,coalesce(m.recipient,'') FROM task_parks p LEFT JOIN messages m ON p.kind IN ('ask','escalation') AND m.id=p.ref_id WHERE p.company_id=? AND p.task_id=? AND p.status='active' ORDER BY p.created_at,p.id", tx.company, t.ID)
	if err != nil {
		return snap, err
	}
	for parks.Next() {
		var p tasks.Park
		var recipient string
		if err = parks.Scan(&p.ID, &p.Kind, &p.RefID, &p.Reason, &recipient); err != nil {
			parks.Close()
			return snap, err
		}
		p.Who = "owner"
		if recipient != "" && recipient != "owner" {
			p.Who = "agent:" + recipient
		}
		snap.Parks = append(snap.Parks, p)
	}
	if err = parks.Err(); err != nil {
		parks.Close()
		return snap, err
	}
	if err = parks.Close(); err != nil {
		return snap, err
	}
	triggers, err := tx.QueryContext(ctx, "SELECT seq,kind,actor,authority,ref_id FROM task_triggers WHERE company_id=? AND task_id=? AND seq>? AND seq<=? ORDER BY seq LIMIT 20", tx.company, t.ID, snap.HandledSeq, snap.WakeSeq)
	if err != nil {
		return snap, err
	}
	for triggers.Next() {
		var g tasks.Trigger
		if err = triggers.Scan(&g.Seq, &g.Kind, &g.Actor, &g.Authority, &g.RefID); err != nil {
			triggers.Close()
			return snap, err
		}
		snap.Pending = append(snap.Pending, g)
	}
	if err = triggers.Err(); err != nil {
		triggers.Close()
		return snap, err
	}
	if err = triggers.Close(); err != nil {
		return snap, err
	}
	for _, dep := range t.Dependencies {
		d, err := taskTx(ctx, tx, dep)
		if err != nil {
			return snap, err
		}
		snap.Dependencies = append(snap.Dependencies, tasks.Dependency{ID: d.ID, Status: d.Status})
	}
	for _, ref := range t.RequiredArtifacts {
		a, err := artifactApprovalTx(ctx, tx, t.ProjectID, ref.ID, ref.Version)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return snap, err
		}
		if err != nil || a.Status != "APPROVED" || a.Artifact == nil || *a.Artifact != ref {
			snap.UnapprovedArtifacts = append(snap.UnapprovedArtifacts, ref)
		}
	}
	if t.Assignee != "" {
		if snap.AgentRun, err = run("SELECT id,status,coalesce(task_id,''),unreleased FROM runs WHERE company_id=? AND agent=? AND status IN ('QUEUED','RUNNING','CANCELLING') AND coalesce(task_id,'')!=? ORDER BY created_at LIMIT 1", tx.company, t.Assignee, t.ID); err != nil {
			return snap, err
		}
		if snap.AgentLive, snap.AgentProjectLive, err = agentLive(ctx, tx, t.Assignee, t.ProjectID, t.ID); err != nil {
			return snap, err
		}
	}
	// Quota keys come from the agent catalog, which Core owns: storage passes the paused keys along.
	if snap.Paused, err = pausedFor(ctx, tx, t.ProjectID, t.Assignee, ""); err != nil {
		return snap, err
	}
	if snap.QuotaPaused, err = activeQuotaKeys(ctx, tx); err != nil {
		return snap, err
	}
	p, err := projectTx(ctx, tx, t.ProjectID)
	if err != nil {
		return snap, err
	}
	// The workspace_writer index includes unreleased lost runs: they keep the project workspace slot.
	snap.WriterRun, err = run("SELECT id,status,coalesce(task_id,''),unreleased FROM runs WHERE company_id=? AND workspace=? AND (status IN ('QUEUED','RUNNING','CANCELLING') OR unreleased=1) AND coalesce(task_id,'')!=? ORDER BY unreleased DESC,created_at LIMIT 1", tx.company, p.Workspace, t.ID)
	return snap, err
}

// agentLive counts the agent's live runs other than task's, in all projects and in project.
func agentLive(ctx context.Context, tx *Tx, agent, project, task string) (all, inProject int, err error) {
	err = tx.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(json_extract(spec,'$.project')=?),0) FROM runs
 WHERE company_id=? AND agent=? AND status IN ('QUEUED','RUNNING','CANCELLING') AND coalesce(task_id,'')!=?`, project, tx.company, agent, task).Scan(&all, &inProject)
	return
}

// AgentCaps reads the concurrency caps from a captured agent config: live runs of the agent, and per
// project (defaults 1 and the agent cap).
func AgentCaps(agentJSON []byte) (perAgent, perProject int) {
	var a struct {
		Spec struct {
			Execution struct {
				MaxConcurrentRuns           int `json:"max_concurrent_runs"`
				MaxConcurrentRunsPerProject int `json:"max_concurrent_runs_per_project"`
			} `json:"execution"`
		} `json:"spec"`
	}
	_ = json.Unmarshal(agentJSON, &a)
	perAgent = max(a.Spec.Execution.MaxConcurrentRuns, 1)
	perProject = a.Spec.Execution.MaxConcurrentRunsPerProject
	if perProject == 0 || perProject > perAgent {
		perProject = perAgent
	}
	return perAgent, perProject
}
