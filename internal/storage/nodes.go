package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"appfactory/internal/protocol"
)

// Runtime protocol v2, Core side (docs/runtime-protocol-v2.md): node registration with epochs, the
// two-phase claim (CLAIMED, then /start), the inventory heartbeat and lost-run detection.
//
// A CLAIMED run is stored as status RUNNING with run_started 0: it holds its agent, project and node
// capacity exactly like a running run, so every existing rule about live runs applies to it.

// HeartbeatSeconds is the inventory heartbeat period nodes are told at register (a variable for tests).
var HeartbeatSeconds = 10

const (
	// StartWindow is the time a CLAIMED run has to call /start; claimed/starting inventory entries
	// extend it up to MaxStartWindow after the claim.
	StartWindow    = 60 * time.Second
	MaxStartWindow = 15 * time.Minute
	// NodeOffline is the silence after which a node is offline; it is also the lease of v2 runs, so
	// the existing lease expiry (with its continuity guard) ends the runs of a silent node.
	NodeOffline = 60 * time.Second
	// TimeoutGrace is how long past timeout_seconds Core lets a v2 run stay listed before ending it.
	TimeoutGrace = 60 * time.Second
	// inventoryGrace protects a run started after the node took its inventory snapshot.
	inventoryGrace = 30 * time.Second
	// cleanupListings bounds how many heartbeat answers list a closed task's worktree for removal.
	cleanupListings = 30
)

// ErrStaleEpoch: the node registered (or beats with) an epoch not newer than the one Core knows.
var ErrStaleEpoch = fmt.Errorf("%w: stale_epoch", ErrConflict)

// ErrUnknownNode: the node is not registered with this epoch; it must register again.
var ErrUnknownNode = fmt.Errorf("%w: node not registered", ErrNotFound)

var nodeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type NodeDriver struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	OK      bool   `json:"ok"`
}

type NodeRegistration struct {
	NodeID          string         `json:"node_id"`
	Name            string         `json:"name"`
	Epoch           int64          `json:"epoch"`
	ProtocolVersion int            `json:"protocol_version"`
	Slots           int            `json:"slots"`
	Drivers         []NodeDriver   `json:"drivers"`
	Resources       map[string]int `json:"resources"`
	Labels          []string       `json:"labels"`
}

type NodeRegistered struct {
	Incarnation        string `json:"incarnation"`
	HeartbeatSeconds   int    `json:"heartbeat_seconds"`
	StartWindowSeconds int    `json:"start_window_seconds"`
}

// Node is a registered node as Core sees it.
type Node struct {
	NodeRegistration
	Health       json.RawMessage `json:"health"`
	RegisteredAt int64           `json:"registered_at"`
	LastSeen     int64           `json:"last_seen"`
	Online       bool            `json:"online"`
}

// Incarnation is this database's identity. Store.Open creates it, and replaces it when the database
// no longer matches the incarnation file next to it (a restored backup).
func (s *Store) Incarnation(ctx context.Context) (string, error) {
	var v string
	err := s.DB.QueryRowContext(ctx, "SELECT value FROM core_meta WHERE key='incarnation'").Scan(&v)
	return v, err
}

func (s *Store) newIncarnation(ctx context.Context) (string, error) {
	v := protocol.ID()
	_, err := s.DB.ExecContext(ctx, "INSERT INTO core_meta(key,value) VALUES('incarnation',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", v)
	return v, err
}

// checkIncarnation creates the database's incarnation and keeps a copy in <path>.incarnation. A database
// whose incarnation differs from that file was restored from a backup (or copied): it gets a new
// incarnation, so nodes stop and seal their runs and register again (runtime protocol v2 §7).
func (s *Store) checkIncarnation(ctx context.Context, path string) error {
	inc, err := s.Incarnation(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		inc, err = s.newIncarnation(ctx)
	}
	if err != nil {
		return err
	}
	if path == "" || strings.HasPrefix(path, ":memory:") || strings.HasPrefix(path, "file:") {
		return nil
	}
	file := path + ".incarnation"
	b, err := os.ReadFile(file)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return err
	case strings.TrimSpace(string(b)) != inc:
		slog.Warn("database incarnation differs from its file: restored backup; new incarnation", "file", file)
		if inc, err = s.newIncarnation(ctx); err != nil {
			return err
		}
	default:
		return nil
	}
	return os.WriteFile(file, []byte(inc+"\n"), 0o600)
}

