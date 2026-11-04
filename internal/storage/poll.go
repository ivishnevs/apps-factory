package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// HTTP node transport (http-node-transport): a node's long poll gets offers of queued runs and the node actions
// that are run or task state in Postgres; run events arrive in ordered batches. Reads here change nothing: only the
// signed claim takes a run, and the heartbeat keeps judging the inventory.

// Offer is a queued run a node may claim (a hint: the claim re-checks everything).
type Offer struct {
	RunID    string `json:"run_id"`
	Provider string `json:"provider"`
}

// PollWork is what a node's poll answers, offers aside: urgent stops of its runs, worktree resets and cleanups.
type PollWork struct {
	Stop    []Stop   `json:"stop"`
	Reset   []string `json:"reset"`
	Cleanup []string `json:"cleanup"`
	// SyncJobs are private skill sources the node fetches (skill-registry D3).
	SyncJobs []SyncJob `json:"sync_jobs"`
}

// Empty reports whether w lists nothing.
func (w PollWork) Empty() bool {
	return len(w.Stop) == 0 && len(w.Reset) == 0 && len(w.Cleanup) == 0 && len(w.SyncJobs) == 0
}

// Key identifies w's contents, so a poll answers a list it already delivered only once (the heartbeat stays
// the guarantee for what the node misses).
func (w PollWork) Key() string {
	b, _ := json.Marshal(w)
	return string(b)
}

// maxOfferScan bounds how many queued runs one offers query reads.
const maxOfferScan = 256

// pollNode reads the node's epoch, drivers and labels: ErrUnknownNode for a node or newer epoch Core does not know,
// ErrStaleEpoch for an older one (like the heartbeat).
func (s *Store) pollNode(ctx context.Context, company, node string, epoch int64) (Node, error) {
	var n Node
	var drivers, labels []byte
	err := s.DB.QueryRowContext(ctx, "SELECT epoch,drivers,labels FROM nodes WHERE company_id=? AND id=?", company, node).Scan(&n.Epoch, &drivers, &labels)
	if errors.Is(err, sql.ErrNoRows) || err == nil && epoch > n.Epoch {
		return n, ErrUnknownNode
	}
	if err != nil {
		return n, err
	}
	if epoch < n.Epoch {
		return n, ErrStaleEpoch
	}
	_ = json.Unmarshal(drivers, &n.Drivers)
	_ = json.Unmarshal(labels, &n.Labels)
	return n, nil
}

// Offers lists queued runs of company the node can take, oldest first: at most free[provider] runs of each provider
// whose driver is usable on the node, none for a paused node, without the runs in skip (claims the node had refused
// lately) and without runs whose task's unpushed work is pinned to another node or whose agent needs labels the node
// lacks. The claim re-checks all of it, plus caps, resource classes and project setup.
func (s *Store) Offers(ctx context.Context, company, node string, epoch int64, free map[string]int, skip []string) ([]Offer, error) {
	out := []Offer{}
	n, err := s.pollNode(ctx, company, node, epoch)
	if err != nil {
		return out, err
	}
	var paused int
	if err = s.DB.QueryRowContext(ctx, "SELECT count(*) FROM pauses WHERE company_id=? AND (scope='node' AND target=? OR scope='company' AND (until=0 OR until>?))", company, node, s.Now().Unix()).Scan(&paused); err != nil {
		return out, err
	}
	if paused > 0 {
		return out, nil
	}
	left := map[string]int{}
	var providers []string
	for p, k := range free {
		if k > 0 && slices.ContainsFunc(n.Drivers, func(d NodeDriver) bool { return d.Name == p && d.OK }) {
			left[p] = k
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return out, nil
	}
	sort.Strings(providers)
	providerJSON, _ := json.Marshal(providers)
	skipJSON, _ := json.Marshal(append([]string{}, skip...))
	rows, err := s.DB.QueryContext(ctx, `SELECT r.id,r.spec->>'provider',coalesce(r.spec->'config_snapshot','null'),coalesce(w.node_id,'') FROM runs r
 LEFT JOIN workspaces w ON w.company_id=r.company_id AND w.task_id=r.task_id AND w.unpushed=1 AND w.node_id!=''
 WHERE r.company_id=? AND r.status='QUEUED' AND r.spec->>'provider' IN (SELECT jsonb_array_elements_text(?::jsonb))
 AND r.id NOT IN (SELECT jsonb_array_elements_text(?::jsonb))
 ORDER BY r.created_at,r.seq LIMIT ?`, company, string(providerJSON), string(skipJSON), maxOfferScan)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, provider, pinned string
		var snapshot []byte
		if err = rows.Scan(&id, &provider, &snapshot, &pinned); err != nil {
			return out, err
		}
		if left[provider] == 0 || pinned != "" && pinned != node || len(tasks.MissingLabels(n.Labels, AgentLabels(snapshot))) > 0 {
			continue
		}
		left[provider]--
		out = append(out, Offer{RunID: id, Provider: provider})
	}
	return out, rows.Err()
}

