package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"appfactory/internal/protocol"
)

// Node health events (node-agent D5, D7): Core writes them into the event feed (company-wide, entity
// node:<id>) in the transaction of the register, heartbeat or expiry that observed the change. Each alert is
// raised once per episode; the node's row keeps what was raised (migration 0026).
//
//	node.registered       every register (no attention)
//	node.online           a node that was offline is seen again
//	node.offline          silent for NodeOffline (no attention); again with attention after OfflineAttention
//	node.low_disk         health.disk low or critical; re-armed when it is ok again (attention)
//	node.disk_critical    health.disk critical; Core answers pause (attention); re-armed at ok
//	node.disk_ok          health.disk ok after an alert
//	node.paused           Core started answering pause; node.resumed when it stops
//	node.restart_loop     RestartLoopCount process restarts within RestartLoopWindow (attention)
//	node.check_failed     a self-check started failing; ref is the check name (attention)
//	node.check_passed     a failing self-check passes again; ref is the check name
//	node.heartbeat_stalled the node had no successful heartbeat for 5 min and reported it on reconnect (attention)

const (
	// RestartLoopWindow and RestartLoopCount: that many process restarts of a node within the window raise
	// one node.restart_loop; a window without restarts re-arms it.
	RestartLoopWindow = 15 * time.Minute
	RestartLoopCount  = 4
	// OfflineAttention is how long a node stays offline before node.offline is raised with attention.
	OfflineAttention = 10 * time.Minute
)

// Register reasons (NodeRegistration.Reason): only a process start counts towards node.restart_loop.
const (
	RegisterStart      = "start"      // the node process started (default)
	RegisterReregister = "reregister" // Core lost the node or its incarnation changed
)

// NodeCheck is one entry of the heartbeat's health.checks: the node's self-checks (driver probes travel in drivers). Only
// status fail counts; a node lists its failing checks (passing ones may be omitted).
type NodeCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// nodeHealthReport is what Core reads from a heartbeat's health.
type nodeHealthReport struct {
	Disk string `json:"disk"`
	// Checks: nil (absent) keeps the last known failing checks; [] says none fails.
	Checks *[]NodeCheck `json:"checks"`
	// StalledSeconds > 0: the node had no successful heartbeat for that long (sent once, on reconnect).
	StalledSeconds int64 `json:"stalled_seconds"`
}

func nodeLabel(name, id string) string {
	if name == "" || name == id {
		return id
	}
	return name + " (" + id + ")"
}

func nodeFeed(ctx context.Context, tx *Tx, id, name, kind, summary, ref string, attention bool) error {
	if ref == "" {
		ref = id
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: kind, EntityID: "node:" + id, CreatedAt: tx.now(), Actor: "node",
		Summary: "node " + nodeLabel(name, id) + ": " + summary, Attention: attention, Ref: ref})
}

// nodePrior is a node's row before a register.
type nodePrior struct {
	exists, online, restartAlerted bool
	starts                         []int64
}

func readNodePrior(ctx context.Context, tx *Tx, id string) (nodePrior, error) {
	var p nodePrior
	var starts string
	err := tx.QueryRowContext(ctx, "SELECT online,starts,restart_alerted FROM nodes WHERE company_id=? AND id=?", tx.company, id).Scan(&p.online, &starts, &p.restartAlerted)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	p.exists = true
	_ = json.Unmarshal([]byte(starts), &p.starts)
	return p, nil
}

