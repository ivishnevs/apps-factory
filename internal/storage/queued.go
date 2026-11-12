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

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Why a QUEUED run is not claimed yet (owner-ui 6.1): the claim's own checks (ClaimV2) judged without a
// claim, over every registered node. A company-wide gate comes first (an owner or quota pause, the agent's
// concurrency cap, the node holding the task's unpushed work); otherwise each node says what it lacks and
// the node closest to taking the run gives the reason.

// Queued reasons.
const (
	QueuedPaused       = "paused"        // a company, project, agent or quota key pause
	QueuedAgentBusy    = "agent_busy"    // the agent's concurrency cap (per agent or per project)
	QueuedPinned       = "pinned"        // tie work: only the node holding the task's unsaved work may take it (node-ties alias)
	QueuedAffinity     = "affinity"      // tie session: only the node holding the agent's session may take it (node-ties alias)
	QueuedNoSlot       = "no_slot"       // every slot of the node is held
	QueuedResourceBusy = "resource_busy" // a resource class of the node is held
	QueuedNodePaused   = "node_paused"   // the owner paused the node
	// QueuedNodeDiskCritical: Core paused the node for a critical disk (disk-pause-conversations): no work or review
	// run, no conversation below its conversation floor; it resumes by itself once space is freed.
	QueuedNodeDiskCritical = "node_disk_critical"
	QueuedDriverDisabled   = "driver_disabled" // the run's driver failed its checks on the node
	QueuedNodeOffline      = "node_offline"    // the node does not beat
	QueuedLabels           = "labels"          // the node lacks the agent's labels
	QueuedNotReady         = "not_ready"       // the node has a readiness fault for the run's project (node-readiness)
	// QueuedNoReadyNode: every node that could take the run is not ready for its project (node-readiness); the
	// detail names the nodes and their fixes.
	QueuedNoReadyNode = "no_ready_node"
	QueuedNoNode      = "no_node"   // no registered node
	QueuedUnclaimed   = "unclaimed" // a node can take it: the claim is on its way
)

// queuedRank orders node reasons from the closest to running to the farthest.
var queuedRank = []string{QueuedNoSlot, QueuedResourceBusy, QueuedNodeDiskCritical, QueuedNodePaused, QueuedDriverDisabled, QueuedNodeOffline, QueuedNotReady, QueuedLabels}

