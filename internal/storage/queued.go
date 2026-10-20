package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Why a QUEUED run is not claimed yet (owner-ui 6.1): the claim's own checks (ClaimV2) judged without a
// claim, over every registered node. A company-wide gate comes first (an owner or quota pause, the agent's
// concurrency cap, the node holding the task's unpushed work); otherwise each node says what it lacks and
// the node closest to taking the run gives the reason.

// Queued reasons.
const (
	QueuedPaused         = "paused"          // a company, project, agent or quota key pause
	QueuedAgentBusy      = "agent_busy"      // the agent's concurrency cap (per agent or per project)
	QueuedPinned         = "pinned"          // only the node holding the task's unpushed work may take it
	QueuedNoSlot         = "no_slot"         // every slot of the node is held
	QueuedResourceBusy   = "resource_busy"   // a resource class of the node is held
	QueuedNodePaused     = "node_paused"     // the owner paused the node, or its disk is below critical
	QueuedDriverDisabled = "driver_disabled" // the run's driver failed its checks on the node
	QueuedNodeOffline    = "node_offline"    // the node does not beat
	QueuedLabels         = "labels"          // the node lacks the agent's labels
	QueuedNoNode         = "no_node"         // no registered node (v1 workers only)
	QueuedUnclaimed      = "unclaimed"       // a node can take it: the claim is on its way
)

// queuedRank orders node reasons from the closest to running to the farthest.
var queuedRank = []string{QueuedNoSlot, QueuedResourceBusy, QueuedNodePaused, QueuedDriverDisabled, QueuedNodeOffline, QueuedLabels}

// QueuedReason says why a QUEUED run waits. Until is when it clears by itself (a quota pause, a driver's
// auth hold-off), 0 when unknown. Nodes are each node's answer.
type QueuedReason struct {
	Reason string       `json:"reason"`
	Detail string       `json:"detail"`
	Until  int64        `json:"until,omitempty"`
	Nodes  []QueuedNode `json:"nodes,omitempty"`
}

// QueuedNode is what one node lacks for the run ("" Reason: it can take it).
type QueuedNode struct {
	Node   string `json:"node"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason,omitempty"`
	Detail string `json:"detail,omitempty"`
	Until  int64  `json:"until,omitempty"`
}

// QueuedWhy is why the run waits; nil when it is not QUEUED (or revoked).
func (s *Store) QueuedWhy(ctx context.Context, company, id string) (*QueuedReason, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queuedWhy(ctx, tx, id)
}

