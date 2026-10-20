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
	"appfactory/internal/tasks"
)

// Runtime protocol v2, Core side (docs/runtime-protocol-v2.md): node registration with epochs, the
// two-phase claim (CLAIMED, then /start), the inventory heartbeat and lost-run detection.
//
// A CLAIMED run (run_started 0 until /start moves it to RUNNING) holds its agent, project and node capacity
// exactly like a running run, so every rule about live runs applies to it.

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
	// Check names the failing check of a driver that is not ok (version, probe, auth); Detail says why.
	Check  string `json:"check,omitempty"`
	Detail string `json:"detail,omitempty"`
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
	// Worktrees: the node runs worktree tasks in their own worktree (node.yaml worktrees.enabled), so
	// tasks of one project run in parallel on it.
	Worktrees bool `json:"worktrees,omitempty"`
	// Reason is why the node registers: start (default; counts towards node.restart_loop) or reregister.
	Reason string `json:"reason,omitempty"`
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
// no longer matches the incarnation file in Core's data directory (a restored backup).
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

// checkIncarnation creates the database's incarnation and, given a file, keeps a copy in it. A database
// whose incarnation differs from that file was restored from a backup (or copied): it gets a new
// incarnation, so nodes stop and seal their runs and register again (runtime protocol v2 §7).
func (s *Store) checkIncarnation(ctx context.Context, file string) error {
	// Core processes starting together create one incarnation: the first insert wins.
	if _, err := s.DB.ExecContext(ctx, "INSERT INTO core_meta(key,value) VALUES('incarnation',?) ON CONFLICT(key) DO NOTHING", protocol.ID()); err != nil {
		return err
	}
	inc, err := s.Incarnation(ctx)
	if err != nil || file == "" {
		return err
	}
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
// a run that never started ends lost and released (safe to retry), a started one lost and released too,
// since the new epoch killed what its previous process left.
func (s *Store) RegisterNode(ctx context.Context, company string, r NodeRegistration) (NodeRegistered, error) {
	var out NodeRegistered
	switch {
	case !nodeID.MatchString(r.NodeID):
		return out, inputError("valid node_id required")
	case r.ProtocolVersion != 2:
		return out, inputError("protocol_version 2 required")
	case r.Epoch < 1 || r.Slots < 1 || r.Slots > 64:
		return out, inputError("epoch >= 1 and slots 1..64 required")
	case !slices.Contains([]string{"", RegisterStart, RegisterReregister}, r.Reason):
		return out, inputError("reason start or reregister required")
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
	var before []byte
	err = tx.QueryRowContext(ctx, "SELECT epoch,drivers FROM nodes WHERE company_id=? AND id=?", company, r.NodeID).Scan(&epoch, &before)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && r.Epoch <= epoch {
		return out, ErrStaleEpoch
	}
	var known []NodeDriver
	_ = json.Unmarshal(before, &known)
	prior, err := readNodePrior(ctx, tx, r.NodeID)
	if err != nil {
		return out, err
	}
	drivers, _ := json.Marshal(nonNil(r.Drivers))
	resources, _ := json.Marshal(nonNilMap(r.Resources))
	labels, _ := json.Marshal(nonNil(r.Labels))
	now := tx.now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO nodes(company_id,id,name,epoch,protocol_version,slots,drivers,resources,labels,registered_at,last_seen,online,worktrees)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,1,?) ON CONFLICT(company_id,id) DO UPDATE SET name=excluded.name,epoch=excluded.epoch,
 protocol_version=excluded.protocol_version,slots=excluded.slots,drivers=excluded.drivers,resources=excluded.resources,
 labels=excluded.labels,health='{}',registered_at=excluded.registered_at,last_seen=excluded.last_seen,online=1,worktrees=excluded.worktrees`,
		company, r.NodeID, r.Name, r.Epoch, r.ProtocolVersion, r.Slots, drivers, resources, labels, now, now, r.Worktrees); err != nil {
		return out, err
	}
	if err = nodeRegistered(ctx, tx, r, prior); err != nil {
		return out, err
	}
	if err = loseNodeRuns(ctx, tx, r.NodeID, r.Epoch, fmt.Sprintf("node %s restarted (epoch %d)", r.NodeID, r.Epoch), true); err != nil {
		return out, err
	}
	// The older epoch's process is gone (the new one killed and sealed what it left): its runs hold nothing.
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET node_held=0 WHERE company_id=? AND node_id=? AND epoch<? AND node_held=1", company, r.NodeID, r.Epoch); err != nil {
		return out, err
	}
	if err = driverTransitions(ctx, tx, r.NodeID, r.Name, known, r.Drivers); err != nil {
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

// driverTransitions records one feed event per driver that changed between usable and not usable on a
// node (runtime-drivers D4): driver_unsupported{driver, check} needs attention, driver_restored does not.
// A driver Core has not seen on the node before counts as usable, so a first register with a failing
// driver reports it; a new epoch that declares the same failure again does not.
func driverTransitions(ctx context.Context, tx *Tx, node, name string, before, after []NodeDriver) error {
	for _, d := range after {
		i := slices.IndexFunc(before, func(b NodeDriver) bool { return b.Name == d.Name })
		was := i < 0 || before[i].OK
		if was == d.OK {
			continue
		}
		item := FeedItem{EventID: protocol.ID(), Type: "driver_restored", EntityID: "node:" + node, CreatedAt: tx.now(), Actor: "node:" + node, Ref: d.Name,
			Summary: fmt.Sprintf("driver %s usable again on node %s (%s)", d.Name, name, d.Version)}
		if !d.OK {
			check := d.Check
			if check == "" {
				check = "probe"
			}
			item.Type, item.Attention = "driver_unsupported", true
			item.Summary = fmt.Sprintf("driver %s disabled on node %s: %s check failed", d.Name, name, check)
			if d.Detail != "" {
				item.Summary += ": " + d.Detail
			}
			slog.Warn("driver unsupported on node", "node_id", node, "driver", d.Name, "check", check, "detail", d.Detail)
		}
		if err := feed(ctx, tx, item); err != nil {
			return err
		}
	}
	return nil
}

// loseNodeRuns ends the unended runs of node claimed before epoch (every epoch when epoch is 0).
// released: the node's runtime is known to be gone (a new epoch); otherwise the lost runs keep their
// project, workspace and session until the node confirms.
func loseNodeRuns(ctx context.Context, tx *Tx, node string, epoch int64, why string, released bool) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,run_started FROM runs WHERE company_id=? AND node_id=? AND (?=0 OR epoch<?) AND status IN ('CLAIMED','RUNNING','CANCELLING') ORDER BY seq", tx.company, node, epoch, epoch)
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
			err = endLost(ctx, tx, l.id, "lost: not started ("+why+")")
		} else {
			err = endLostRun(ctx, tx, l.id, "lost: "+why, !released)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// endLostRun ends a started run Core judged lost: FAILED with outcome lost (timed_out when the result says
// so). unreleased: its runtime may still run, so the run keeps its workspace, session and task slot and its
// task waits until the node proves the runtime gone (releaseLost); late terminal evidence may still replace
// the outcome (Apply).
func endLostRun(ctx context.Context, tx *Tx, id, result string, unreleased bool) error {
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET unreleased=? WHERE id=? AND status IN ('CLAIMED','RUNNING','CANCELLING')", unreleased, id); err != nil {
		return err
	}
	return endLost(ctx, tx, id, result)
}

// endLost ends a run Core judged lost: outcome lost, or timed_out when the result says so.
func endLost(ctx context.Context, tx *Tx, id, result string) error {
	outcome := "lost"
	if strings.HasPrefix(result, "timed_out") {
		outcome = "timed_out"
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET status='ENDED',outcome=?,result=?,finished_at=? WHERE id=? AND status IN ('CLAIMED','RUNNING','CANCELLING')", outcome, result, tx.now().Unix(), id); err != nil {
		return err
	}
	return syncTaskRun(ctx, tx, id)
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

// Refusal is a 409 answer of a v2 claim: Retry keeps the run QUEUED, Drop ends the command. Detail says
// what is short for a capacity refusal (driver, labels, slots, a resource class).
type Refusal struct {
	Retry  bool   `json:"retry,omitempty"`
	Drop   bool   `json:"drop,omitempty"`
	Reason string `json:"reason"`
	Detail string `json:"detail,omitempty"`
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
	var drivers, resources, labels []byte
	err = tx.QueryRowContext(ctx, "SELECT epoch,slots,drivers,resources,labels,worktrees FROM nodes WHERE company_id=? AND id=?", company, req.NodeID).Scan(&n.Epoch, &n.Slots, &drivers, &resources, &labels, &n.Worktrees)
	if errors.Is(err, sql.ErrNoRows) || err == nil && req.Epoch != n.Epoch {
		return out, drop("old_epoch")
	}
	if err != nil {
		return out, err
	}
	_ = json.Unmarshal(drivers, &n.Drivers)
	_ = json.Unmarshal(resources, &n.Resources)
	_ = json.Unmarshal(labels, &n.Labels)
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
		if status == "QUEUED" || status == "ENDED" {
			return out, drop("stale")
		}
		return claimedAnswer(ctx, tx, company, id)
	}
	switch {
	case revoked:
		return out, drop("revoked")
	case status == "CLAIMED" || status == "RUNNING" || status == "CANCELLING":
		return out, drop("duplicate")
	case status != "QUEUED":
		return out, drop("stale")
	}
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", company, id)
	if err != nil {
		return out, err
	}
	if taskID != "" {
		// The task no longer wants the run (reassigned, cancelled, parked, a dependency reopened): it ends stale.
		if why, err := recheckTask(ctx, tx, id, spec); err != nil {
			return out, err
		} else if why != "" {
			return out, claimStale(ctx, tx, id, why)
		}
		// Pinning (task-worktrees D8): unpushed work of the task lives on one node; only that node continues it.
		var pinned string
		if err = tx.QueryRowContext(ctx, "SELECT node_id FROM workspaces WHERE company_id=? AND task_id=? AND unpushed=1 AND node_id!=''", company, taskID).Scan(&pinned); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if pinned != "" && pinned != req.NodeID {
			return out, retry("pinned")
		}
	}
	if paused, err := pausedFor(ctx, tx, spec.Project, spec.Agent, QuotaKey(spec.ConfigSnapshot, spec.Agent)); err != nil {
		return out, err
	} else if len(paused) > 0 {
		return out, retry("paused")
	}
	if paused, err := nodePaused(ctx, tx, req.NodeID); err != nil {
		return out, err
	} else if paused {
		return out, &Refusal{Retry: true, Reason: "paused", Detail: "node " + req.NodeID + " paused by the owner"}
	}
	if !slices.ContainsFunc(n.Drivers, func(d NodeDriver) bool { return d.Name == string(spec.Provider) && d.OK }) {
		return out, &Refusal{Retry: true, Reason: "capacity", Detail: "driver " + string(spec.Provider) + " not usable on this node"}
	}
	if missing := tasks.MissingLabels(n.Labels, AgentLabels(spec.ConfigSnapshot)); len(missing) > 0 {
		return out, &Refusal{Retry: true, Reason: "capacity", Detail: "node lacks labels " + strings.Join(missing, ", ")}
	}
	// Concurrency caps over started runs; the run stays QUEUED and the node retries.
	var busy, busyProject int
	if err = tx.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(CASE WHEN spec->>'project'=? THEN 1 ELSE 0 END),0) FROM runs WHERE company_id=? AND agent=? AND id!=? AND status IN ('CLAIMED','RUNNING','CANCELLING')", spec.Project, company, spec.Agent, id).Scan(&busy, &busyProject); err != nil {
		return out, err
	}
	if perAgent, perProject := AgentCaps(spec.ConfigSnapshot); busy >= perAgent || busyProject >= perProject {
		return out, retry("agent_busy")
	}
	if short, err := nodeShort(ctx, tx, n, req.NodeID, AgentResources(spec.ConfigSnapshot)); err != nil {
		return out, err
	} else if short != "" {
		return out, &Refusal{Retry: true, Reason: "capacity", Detail: short}
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
	if err = briefClaim(ctx, tx, id, &spec); err != nil {
		return out, err
	}
	now := tx.now()
	// The run's writer slot follows the node: its own worktree, or the shared project workspace.
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET workspace=? WHERE id=? AND status='QUEUED'", writerSlot(spec, n.Worktrees), id); err != nil {
		if errors.Is(constraint(err), ErrConflict) {
			return out, retry("project_busy")
		}
		return out, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE runs SET status='CLAIMED',lease=?,lease_until=?,started_at=?,last_activity_at=started_at,protocol=2,node_id=?,epoch=?,claim_id=?,slot=?,start_by=?,run_started=0,node_held=1,
 brief_from=coalesce((SELECT handled_seq FROM tasks WHERE id=runs.task_id),0),brief_to=coalesce((SELECT wake_seq FROM tasks WHERE id=runs.task_id),0)
 WHERE id=? AND status='QUEUED'`, protocol.ID(), now.Add(NodeOffline).Unix(), now.Unix(), req.NodeID, req.Epoch, req.ClaimID, req.Slot, now.Add(StartWindow).Unix(), id)
	if err != nil {
		return out, constraint(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return out, drop("stale")
	}
	if err = markNodeSeen(ctx, tx, req.NodeID); err != nil {
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

// NodeLoad is what a node's runs hold: Held runs (slots) and the resource classes they use. A run holds
// from its claim until it has ended in Core and the node no longer lists it unended (node_held: the node
// lists a run as ended_unacked only after its seal and run-stop hooks), so an ended but unsealed run
// still holds its slot and classes (node-agent D3).
type NodeLoad struct {
	Held int            `json:"held"`
	Used map[string]int `json:"used,omitempty"`
}

func nodeLoad(ctx context.Context, tx *Tx, node string, epoch int64) (NodeLoad, error) {
	l := NodeLoad{Used: map[string]int{}}
	rows, err := tx.QueryContext(ctx, "SELECT spec FROM runs WHERE company_id=? AND node_id=? AND epoch=? AND (status IN ('CLAIMED','RUNNING','CANCELLING') OR node_held=1) ORDER BY seq", tx.company, node, epoch)
	if err != nil {
		return l, err
	}
	defer rows.Close()
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			return l, err
		}
		var spec protocol.RunSpec
		if err = json.Unmarshal(b, &spec); err != nil {
			return l, err
		}
		l.Held++
		for k, v := range AgentResources(spec.ConfigSnapshot) {
			l.Used[k] += v
		}
	}
	return l, rows.Err()
}

// nodeShort says what the node lacks for a run needing need ("" when it has room).
func nodeShort(ctx context.Context, tx *Tx, n Node, node string, need map[string]int) (string, error) {
	l, err := nodeLoad(ctx, tx, node, n.Epoch)
	if err != nil {
		return "", err
	}
	return tasks.NodeShort(l.Held, n.Slots, l.Used, n.Resources, need), nil
}

// AgentLabels reads spec.labels (node labels a run needs) from an agent snapshot.
func AgentLabels(agentJSON []byte) []string {
	var a struct {
		Spec struct {
			Labels []string `json:"labels"`
		} `json:"spec"`
	}
	_ = json.Unmarshal(agentJSON, &a)
	return a.Spec.Labels
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
// It moves a CLAIMED run to RUNNING, is retry-safe and returns the run's status (CANCELLING tells the node
// to stop).
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
	if token != lease || proto != 2 || status != "CLAIMED" && status != "RUNNING" && status != "CANCELLING" {
		return status, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET run_started=?,last_activity_at=?,status=CASE WHEN status='CLAIMED' THEN 'RUNNING' ELSE status END WHERE id=? AND run_started=0", tx.now().Unix(), tx.now().Unix(), id); err != nil {
		return "", err
	}
	if status == "CLAIMED" {
		status = "RUNNING"
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
	// ResetDone lists tasks whose worktree the node reset after a reset answer.
	ResetDone []string `json:"reset_done,omitempty"`
	// Drivers, when present, replaces the node's driver list (runtime-drivers D4): a driver that failed or
	// passed its checks again is declared without a new epoch.
	Drivers []NodeDriver `json:"drivers,omitempty"`
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
	// Reset lists tasks whose worktree on this node must be reset from the remote branch (workspace_diverged
	// answered reset_workspace); the node confirms with reset_done.
	Reset []string `json:"reset"`
}

// NodeHeartbeat renews the leases of every listed run, judges the node's runs against its inventory and
// answers what the node must stop, forget and clean up. An older epoch is ErrStaleEpoch; a node or epoch
// Core does not know is ErrUnknownNode (the node registers again).
func (s *Store) NodeHeartbeat(ctx context.Context, company, node string, hb NodeHeartbeat) (HeartbeatAnswer, error) {
	out := HeartbeatAnswer{Stop: []Stop{}, Acked: []string{}, Cleanup: []string{}, Reset: []string{}}
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
	var name string
	var before []byte
	err = tx.QueryRowContext(ctx, "SELECT epoch,name,drivers FROM nodes WHERE company_id=? AND id=?", company, node).Scan(&epoch, &name, &before)
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
	if err = markNodeSeen(ctx, tx, node); err != nil {
		return out, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE nodes SET last_seen=?,online=1,health=? WHERE company_id=? AND id=?", now.Unix(), string(health), company, node); err != nil {
		return out, err
	}
	if hb.Drivers != nil {
		var known []NodeDriver
		_ = json.Unmarshal(before, &known)
		drivers, _ := json.Marshal(hb.Drivers)
		if _, err = tx.ExecContext(ctx, "UPDATE nodes SET drivers=? WHERE company_id=? AND id=?", string(drivers), company, node); err != nil {
			return out, err
		}
		if err = driverTransitions(ctx, tx, node, name, known, hb.Drivers); err != nil {
			return out, err
		}
	}
	// What the node lists unended holds its capacity (node_held); everything else of the node is released.
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET node_held=0 WHERE company_id=? AND node_id=? AND node_held=1", company, node); err != nil {
		return out, err
	}
	for _, x := range hb.Inventory {
		if x.State == "ended_unacked" {
			continue
		}
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET node_held=1 WHERE company_id=? AND id=? AND node_id=? AND epoch=?", company, x.RunID, node, hb.Epoch); err != nil {
			return out, err
		}
	}
	// Health events (node.low_disk, node.check_failed, ...) and the pause answer at critical disk.
	if out.Pause, err = nodeHealthEvents(ctx, tx, node, health); err != nil {
		return out, err
	}
	if !out.Pause { // an owner node pause (factory pause --node N) also stops the node claiming
		if out.Pause, err = nodePaused(ctx, tx, node); err != nil {
			return out, err
		}
	}
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
		if status != "CLAIMED" && status != "RUNNING" && status != "CANCELLING" {
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
			if err = endLostRun(ctx, tx, x.RunID, "timed_out: still running past timeout_seconds + 60 s grace", true); err != nil {
				return out, err
			}
			out.Stop = append(out.Stop, Stop{x.RunID, "timed_out"})
			continue
		}
		startBy := "start_by"
		if started == 0 && (x.State == "claimed" || x.State == "starting") {
			// Extend the start window, never past MaxStartWindow after the claim.
			startBy = fmt.Sprintf("GREATEST(start_by,LEAST(%d,%d))", now.Add(StartWindow).Unix(), time.Unix(startedAt, 0).Add(MaxStartWindow).Unix())
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
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND node_id=? AND epoch=? AND status IN ('RUNNING','CANCELLING') AND run_started>0 AND run_started<? ORDER BY seq", company, node, hb.Epoch, now.Add(-inventoryGrace).Unix())
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
		if err = endLostRun(ctx, tx, id, "lost: missing from node "+node+" inventory", false); err != nil {
			return out, err
		}
	}
	// Lost runs the node proves gone: older epochs, or this epoch's runs it no longer lists.
	if err = releaseNodeRuns(ctx, tx, node, hb.Epoch, listed, hb.Epoch); err != nil {
		return out, err
	}
	// Resets the node confirmed: the task runs again from the fresh worktree.
	for _, task := range hb.ResetDone {
		res, err := tx.ExecContext(ctx, "UPDATE workspaces SET reset_pending=0,updated_at=? WHERE company_id=? AND task_id=? AND node_id=? AND reset_pending=1", now.Unix(), company, task, node)
		if err != nil {
			return out, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue
		}
		t, err := taskTx(ctx, tx, task)
		if err != nil {
			return out, err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkBlock, resetRef, "cleared"); err != nil {
			return out, err
		}
		if t.Status == "READY" {
			if err = resetRetry(ctx, tx, &t); err != nil {
				return out, err
			}
			if err = saveTask(ctx, tx, &t, "task.retry"); err != nil {
				return out, err
			}
		}
	}
	rrows, err := tx.QueryContext(ctx, "SELECT task_id FROM workspaces WHERE company_id=? AND node_id=? AND reset_pending=1 ORDER BY task_id LIMIT 64", company, node)
	if err != nil {
		return out, err
	}
	for rrows.Next() {
		var task string
		if err = rrows.Scan(&task); err != nil {
			rrows.Close()
			return out, err
		}
		out.Reset = append(out.Reset, task)
	}
	if err = rrows.Err(); err != nil {
		rrows.Close()
		return out, err
	}
	rrows.Close()
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
// continuous ticking) and ends their unended runs as lost; CLAIMED runs past start_by end lost.
func (s *Store) ExpireNodes(ctx context.Context, company string) error {
	// Both windows are 60 s: a node (or a CLAIMED run) is judged only after 60 s of continuous ticking,
	// so a Core host that slept judges nothing until then (runtime protocol v2 §7).
	cutoff := s.windowCutoff(NodeOffline)
	if cutoff == 0 {
		return nil
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE company_id=? AND protocol=2 AND run_started=0 AND status IN ('CLAIMED','CANCELLING') AND start_by<? ORDER BY seq", company, cutoff)
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
		if err = endLost(ctx, tx, id, "lost: not started within the start window"); err != nil {
			return err
		}
	}
	nrows, err := tx.QueryContext(ctx, "SELECT id FROM nodes WHERE company_id=? AND online=1 AND last_seen<? ORDER BY id", company, cutoff-int64(NodeOffline.Seconds()))
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
		if err = nodeWentOffline(ctx, tx, id); err != nil {
			return err
		}
		if err = loseNodeRuns(ctx, tx, id, 0, "node "+id+" offline", false); err != nil {
			return err
		}
	}
	if err = alertLongOffline(ctx, tx, cutoff); err != nil {
		return err
	}
	return tx.Commit()
}