// RegisterNode records a node's new epoch. Every unended run of an older epoch of the node is lost:
// a run that never started ends FAILED (safe to retry), a started one UNKNOWN.
func (s *Store) RegisterNode(ctx context.Context, company string, r NodeRegistration) (NodeRegistered, error) {
	var out NodeRegistered
	switch {
	case !nodeID.MatchString(r.NodeID):
		return out, inputError("valid node_id required")
	case r.ProtocolVersion != 2:
		return out, inputError("protocol_version 2 required")
	case r.Epoch < 1 || r.Slots < 1 || r.Slots > 64:
		return out, inputError("epoch >= 1 and slots 1..64 required")
	}
	for k, n := range r.Resources {
		if k == "" || n < 0 {
			return out, inputError("resources: class names and counts >= 0 required")
		}
	}
	inc, err := s.Incarnation(ctx)
	if err != nil {
		return out, err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var epoch int64
	err = tx.QueryRowContext(ctx, "SELECT epoch FROM nodes WHERE company_id=? AND id=?", company, r.NodeID).Scan(&epoch)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && r.Epoch <= epoch {
		return out, ErrStaleEpoch
	}
	drivers, _ := json.Marshal(nonNil(r.Drivers))
	resources, _ := json.Marshal(nonNilMap(r.Resources))
	labels, _ := json.Marshal(nonNil(r.Labels))
	now := tx.now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO nodes(company_id,id,name,epoch,protocol_version,slots,drivers,resources,labels,registered_at,last_seen,online)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,1) ON CONFLICT(company_id,id) DO UPDATE SET name=excluded.name,epoch=excluded.epoch,
 protocol_version=excluded.protocol_version,slots=excluded.slots,drivers=excluded.drivers,resources=excluded.resources,
 labels=excluded.labels,health='{}',registered_at=excluded.registered_at,last_seen=excluded.last_seen,online=1`,
		company, r.NodeID, r.Name, r.Epoch, r.ProtocolVersion, r.Slots, drivers, resources, labels, now, now); err != nil {
		return out, err
	}
	if err = loseNodeRuns(ctx, tx, r.NodeID, r.Epoch, fmt.Sprintf("node %s restarted (epoch %d)", r.NodeID, r.Epoch), true); err != nil {
		return out, err
	}
	// Runs lost earlier while the node was silent are gone too: the new process killed their groups.
	if err = releaseNodeRuns(ctx, tx, r.NodeID, r.Epoch, nil, 0); err != nil {
		return out, err
	}
	var same int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM nodes WHERE company_id=? AND name=? AND id!=? AND online=1", company, r.Name, r.NodeID).Scan(&same); err != nil {
		return out, err
	}
	if same > 0 {
		slog.Warn("two online nodes share a name", "name", r.Name, "node_id", r.NodeID)
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	return NodeRegistered{Incarnation: inc, HeartbeatSeconds: HeartbeatSeconds, StartWindowSeconds: int(StartWindow.Seconds())}, nil
}

// loseNodeRuns ends the unended runs of node claimed before epoch (every epoch when epoch is 0).
// released: the node's runtime is known to be gone (a new epoch); otherwise the lost runs keep their
// project, workspace and session until the node confirms.
func loseNodeRuns(ctx context.Context, tx *Tx, node string, epoch int64, why string, released bool) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,run_started FROM runs WHERE company_id=? AND node_id=? AND (?=0 OR epoch<?) AND status IN ('RUNNING','CANCELLING')", tx.company, node, epoch, epoch)
	if err != nil {
		return err
	}
	type lost struct {
		id      string
		started int64
	}
	var runs []lost
	for rows.Next() {
		var l lost
		if err = rows.Scan(&l.id, &l.started); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, l)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, l := range runs {
		if l.started == 0 {
			err = endLost(ctx, tx, l.id, "FAILED", "lost: not started ("+why+")")
		} else {
			err = endLostUnknown(ctx, tx, l.id, "lost: "+why+"; inspect workspace before reconciliation", released)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// endLostUnknown ends a started run as lost (UNKNOWN). released says its runtime is known to be gone, so
// the task may retry at once; otherwise the run keeps its writer slots until the node confirms.
func endLostUnknown(ctx context.Context, tx *Tx, id, result string, released bool) error {
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET released=? WHERE id=?", released, id); err != nil {
		return err
	}
	return endLost(ctx, tx, id, "UNKNOWN", result)
}

// endLost ends a run Core judged lost. FAILED is final (the runtime never ran); UNKNOWN keeps the
// run open for late terminal evidence and reconciliation, as an expired lease does.
func endLost(ctx context.Context, tx *Tx, id, status, result string) error {
	finished := int64(0)
	if status != "UNKNOWN" {
		finished = tx.now().Unix()
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET status=?,result=?,finished_at=CASE WHEN ?>0 THEN ? ELSE finished_at END WHERE id=? AND status IN ('RUNNING','CANCELLING')", status, result, finished, finished, id); err != nil {
		return err
	}
	return syncTaskRun(ctx, tx, id, status == "FAILED")
}

// ClaimRequest is a v2 claim (phase 1).
type ClaimRequest struct {
	NodeID  string `json:"node_id"`
	Epoch   int64  `json:"epoch"`
	ClaimID string `json:"claim_id"`
	Slot    int    `json:"slot"`
}

type Brief struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

// Claimed is the 200 answer of a v2 claim.
type Claimed struct {
	Lease   string           `json:"lease"`
	StartBy time.Time        `json:"start_by"`
	RunSpec protocol.RunSpec `json:"run_spec"`
	Brief   Brief            `json:"brief"`
}

// Refusal is a 409 answer of a v2 claim: Retry keeps the run QUEUED, Drop ends the command.
type Refusal struct {
	Retry  bool   `json:"retry,omitempty"`
	Drop   bool   `json:"drop,omitempty"`
	Reason string `json:"reason"`
}

func (r *Refusal) Error() string { return "claim refused: " + r.Reason }

func retry(reason string) *Refusal { return &Refusal{Retry: true, Reason: reason} }
func drop(reason string) *Refusal  { return &Refusal{Drop: true, Reason: reason} }

// ClaimV2 is phase 1 of the v2 claim: under the store's single connection it re-checks the run and the
// node, and moves the run to CLAIMED. A retried (node_id, epoch, claim_id) gets the same answer. A refusal
// is returned as *Refusal (with a committed transaction where Core recorded something).
func (s *Store) ClaimV2(ctx context.Context, company, id string, req ClaimRequest) (Claimed, error) {
	var out Claimed
	if !nodeID.MatchString(req.NodeID) || req.ClaimID == "" || len(req.ClaimID) > 128 || req.Epoch < 1 || req.Slot < 0 {
		return out, inputError("node_id, epoch, claim_id and slot required")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var n Node
	var drivers, resources []byte
	err = tx.QueryRowContext(ctx, "SELECT epoch,slots,drivers,resources FROM nodes WHERE company_id=? AND id=?", company, req.NodeID).Scan(&n.Epoch, &n.Slots, &drivers, &resources)
	if errors.Is(err, sql.ErrNoRows) || err == nil && req.Epoch != n.Epoch {
		return out, drop("old_epoch")
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(drivers, &n.Drivers)
	_ = json.Unmarshal(resources, &n.Resources)
	var status, node, claim, taskID string
	var epoch int64
	var revoked bool
	err = tx.QueryRowContext(ctx, "SELECT status,node_id,epoch,claim_id,coalesce(task_id,''),revoked FROM runs WHERE company_id=? AND id=?", company, id).Scan(&status, &node, &epoch, &claim, &taskID, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrNotFound
	}
	if err != nil {
		return out, err
	}
	if node == req.NodeID && epoch == req.Epoch && claim == req.ClaimID {
		if status != "RUNNING" && status != "CANCELLING" {
			return out, drop("stale")
		}
		return claimedAnswer(ctx, tx, company, id)
	}
	switch {
	case revoked:
		return out, drop("revoked")
	case status == "RUNNING" || status == "CANCELLING":
		return out, drop("duplicate")
	case status != "QUEUED":
		return out, drop("stale")
	}
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", company, id)
	if err != nil {
		return out, err
	}
	if taskID != "" {
		t, err := taskTx(ctx, tx, taskID)
		if err != nil {
			return out, err
		}
		if t.CurrentRunID != id || t.Assignee != spec.Agent || t.CancelRequested {
			return out, drop("not_runnable")
		}
	}
	if paused, err := pausedFor(ctx, tx, spec.Project, spec.Agent, QuotaKey(spec.ConfigSnapshot, spec.Agent)); err != nil {
		return out, err
	} else if len(paused) > 0 {
		return out, retry("paused")
	}
	if !slices.ContainsFunc(n.Drivers, func(d NodeDriver) bool { return d.Name == string(spec.Provider) && d.OK }) {
		return out, retry("capacity")
	}
	var busy int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE company_id=? AND agent=? AND id!=? AND status IN ('RUNNING','CANCELLING')", company, spec.Agent, id).Scan(&busy); err != nil {
		return out, err
	}
	if busy > 0 {
		return out, retry("agent_busy")
	}
	if ok, err := nodeHasRoom(ctx, tx, n, req.NodeID, AgentResources(spec.ConfigSnapshot)); err != nil || !ok {
		if err == nil {
			err = retry("capacity")
		}
		return out, err
	}
	// OpenSpec and approved artifacts as in v1; a failed check is recorded on the task.
	if spec.OpenSpecChange != "" {
		if _, e := s.OpenSpec.CheckChange(spec.Workspace, spec.OpenSpecChange); e != nil {
			if err = blockOpenSpecRun(ctx, tx, id, spec.TaskID, e.Error()); err != nil {
				return out, err
			}
			if err = tx.Commit(); err != nil {
				return out, err
			}
			return out, drop("not_runnable")
		}
	}
	valid, err := approvedRunArtifacts(ctx, tx, spec)
	if err != nil {
		return out, err
	}
	if !valid {
		if err = reconcileArtifactTasks(ctx, tx); err != nil {
			return out, err
		}
		if err = tx.Commit(); err != nil {
			return out, err
		}
		return out, drop("not_runnable")
	}
	now := tx.now()
	res, err := tx.ExecContext(ctx, `UPDATE runs SET status='RUNNING',lease=?,lease_until=?,started_at=?,last_activity_at=started_at,protocol=2,node_id=?,epoch=?,claim_id=?,slot=?,start_by=?,run_started=0,
 brief_from=coalesce((SELECT handled_seq FROM tasks WHERE id=runs.task_id),0),brief_to=coalesce((SELECT wake_seq FROM tasks WHERE id=runs.task_id),0)
 WHERE id=? AND status='QUEUED'`, protocol.ID(), now.Add(NodeOffline).Unix(), now.Unix(), req.NodeID, req.Epoch, req.ClaimID, req.Slot, now.Add(StartWindow).Unix(), id)
	if err != nil {
		return out, constraint(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return out, drop("stale")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE nodes SET last_seen=?,online=1 WHERE company_id=? AND id=?", now.Unix(), company, req.NodeID); err != nil {
		return out, err
	}
	if out, err = claimedAnswer(ctx, tx, company, id); err != nil {
		return out, err
	}
	return out, tx.Commit()
}

func claimedAnswer(ctx context.Context, tx *Tx, company, id string) (Claimed, error) {
	var out Claimed
	var b []byte
	var startBy int64
	if err := tx.QueryRowContext(ctx, "SELECT spec,lease,start_by,brief_from,brief_to FROM runs WHERE company_id=? AND id=?", company, id).Scan(&b, &out.Lease, &startBy, &out.Brief.From, &out.Brief.To); err != nil {
		return out, err
	}
	if err := json.Unmarshal(b, &out.RunSpec); err != nil {
		return out, err
	}
	out.RunSpec.Company = company
	out.StartBy = time.Unix(startBy, 0).UTC()
	return out, nil
}

// nodeHasRoom: a free slot and, per resource class, the node's capacity covers what its unended runs
// and this one need. A class the node does not declare has capacity 0.
func nodeHasRoom(ctx context.Context, tx *Tx, n Node, node string, need map[string]int) (bool, error) {
	rows, err := tx.QueryContext(ctx, "SELECT spec FROM runs WHERE company_id=? AND node_id=? AND epoch=? AND status IN ('RUNNING','CANCELLING')", tx.company, node, n.Epoch)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	used, held := map[string]int{}, 0
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return false, err
		}
		var spec protocol.RunSpec
		if err = json.Unmarshal(b, &spec); err != nil {
			return false, err
		}
		held++
		for k, v := range AgentResources(spec.ConfigSnapshot) {
			used[k] += v
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if held >= n.Slots {
		return false, nil
	}
	for k, v := range need {
		if used[k]+v > n.Resources[k] {
			return false, nil
		}
	}
	return true, nil
}

// AgentResources reads spec.resources (a run's needs per node resource class) from an agent snapshot.
func AgentResources(agentJSON []byte) map[string]int {
	var a struct {
		Spec struct {
			Resources map[string]int `json:"resources"`
		} `json:"spec"`
	}
	_ = json.Unmarshal(agentJSON, &a)
	return a.Spec.Resources
}

// StartRun is phase 2 of the v2 claim: the node allocated the workspace and launched the runtime.
// It is retry-safe and returns the run's status (CANCELLING tells the node to stop).
func (s *Store) StartRun(ctx context.Context, company, id, lease string) (string, error) {
	if lease == "" {
		return "", inputError("lease required")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var status, token string
	var proto int
	err = tx.QueryRowContext(ctx, "SELECT status,lease,protocol FROM runs WHERE company_id=? AND id=?", company, id).Scan(&status, &token, &proto)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if token != lease || proto != 2 || status != "RUNNING" && status != "CANCELLING" {
		return status, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET run_started=?,last_activity_at=? WHERE id=? AND run_started=0", tx.now().Unix(), tx.now().Unix(), id); err != nil {
		return "", err
	}
	return status, tx.Commit()
}

// InventoryRun is one entry of the inventory heartbeat.
type InventoryRun struct {
	RunID     string `json:"run_id"`
	State     string `json:"state"`
	ElapsedMS int64  `json:"elapsed_ms"`
}

type NodeHeartbeat struct {
	Epoch     int64           `json:"epoch"`
	Inventory []InventoryRun  `json:"inventory"`
	Health    json.RawMessage `json:"health"`
}

type Stop struct {
	RunID  string `json:"run_id"`
	Reason string `json:"reason"`
}

type HeartbeatAnswer struct {
	Incarnation string   `json:"incarnation"`
	Stop        []Stop   `json:"stop"`
	Acked       []string `json:"acked"`
	Cleanup     []string `json:"cleanup"`
	Pause       bool     `json:"pause"`
}

// NodeHeartbeat renews the leases of every listed run, judges the node's runs against its inventory and
// answers what the node must stop, forget and clean up. An older epoch is ErrStaleEpoch; a node or epoch
// Core does not know is ErrUnknownNode (the node registers again).
func (s *Store) NodeHeartbeat(ctx context.Context, company, node string, hb NodeHeartbeat) (HeartbeatAnswer, error) {
	out := HeartbeatAnswer{Stop: []Stop{}, Acked: []string{}, Cleanup: []string{}}
	inc, err := s.Incarnation(ctx)
	if err != nil {
		return out, err
	}
	out.Incarnation = inc
	for _, x := range hb.Inventory {
		switch x.State {
		case "claimed", "starting", "running", "ended_unacked":
		default:
			return out, inputError("inventory state claimed, starting, running or ended_unacked required")
		}
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	var epoch int64
	err = tx.QueryRowContext(ctx, "SELECT epoch FROM nodes WHERE company_id=? AND id=?", company, node).Scan(&epoch)
	if errors.Is(err, sql.ErrNoRows) || err == nil && hb.Epoch > epoch {
		return out, ErrUnknownNode
	}
	if err != nil {
		return out, err
	}
	if hb.Epoch < epoch {
		return out, ErrStaleEpoch
	}
	now := tx.now()
	health := hb.Health
	if len(health) == 0 {
		health = json.RawMessage("{}")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE nodes SET last_seen=?,online=1,health=? WHERE company_id=? AND id=?", now.Unix(), string(health), company, node); err != nil {
		return out, err
	}
	var h struct {
		Disk string `json:"disk"`
	}
	_ = json.Unmarshal(health, &h)
	out.Pause = h.Disk == "critical"
	listed := map[string]bool{}
	for _, x := range hb.Inventory {
		listed[x.RunID] = true
		var status, rnode, stopReason string
		var repoch, started, startedAt, endApplied int64
		var revoked bool
		var b []byte
		err = tx.QueryRowContext(ctx, "SELECT status,node_id,epoch,run_started,started_at,end_applied,revoked,spec,stop_reason FROM runs WHERE company_id=? AND id=?", company, x.RunID).Scan(&status, &rnode, &repoch, &started, &startedAt, &endApplied, &revoked, &b, &stopReason)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (rnode != node || repoch != hb.Epoch) {
			out.Stop = append(out.Stop, Stop{x.RunID, "unknown_run"})
			continue
		}
		if err != nil {
			return out, err
		}
		if x.State == "ended_unacked" {
			if endApplied == 1 {
				out.Acked = append(out.Acked, x.RunID)
			}
			continue
		}
		if status != "RUNNING" && status != "CANCELLING" {
			out.Stop = append(out.Stop, Stop{x.RunID, "terminal"})
			continue
		}
		var spec protocol.RunSpec
		if err = json.Unmarshal(b, &spec); err != nil {
			return out, err
		}
		// elapsed_ms runs on the node's sleep-excluding clock since the claim: a sleeping laptop never
		// wakes up into a timeout (reliable-recovery D7).
		if started > 0 && spec.TimeoutSeconds > 0 && time.Duration(x.ElapsedMS)*time.Millisecond > time.Duration(spec.TimeoutSeconds)*time.Second+TimeoutGrace {
			if err = endLost(ctx, tx, x.RunID, "UNKNOWN", "timed_out: still running past timeout_seconds + 60 s grace; inspect workspace before reconciliation"); err != nil {
				return out, err
			}
			out.Stop = append(out.Stop, Stop{x.RunID, "timed_out"})
			continue
		}
		startBy := "start_by"
		if started == 0 && (x.State == "claimed" || x.State == "starting") {
			// Extend the start window, never past MaxStartWindow after the claim.
			startBy = fmt.Sprintf("max(start_by,min(%d,%d))", now.Add(StartWindow).Unix(), time.Unix(startedAt, 0).Add(MaxStartWindow).Unix())
		}
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET lease_until=?,start_by="+startBy+" WHERE id=?", now.Add(NodeOffline).Unix(), x.RunID); err != nil {
			return out, err
		}
		switch {
		case status == "CANCELLING" || revoked:
			out.Stop = append(out.Stop, Stop{x.RunID, "revoked"})
		case stopReason != "":
			out.Stop = append(out.Stop, Stop{x.RunID, stopReason})
		}
	}
	// A started run of this epoch the node no longer lists is lost.
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND node_id=? AND epoch=? AND status IN ('RUNNING','CANCELLING') AND run_started>0 AND run_started<?", company, node, hb.Epoch, now.Add(-inventoryGrace).Unix())
	if err != nil {
		return out, err
	}
	var missing []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return out, err
		}
		if !listed[id] {
			missing = append(missing, id)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return out, err
	}
	rows.Close()
	for _, id := range missing {
		if err = endLostUnknown(ctx, tx, id, "lost: missing from node "+node+" inventory", true); err != nil {
			return out, err
		}
	}
	// Lost runs the node proves gone: older epochs, or this epoch's runs it no longer lists.
	if err = releaseNodeRuns(ctx, tx, node, hb.Epoch, listed, hb.Epoch); err != nil {
		return out, err
	}
	// Closed tasks' worktrees on this node, listed a bounded number of times (the node retries busy ones).
	crow, err := tx.QueryContext(ctx, `SELECT w.task_id FROM workspaces w JOIN tasks t ON t.id=w.task_id
 WHERE w.company_id=? AND w.node_id=? AND w.mode='worktree' AND t.status IN ('DONE','CANCELLED') AND w.cleanup_listed<? ORDER BY w.updated_at LIMIT 64`, company, node, cleanupListings)
	if err != nil {
		return out, err
	}
	for crow.Next() {
		var task string
		if err = crow.Scan(&task); err != nil {
			crow.Close()
			return out, err
		}
		out.Cleanup = append(out.Cleanup, task)
	}
	if err = crow.Err(); err != nil {
		crow.Close()
		return out, err
	}
	crow.Close()
	for _, task := range out.Cleanup {
		if _, err = tx.ExecContext(ctx, "UPDATE workspaces SET cleanup_listed=cleanup_listed+1 WHERE company_id=? AND task_id=?", company, task); err != nil {
			return out, err
		}
	}
	return out, tx.Commit()
}

// ExpireNodes marks nodes silent for NodeOffline offline (judged like leases, after one window of
// continuous ticking) and ends their unended runs as lost; CLAIMED runs past start_by end FAILED.
func (s *Store) ExpireNodes(ctx context.Context, company string) error {
	cutoff := s.leaseCutoff()
	if cutoff == 0 {
		return nil
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND protocol=2 AND run_started=0 AND status IN ('RUNNING','CANCELLING') AND start_by<?", company, cutoff)
	if err != nil {
		return err
	}
	var late []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		late = append(late, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range late {
		if err = endLost(ctx, tx, id, "FAILED", "lost: not started within the start window"); err != nil {
			return err
		}
	}
	nrows, err := tx.QueryContext(ctx, "SELECT id FROM nodes WHERE company_id=? AND online=1 AND last_seen<?", company, cutoff-int64(NodeOffline.Seconds()))
	if err != nil {
		return err
	}
	var silent []string
	for nrows.Next() {
		var id string
		if err = nrows.Scan(&id); err != nil {
			nrows.Close()
			return err
		}
		silent = append(silent, id)
	}
	if err = nrows.Err(); err != nil {
		nrows.Close()
		return err
	}
	nrows.Close()
	for _, id := range silent {
		if _, err = tx.ExecContext(ctx, "UPDATE nodes SET online=0 WHERE company_id=? AND id=?", company, id); err != nil {
			return err
		}
		slog.Warn("node offline", "node_id", id)
		if err = loseNodeRuns(ctx, tx, id, 0, "node "+id+" offline", false); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Nodes lists the registered nodes.
func (s *Store) Nodes(ctx context.Context, company string) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,epoch,protocol_version,slots,drivers,resources,labels,health,registered_at,last_seen,online FROM nodes WHERE company_id=? ORDER BY name,id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var n Node
		var drivers, resources, labels, health []byte
		if err = rows.Scan(&n.NodeID, &n.Name, &n.Epoch, &n.ProtocolVersion, &n.Slots, &drivers, &resources, &labels, &health, &n.RegisteredAt, &n.LastSeen, &n.Online); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(drivers, &n.Drivers)
		_ = json.Unmarshal(resources, &n.Resources)
		_ = json.Unmarshal(labels, &n.Labels)
		n.Health = json.RawMessage(health)
		out = append(out, n)
	}
	return out, rows.Err()
}

func nonNil[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}

func nonNilMap(m map[string]int) map[string]int {
	if m == nil {
		return map[string]int{}
	}
	return m
}