// NodeWork is the node's poll work: stops of its current epoch's live runs Core revoked or gave a stop reason,
// worktree resets waiting for it and closed tasks' worktrees on it (the lists the heartbeat answers too).
func (s *Store) NodeWork(ctx context.Context, company, node string, epoch int64) (PollWork, error) {
	out := PollWork{Stop: []Stop{}, Reset: []string{}, Cleanup: []string{}, SyncJobs: []SyncJob{}}
	if _, err := s.pollNode(ctx, company, node, epoch); err != nil {
		return out, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id,CASE WHEN status='CANCELLING' OR revoked=1 THEN 'revoked' ELSE stop_reason END FROM runs
 WHERE company_id=? AND node_id=? AND epoch=? AND status IN ('CLAIMED','RUNNING','CANCELLING') AND (status='CANCELLING' OR revoked=1 OR stop_reason!='') ORDER BY seq`, company, node, epoch)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var x Stop
		if err = rows.Scan(&x.RunID, &x.Reason); err != nil {
			rows.Close()
			return out, err
		}
		out.Stop = append(out.Stop, x)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	if out.Reset, err = s.strings(ctx, "SELECT task_id FROM workspaces WHERE company_id=? AND node_id=? AND reset_pending=1 ORDER BY task_id LIMIT 64", company, node); err != nil {
		return out, err
	}
	out.Cleanup, err = s.strings(ctx, `SELECT w.task_id FROM workspaces w JOIN tasks t ON t.company_id=w.company_id AND t.id=w.task_id
 WHERE w.company_id=? AND w.node_id=? AND w.mode='worktree' AND t.status IN ('DONE','CANCELLED') AND w.cleanup_listed<? ORDER BY w.updated_at LIMIT 64`, company, node, cleanupListings)
	if err != nil {
		return out, err
	}
	out.SyncJobs, err = s.NodeSyncJobs(ctx, company, node)
	return out, err
}

func (s *Store) strings(ctx context.Context, q string, args ...any) ([]string, error) {
	out := []string{}
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			return out, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Batch limits of POST /runs/{run}/events (http-node-transport D5).
const (
	MaxEventBatch      = 256
	MaxEventBatchBytes = 1 << 20
)

// SeqEvent is a run event with its per-run sequence (1, 2, ...).
type SeqEvent struct {
	Seq   int64             `json:"seq"`
	Event protocol.Envelope `json:"event"`
}

// GapError refuses a batch that starts after the next sequence Core expects.
type GapError struct{ Expected int64 }

func (e *GapError) Error() string { return fmt.Sprintf("event gap: expected seq %d", e.Expected) }

// ApplyEvents applies a node's batch of run events in sequence order (D5): events up to the run's applied sequence
// are skipped (a resent batch), a batch beyond the next one is a *GapError, every other event goes through Apply
// in its own transaction with the sequence it advances to. It answers the highest applied sequence. Apply's
// ErrConflict (another lease) and ErrInvalid end the batch with that error.
func (s *Store) ApplyEvents(ctx context.Context, company, run string, events []SeqEvent) (int64, error) {
	applied, err := s.appliedSeq(ctx, company, run)
	if err != nil {
		return 0, err
	}
	for i, e := range events {
		if i > 0 && e.Seq != events[i-1].Seq+1 {
			return applied, inputError("event seq must increase by one within a batch")
		}
		if e.Event.CorrelationID != run {
			return applied, fmt.Errorf("%w: event of another run", ErrInvalid)
		}
	}
	for _, e := range events {
		if e.Seq <= applied {
			continue
		}
		if e.Seq != applied+1 {
			return applied, &GapError{Expected: applied + 1}
		}
		if err = s.apply(ctx, company, e.Event, e.Seq); err != nil {
			return applied, err
		}
		applied = e.Seq
	}
	return applied, nil
}

func (s *Store) appliedSeq(ctx context.Context, company, run string) (int64, error) {
	var applied int64
	err := s.DB.QueryRowContext(ctx, "SELECT events_seq FROM runs WHERE company_id=? AND id=?", company, run).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return applied, err
}