// Nodes lists the registered nodes.
func (s *Store) Nodes(ctx context.Context, company string) ([]Node, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,name,epoch,protocol_version,slots,drivers,resources,labels,health,registered_at,last_seen,online,worktrees FROM nodes WHERE company_id=? ORDER BY name,id", company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Node{}
	for rows.Next() {
		var n Node
		var drivers, resources, labels, health []byte
		if err = rows.Scan(&n.NodeID, &n.Name, &n.Epoch, &n.ProtocolVersion, &n.Slots, &drivers, &resources, &labels, &health, &n.RegisteredAt, &n.LastSeen, &n.Online, &n.Worktrees); err != nil {
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

// PinTimeout is how long a node holding a task's unpushed work may be offline before Core escalates
// pinned_node_unavailable.
var PinTimeout = 30 * time.Minute

// PinnedUnavailable lists open tasks whose pinned node cannot continue them (tasks.PinnedUnavailable: removed,
// offline longer than PinTimeout, or unable to run the assignee); the stalled-sweep escalates them. fill,
// when set, adds what Core knows from the catalog (the assignee's needs) to each snapshot.
func (s *Store) PinnedUnavailable(ctx context.Context, company string, fill func(*tasks.Snapshot)) ([]EscalationWant, error) {
	snaps, err := s.TaskSnapshots(ctx, company, "")
	if err != nil {
		return nil, err
	}
	out := []EscalationWant{}
	for _, snap := range snaps {
		if fill != nil {
			fill(&snap)
		}
		if why := tasks.PinnedUnavailable(snap); why != "" {
			out = append(out, EscalationWant{TaskID: snap.Task.ID, RefID: snap.PinnedNode, Summary: why + "; reset_workspace continues from the remote branch on any node (the unpushed work stays on that node's WIP ref)"})
		}
	}
	return out, nil
}

// RemoveNode forgets an offline node (a replaced machine): its row goes, its tokens are revoked, and its
// unended runs are lost as after a restart. Tasks pinned to it report pinned_node_unavailable.
func (s *Store) RemoveNode(ctx context.Context, company, node string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var online bool
	err = tx.QueryRowContext(ctx, "SELECT online FROM nodes WHERE company_id=? AND id=?", company, node).Scan(&online)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if online {
		return fmt.Errorf("%w: node %s is online; stop it first", ErrConflict, node)
	}
	if err = loseNodeRuns(ctx, tx, node, 0, "node "+node+" removed", true); err != nil {
		return err
	}
	if err = releaseNodeRuns(ctx, tx, node, 1<<62, nil, 0); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET node_held=0 WHERE company_id=? AND node_id=?", company, node); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE node_tokens SET revoked_at=? WHERE company_id=? AND node_id=? AND revoked_at=0", tx.now().Unix(), company, node); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM nodes WHERE company_id=? AND id=?", company, node); err != nil {
		return err
	}
	return tx.Commit()
}