// nodeRegistered records a register's events: node.registered, node.online for a node that was offline, and
// the restart count behind node.restart_loop. A node's first register is not a restart.
func nodeRegistered(ctx context.Context, tx *Tx, r NodeRegistration, prior nodePrior) error {
	now := tx.now()
	reason := r.Reason
	if reason == "" {
		reason = RegisterStart
	}
	if err := nodeFeed(ctx, tx, r.NodeID, r.Name, "node.registered", fmt.Sprintf("epoch %d registered (%s), %d slots", r.Epoch, reason, r.Slots), "", false); err != nil {
		return err
	}
	if prior.exists && !prior.online {
		if err := nodeFeed(ctx, tx, r.NodeID, r.Name, "node.online", "online again", "", false); err != nil {
			return err
		}
	}
	starts, alerted := prior.starts, prior.restartAlerted
	if prior.exists && reason == RegisterStart {
		cutoff := now.Add(-RestartLoopWindow).Unix()
		starts = slices.DeleteFunc(slices.Clone(starts), func(t int64) bool { return t <= cutoff })
		if len(starts) == 0 {
			alerted = false // a calm window re-arms the alert
		}
		starts = append(starts, now.Unix())
		if len(starts) >= RestartLoopCount && !alerted {
			alerted = true
			if err := nodeFeed(ctx, tx, r.NodeID, r.Name, "node.restart_loop", fmt.Sprintf("%d restarts within %s (epoch %d); check the node's logs (launchd/journald)", len(starts), RestartLoopWindow, r.Epoch), "", true); err != nil {
				return err
			}
		}
	}
	b, _ := json.Marshal(nonNil(starts))
	_, err := tx.ExecContext(ctx, "UPDATE nodes SET starts=?,restart_alerted=?,offline_alerted=0 WHERE company_id=? AND id=?", string(b), alerted, tx.company, r.NodeID)
	return err
}

// markNodeSeen records contact with a node (heartbeat, claim): last_seen, online, and node.online when it was offline.
func markNodeSeen(ctx context.Context, tx *Tx, id string) error {
	var online bool
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT online,name FROM nodes WHERE company_id=? AND id=?", tx.company, id).Scan(&online, &name); err != nil {
		return err
	}
	if !online {
		if err := nodeFeed(ctx, tx, id, name, "node.online", "online again", "", false); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, "UPDATE nodes SET last_seen=?,online=1,offline_alerted=0 WHERE company_id=? AND id=?", tx.now().Unix(), tx.company, id)
	return err
}

// nodeHealthEvents judges a heartbeat's health against what was raised before and returns whether Core
// answers pause (critical disk; an unreadable disk keeps the last answer).
func nodeHealthEvents(ctx context.Context, tx *Tx, id string, health json.RawMessage) (bool, error) {
	var name, failedJSON string
	var diskAlerts int
	var paused bool
	if err := tx.QueryRowContext(ctx, "SELECT name,disk_alerts,core_paused,failed_checks FROM nodes WHERE company_id=? AND id=?", tx.company, id).Scan(&name, &diskAlerts, &paused, &failedJSON); err != nil {
		return false, err
	}
	var h nodeHealthReport
	_ = json.Unmarshal(health, &h)
	ev := func(kind, summary, ref string, attention bool) error {
		return nodeFeed(ctx, tx, id, name, kind, summary, ref, attention)
	}
	const lowBit, criticalBit = 1, 2
	var err error
	switch h.Disk {
	case "ok":
		if diskAlerts != 0 {
			err = ev("node.disk_ok", "free disk above the low watermark again", "", false)
		}
		diskAlerts = 0
	case "low", "critical":
		if diskAlerts&lowBit == 0 {
			diskAlerts |= lowBit
			err = ev("node.low_disk", "free disk below the low watermark; the janitor prunes aggressively", "", true)
		}
		if err == nil && h.Disk == "critical" && diskAlerts&criticalBit == 0 {
			diskAlerts |= criticalBit
			err = ev("node.disk_critical", "free disk below the critical watermark; claims paused until space is freed", "", true)
		}
	}
	if err != nil {
		return false, err
	}
	pause := paused
	if h.Disk != "" {
		pause = h.Disk == "critical"
	}
	switch {
	case pause && !paused:
		err = ev("node.paused", "Core pauses claims on this node (critical disk)", "", false)
	case !pause && paused:
		err = ev("node.resumed", "claims resume on this node", "", false)
	}
	if err != nil {
		return false, err
	}
	failed := map[string]string{}
	_ = json.Unmarshal([]byte(failedJSON), &failed)
	if h.Checks != nil {
		now := map[string]string{}
		for _, c := range *h.Checks {
			if c.Status == "fail" && c.Check != "" {
				now[c.Check] = c.Detail
			}
		}
		for _, check := range sortedNames(now) {
			if _, was := failed[check]; !was {
				if err = ev("node.check_failed", "check "+check+" failed: "+firstLine(now[check]), check, true); err != nil {
					return false, err
				}
			}
		}
		for _, check := range sortedNames(failed) {
			if _, still := now[check]; !still {
				if err = ev("node.check_passed", "check "+check+" passes again", check, false); err != nil {
					return false, err
				}
			}
		}
		failed = now
	}
	if h.StalledSeconds > 0 {
		if err = ev("node.heartbeat_stalled", fmt.Sprintf("no successful heartbeat for %s; connectivity is back", (time.Duration(h.StalledSeconds)*time.Second).String()), "", true); err != nil {
			return false, err
		}
	}
	b, _ := json.Marshal(failed)
	_, err = tx.ExecContext(ctx, "UPDATE nodes SET disk_alerts=?,core_paused=?,failed_checks=? WHERE company_id=? AND id=?", diskAlerts, pause, string(b), tx.company, id)
	return pause, err
}