// QueuedReason says why a QUEUED run waits. Until is when it clears by itself (a quota pause, a driver's
// auth hold-off), 0 when unknown. Nodes are each node's answer.
type QueuedReason struct {
	Reason string       `json:"reason"`
	Detail string       `json:"detail"`
	Until  int64        `json:"until,omitempty"`
	Nodes  []QueuedNode `json:"nodes,omitempty"`
	// Holder and Inactive, for affinity: the node holding the session and why it is inactive ("" when active); an
	// inactive holder's session can be released now (node-readiness 4.1).
	Holder   string `json:"holder,omitempty"`
	Inactive string `json:"inactive,omitempty"`
	// Tie is the node the run is tied to (node-ties): its task's unsaved work or its agent's session is there.
	// Reasons pinned and affinity are its aliases for one release.
	Tie *tasks.Tie `json:"tie,omitempty"`
	// Subject is what holds the run, stable while the cause lasts (throughput-signals groups queued runs by reason
	// and subject): the agent for agent_busy, the pause for paused, the node for a node reason or a tie, the project
	// for no_ready_node; "" when the reason alone says it.
	Subject string `json:"subject,omitempty"`
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
		return &QueuedReason{Reason: QueuedPaused, Detail: detail, Until: p.Until, Subject: p.Scope + ":" + p.Target}, nil
	}
	// The agent's concurrency cap over started runs of the run's kind (work-and-conversation-runs).
	perAgent, perProject, kind := AgentRunCaps(spec.ConfigSnapshot).For(spec.Conversation)
	busy, busyProject, err := agentStarted(ctx, tx, spec.Agent, spec.Project, id, kind)
	if err != nil {
		return nil, err
	}
	runs := "runs"
	switch kind {
	case kindConversation:
		runs = "conversation runs"
	case kindWork:
		runs = "work runs"
	}
	switch {
	case perAgent > 0 && busy >= perAgent:
		return &QueuedReason{Reason: QueuedAgentBusy, Detail: fmt.Sprintf("agent %s runs %d of %d %s at a time", spec.Agent, busy, perAgent, runs), Subject: spec.Agent}, nil
	case perProject > 0 && busyProject >= perProject:
		return &QueuedReason{Reason: QueuedAgentBusy, Detail: fmt.Sprintf("agent %s runs %d of %d %s at a time in project %s", spec.Agent, busyProject, perProject, runs, spec.Project), Subject: spec.Agent}, nil
	}
	// node-ties: the work tie (the task's unsaved work is only on one node) outranks the session tie.
	pinned := ""
	var tie *tasks.Tie
	if taskID != "" {
		if tie, err = workTie(ctx, tx, tx.company, taskID); err != nil {
			return nil, err
		} else if tie != nil {
			pinned = tie.Node
		}
	}
	nodes, err := queuedNodes(ctx, tx, spec, now)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return &QueuedReason{Reason: QueuedNoNode, Detail: "no registered node"}, nil
	}
	if pinned != "" {
		i := slices.IndexFunc(nodes, func(n QueuedNode) bool { return n.Node == pinned })
		if i < 0 {
			return &QueuedReason{Reason: QueuedPinned, Detail: tie.Say() + "; the node is no longer registered", Nodes: nodes, Tie: tie, Subject: pinned}, nil
		}
		tie.NodeName = nodes[i].Name
		if n := nodes[i]; n.Reason != "" {
			reason := n.Reason
			if reason == QueuedNotReady {
				reason = QueuedNoReadyNode
			}
			return &QueuedReason{Reason: reason, Detail: tie.Say() + "; the node: " + n.Detail, Until: n.Until, Nodes: nodes, Tie: tie, Subject: pinned}, nil
		}
		return &QueuedReason{Reason: QueuedUnclaimed, Detail: tie.Say() + "; it has room, waiting for its claim", Nodes: nodes, Tie: tie, Subject: pinned}, nil
	}
	// Session affinity: the holder of the session takes it while active; Until is the release when it is not.
	if a, err := sessionAffinity(ctx, tx, tx.company, id, spec, "", now); err != nil {
		return nil, err
	} else if a != nil {
		tie, err := sessionTie(ctx, tx, tx.company, id, spec.SessionID, a)
		if err != nil {
			return nil, err
		}
		if i := slices.IndexFunc(nodes, func(n QueuedNode) bool { return n.Node == a.Holder }); i >= 0 {
			tie.NodeName = nodes[i].Name
		}
		detail := tie.Say() + "; it takes the run when it has a free slot"
		if a.Inactive == "" && a.Release > 0 {
			detail += "; another node may start it fresh after the conversation wait"
		}
		if a.Inactive != "" {
			detail = tie.Say() + ", and the node is " + a.Inactive + "; another node may take the run " + tie.Release
		}
		return &QueuedReason{Reason: QueuedAffinity, Detail: detail, Until: a.Release, Nodes: nodes, Holder: a.Holder, Inactive: a.Inactive, Tie: tie, Subject: a.Holder}, nil
	}
	best := -1
	for i, n := range nodes {
		if n.Reason == "" {
			return &QueuedReason{Reason: QueuedUnclaimed, Detail: nodeLabel(n.Name, n.Node) + " has room; waiting for its claim", Nodes: nodes, Subject: n.Node}, nil
		}
		if best < 0 || slices.Index(queuedRank, n.Reason) < slices.Index(queuedRank, nodes[best].Reason) {
			best = i
		}
	}
	n := nodes[best]
	if n.Reason == QueuedNotReady {
		// node-readiness: the run waits for a node to be fixed, not for a retry: name every such node and its fix.
		var parts []string
		for _, x := range nodes {
			if x.Reason == QueuedNotReady {
				parts = append(parts, nodeLabel(x.Name, x.Node)+": "+x.Detail)
			}
		}
		return &QueuedReason{Reason: QueuedNoReadyNode, Detail: "no node is ready for project " + spec.Project + " — " + strings.Join(parts, "; "), Nodes: nodes, Subject: spec.Project}, nil
	}
	detail := nodeLabel(n.Name, n.Node) + ": " + n.Detail
	if len(nodes) > 1 {
		detail += fmt.Sprintf(" (and %d other nodes cannot take it)", len(nodes)-1)
	}
	return &QueuedReason{Reason: n.Reason, Detail: detail, Until: n.Until, Nodes: nodes, Subject: n.Node}, nil
}