// QueuedWhyAll is QueuedWhy of each listed run that is QUEUED, by run ID, in one read.
func (s *Store) QueuedWhyAll(ctx context.Context, company string, ids []string) (map[string]*QueuedReason, error) {
	out := map[string]*QueuedReason{}
	if len(ids) == 0 {
		return out, nil
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		q, err := queuedWhy(ctx, tx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if q != nil {
			out[id] = q
		}
	}
	return out, nil
}

func queuedWhy(ctx context.Context, tx *Tx, id string) (*QueuedReason, error) {
	var status, taskID string
	var revoked bool
	err := tx.QueryRowContext(ctx, "SELECT status,coalesce(task_id,''),revoked FROM runs WHERE company_id=? AND id=?", tx.company, id).Scan(&status, &taskID, &revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || status != "QUEUED" || revoked {
		return nil, err
	}
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", tx.company, id)
	if err != nil {
		return nil, err
	}
	now := tx.now()
	// Pauses: the most general first, with the owner's reason.
	rows, err := tx.QueryContext(ctx, `SELECT scope,target,reason,until FROM pauses WHERE company_id=? AND (until=0 OR until>?)
 AND (scope='company' OR scope='project' AND target=? OR scope='agent' AND target=? OR scope='quota_key' AND target=?)
 ORDER BY CASE scope WHEN 'company' THEN 0 WHEN 'project' THEN 1 WHEN 'agent' THEN 2 ELSE 3 END LIMIT 1`, tx.company, now.Unix(), spec.Project, spec.Agent, QuotaKey(spec.ConfigSnapshot, spec.Agent))
	if err != nil {
		return nil, err
	}
	var p Pause
	found := rows.Next()
	if found {
		err = rows.Scan(&p.Scope, &p.Target, &p.Reason, &p.Until)
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if found {
		what := strings.TrimSpace(p.Scope + " " + p.Target)
		if p.Scope == "quota_key" {
			what = "subscription limit of " + p.Target
		}
		detail := what + " paused"
		if p.Reason != "" {
			detail += ": " + p.Reason
		}
		return &QueuedReason{Reason: QueuedPaused, Detail: detail, Until: p.Until}, nil
	}
	// The agent's concurrency cap over started runs.
	var busy, busyProject int
	if err = tx.QueryRowContext(ctx, "SELECT count(*), coalesce(sum(CASE WHEN spec->>'project'=? THEN 1 ELSE 0 END),0) FROM runs WHERE company_id=? AND agent=? AND id!=? AND status IN ('CLAIMED','RUNNING','CANCELLING')", spec.Project, tx.company, spec.Agent, id).Scan(&busy, &busyProject); err != nil {
		return nil, err
	}
	perAgent, perProject := AgentCaps(spec.ConfigSnapshot)
	switch {
	case busy >= perAgent:
		return &QueuedReason{Reason: QueuedAgentBusy, Detail: fmt.Sprintf("agent %s runs %d of %d at a time", spec.Agent, busy, perAgent)}, nil
	case busyProject >= perProject:
		return &QueuedReason{Reason: QueuedAgentBusy, Detail: fmt.Sprintf("agent %s runs %d of %d at a time in project %s", spec.Agent, busyProject, perProject, spec.Project)}, nil
	}
	pinned := ""
	if taskID != "" {
		if err = tx.QueryRowContext(ctx, "SELECT node_id FROM workspaces WHERE company_id=? AND task_id=? AND unpushed=1 AND node_id!=''", tx.company, taskID).Scan(&pinned); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	nodes, err := queuedNodes(ctx, tx, spec, now)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return &QueuedReason{Reason: QueuedNoNode, Detail: "no registered node; waiting for a worker to claim it"}, nil
	}
	if pinned != "" {
		i := slices.IndexFunc(nodes, func(n QueuedNode) bool { return n.Node == pinned })
		if i < 0 {
			return &QueuedReason{Reason: QueuedPinned, Detail: "the task's unpushed work is on node " + pinned + ", which is no longer registered", Nodes: nodes}, nil
		}
		if n := nodes[i]; n.Reason != "" {
			return &QueuedReason{Reason: n.Reason, Detail: "the task's unpushed work is on " + nodeLabel(n.Name, n.Node) + ": " + n.Detail, Until: n.Until, Nodes: nodes}, nil
		}
		return &QueuedReason{Reason: QueuedUnclaimed, Detail: nodeLabel(nodes[i].Name, nodes[i].Node) + " has room; waiting for its claim", Nodes: nodes}, nil
	}
	best := -1
	for i, n := range nodes {
		if n.Reason == "" {
			return &QueuedReason{Reason: QueuedUnclaimed, Detail: nodeLabel(n.Name, n.Node) + " has room; waiting for its claim", Nodes: nodes}, nil
		}
		if best < 0 || slices.Index(queuedRank, n.Reason) < slices.Index(queuedRank, nodes[best].Reason) {
			best = i
		}
	}
	n := nodes[best]
	detail := nodeLabel(n.Name, n.Node) + ": " + n.Detail
	if len(nodes) > 1 {
		detail += fmt.Sprintf(" (and %d other nodes cannot take it)", len(nodes)-1)
	}
	return &QueuedReason{Reason: n.Reason, Detail: detail, Until: n.Until, Nodes: nodes}, nil
}

// queuedNodes judges every registered node for the run in ClaimV2's order: online, owner pause, disk,
// driver, labels, slots, resource classes.
func queuedNodes(ctx context.Context, tx *Tx, spec protocol.RunSpec, now time.Time) ([]QueuedNode, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,name,epoch,online,last_seen,slots,drivers,labels,resources,health FROM nodes WHERE company_id=? ORDER BY name,id", tx.company)
	if err != nil {
		return nil, err
	}
	type row struct {
		q                                QueuedNode
		epoch, lastSeen                  int64
		online                           bool
		slots                            int
		drivers, labels, resources, hlth []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.q.Node, &r.q.Name, &r.epoch, &r.online, &r.lastSeen, &r.slots, &r.drivers, &r.labels, &r.resources, &r.hlth); err != nil {
			rows.Close()
			return nil, err
		}
		all = append(all, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := make([]QueuedNode, 0, len(all))
	for _, r := range all {
		q := r.q
		set := func(reason, detail string, until int64) { q.Reason, q.Detail, q.Until = reason, detail, until }
		var drivers []NodeDriver
		var labels []string
		var resources map[string]int
		var h struct {
			Disk          string `json:"disk"`
			Free          uint64 `json:"free_disk_bytes"`
			CriticalBytes uint64 `json:"disk_critical_bytes"`
		}
		_ = json.Unmarshal(r.drivers, &drivers)
		_ = json.Unmarshal(r.labels, &labels)
		_ = json.Unmarshal(r.resources, &resources)
		_ = json.Unmarshal(r.hlth, &h)
		var pauseReason string
		paused := false
		if err = tx.QueryRowContext(ctx, "SELECT reason FROM pauses WHERE company_id=? AND scope='node' AND target=?", tx.company, q.Node).Scan(&pauseReason); err == nil {
			paused = true
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		di := slices.IndexFunc(drivers, func(d NodeDriver) bool { return d.Name == string(spec.Provider) })
		switch {
		case !r.online:
			set(QueuedNodeOffline, "offline since "+time.Unix(r.lastSeen, 0).UTC().Format(time.RFC3339), 0)
		case paused:
			d := "paused by the owner"
			if pauseReason != "" {
				d += ": " + pauseReason
			}
			set(QueuedNodePaused, d, 0)
		case h.Disk == "critical":
			d := "paused: disk below its critical watermark"
			if h.CriticalBytes > 0 {
				d = fmt.Sprintf("paused: disk %.1f GiB free, critical below %.1f GiB", float64(h.Free)/(1<<30), float64(h.CriticalBytes)/(1<<30))
			}
			set(QueuedNodePaused, d, 0)
		case di < 0:
			set(QueuedDriverDisabled, "has no "+string(spec.Provider)+" driver", 0)
		case !drivers[di].OK:
			d := drivers[di]
			check := d.Check
			if check == "" {
				check = "probe"
			}
			detail := "driver " + d.Name + " disabled: " + check + " check failed"
			if d.Detail != "" {
				detail += ": " + d.Detail
			}
			if d.Until > now.Unix() {
				detail += " (until " + time.Unix(d.Until, 0).UTC().Format(time.RFC3339) + ")"
			}
			set(QueuedDriverDisabled, detail, d.Until)
		default:
			if missing := tasks.MissingLabels(labels, AgentLabels(spec.ConfigSnapshot)); len(missing) > 0 {
				set(QueuedLabels, "lacks labels "+strings.Join(missing, ", "), 0)
				break
			}
			l, err := nodeLoad(ctx, tx, q.Node, r.epoch)
			if err != nil {
				return nil, err
			}
			if short := tasks.NodeShort(l.Held, r.slots, l.Used, resources, AgentResources(spec.ConfigSnapshot)); short != "" {
				reason := QueuedResourceBusy
				if l.Held >= r.slots {
					reason = QueuedNoSlot
				}
				set(reason, short, 0)
			}
		}
		out = append(out, q)
	}
	return out, nil
}