func sortedNames(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return s
}

// NodeUse is what a node's unended runs of its current epoch hold: slots and resource units per class.
type NodeUse struct {
	Runs      int            `json:"runs"`
	Resources map[string]int `json:"resources"`
}

// NodesInUse sums, per node, the runs and resources its current epoch's unended runs hold (claim capacity).
func (s *Store) NodesInUse(ctx context.Context, company string) (map[string]NodeUse, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT r.node_id,r.spec FROM runs r JOIN nodes n ON n.company_id=r.company_id AND n.id=r.node_id AND n.epoch=r.epoch
 WHERE r.company_id=? AND r.status IN ('CLAIMED','RUNNING','CANCELLING') ORDER BY r.seq`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]NodeUse{}
	for rows.Next() {
		var id string
		var b []byte
		if err = rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		var spec protocol.RunSpec
		if err = json.Unmarshal(b, &spec); err != nil {
			return nil, err
		}
		u := out[id]
		if u.Resources == nil {
			u.Resources = map[string]int{}
		}
		u.Runs++
		for k, v := range AgentResources(spec.ConfigSnapshot) {
			u.Resources[k] += v
		}
		out[id] = u
	}
	return out, rows.Err()
}

// nodeWentOffline records node.offline for a node ExpireNodes found silent.
func nodeWentOffline(ctx context.Context, tx *Tx, id string) error {
	var name string
	if err := tx.QueryRowContext(ctx, "SELECT name FROM nodes WHERE company_id=? AND id=?", tx.company, id).Scan(&name); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE nodes SET offline_alerted=0 WHERE company_id=? AND id=?", tx.company, id); err != nil {
		return err
	}
	return nodeFeed(ctx, tx, id, name, "node.offline", fmt.Sprintf("no heartbeat for %s; its unended runs are lost", NodeOffline), "", false)
}

// alertLongOffline raises node.offline with attention once for each node offline longer than OfflineAttention.
func alertLongOffline(ctx context.Context, tx *Tx, cutoff int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,name,last_seen FROM nodes WHERE company_id=? AND online=0 AND offline_alerted=0 AND last_seen<? ORDER BY name,id", tx.company, cutoff-int64((NodeOffline+OfflineAttention).Seconds()))
	if err != nil {
		return err
	}
	type long struct {
		id, name string
		seen     int64
	}
	var nodes []long
	for rows.Next() {
		var n long
		if err = rows.Scan(&n.id, &n.name, &n.seen); err != nil {
			rows.Close()
			return err
		}
		nodes = append(nodes, n)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, n := range nodes {
		if _, err = tx.ExecContext(ctx, "UPDATE nodes SET offline_alerted=1 WHERE company_id=? AND id=?", tx.company, n.id); err != nil {
			return err
		}
		since := time.Unix(n.seen, 0).UTC().Format(time.RFC3339)
		if err = nodeFeed(ctx, tx, n.id, n.name, "node.offline", "offline for more than "+OfflineAttention.String()+" (last heartbeat "+since+"); start it or remove it", "", true); err != nil {
			return err
		}
	}
	return nil
}