// queuedNodes judges every registered node for the run in ClaimV2's order: online, owner pause, disk,
// driver, labels, project readiness (node-readiness), slots, resource classes.
func queuedNodes(ctx context.Context, tx *Tx, spec protocol.RunSpec, now time.Time) ([]QueuedNode, error) {
	rows, err := tx.QueryContext(ctx, "SELECT id,name,epoch,online,last_seen,slots,conversation_slots,drivers,labels,resources,health FROM nodes WHERE company_id=? ORDER BY name,id", tx.company)
	if err != nil {
		return nil, err
	}
	type row struct {
		q                                QueuedNode
		epoch, lastSeen                  int64
		online                           bool
		slots, convSlots                 int
		drivers, labels, resources, hlth []byte
	}
	var all []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.q.Node, &r.q.Name, &r.epoch, &r.online, &r.lastSeen, &r.slots, &r.convSlots, &r.drivers, &r.labels, &r.resources, &r.hlth); err != nil {
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
		_ = json.Unmarshal(r.drivers, &drivers)
		_ = json.Unmarshal(r.labels, &labels)
		_ = json.Unmarshal(r.resources, &resources)
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
		case diskPaused(r.hlth, spec.Conversation):
			// A conversation goes on at a critical disk (disk-pause-conversations); work and review runs wait.
			set(QueuedNodeDiskCritical, readDisk(r.hlth).what(), 0)
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
			if why, err := nodeNotReady(ctx, tx, tx.company, q.Node, spec.Project); err != nil {
				return nil, err
			} else if why != "" {
				set(QueuedNotReady, "not ready for project "+spec.Project+": "+why, 0)
				break
			}
			l, err := nodeLoad(ctx, tx, q.Node, r.epoch, r.slots)
			if err != nil {
				return nil, err
			}
			n := tasks.Node{Slots: r.slots, Held: l.Held, Used: l.Used, Resources: resources, ConversationSlots: r.convSlots, ConversationHeld: l.Conversation}
			need := RunResources(spec)
			if short := n.Short(spec.Conversation, need); short != "" {
				reason := QueuedResourceBusy
				if l.Held >= r.slots {
					reason = QueuedNoSlot
				} else if held := resourceHolders(need, l, resources); held != "" {
					short += "; held by " + held // throughput-signals 2.2: the queue names who holds the class
				}
				set(reason, short, 0)
			}
		}
		out = append(out, q)
	}
	return out, nil
}

// resourceHolders names who holds the classes the run cannot get on a node (its first short class), "" when no run
// holds one (the node lacks the class).
func resourceHolders(need map[string]int, l NodeLoad, capacity map[string]int) string {
	for _, k := range slices.Sorted(maps.Keys(need)) {
		if l.Used[k]+need[k] > capacity[k] && len(l.Holders[k]) > 0 {
			return strings.Join(l.Holders[k], ", ")
		}
	}
	return ""
}
