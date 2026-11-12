package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"appfactory/internal/protocol"
)

// Node updates, Core side (node-packaging-updates design C, tasks 2.2 and 3.4): which node is due for the release
// Core hands out (the company's policy, approvals, failed attempts), what each node reports of its update, and the
// feed events of both.

// nodePlatform is a package platform a node reports (darwin-arm64, linux-amd64, ...).
var nodePlatform = regexp.MustCompile(`^[a-z0-9]{1,16}-[a-z0-9]{1,16}$`)

// NodeUpdateOffer is the update due for a node: the release, the attempt (the approval's time; 0 under auto without
// one) and whether the owner wants it now (the drain interrupts live runs).
type NodeUpdateOffer struct {
	Version string `json:"version"`
	Attempt int64  `json:"attempt,omitempty"`
	Now     bool   `json:"now,omitempty"`
}

// Why a node behind the release is not offered it.
const (
	UpdateWaitApproval = "approval" // policy approve: an owner or admin approves the node's update
	UpdateWaitFailed   = "failed"   // this release failed on the node; Retry approves it again
)

// DecideNodeUpdate says whether node n gets release under policy: offered (with the attempt), or why it waits ("" with
// no offer: nothing to update, a development build, an unordered version, or a node that predates version reporting).
func DecideNodeUpdate(n Node, policy, release string) (*NodeUpdateOffer, string) {
	if release == "" {
		return nil, ""
	}
	if c, ok := protocol.CompareVersions(n.Version, release); !ok || c >= 0 {
		return nil, ""
	}
	var attempt int64
	if n.UpdateApproved == release {
		attempt = n.UpdateApprovedAt
	}
	if r := n.Update; r != nil && r.State == protocol.UpdateFailed && r.Version == release && r.Attempt == attempt {
		return nil, UpdateWaitFailed
	}
	if policy == protocol.NodeUpdateApprove && n.UpdateApproved != release {
		return nil, UpdateWaitApproval
	}
	return &NodeUpdateOffer{Version: release, Attempt: attempt, Now: n.UpdateNow == release}, ""
}

// nodeUpdateSQL selects what DecideNodeUpdate needs of a node.
const nodeUpdateSQL = "n.version,n.platform,n.update_report,n.update_approved,n.update_approved_at,n.update_now"

func scanNodeUpdate(n *Node, report []byte) {
	var r protocol.NodeUpdateStatus
	if json.Unmarshal(report, &r) == nil && r.State != "" {
		n.Update = &r
	}
}

// NodeUpdatePolicy is the company's node update policy (auto or approve).
func (s *Store) NodeUpdatePolicy(ctx context.Context, company string) (string, error) {
	var p string
	err := s.DB.QueryRowContext(ctx, "SELECT node_update_policy FROM companies WHERE id=?", company).Scan(&p)
	if errors.Is(err, sql.ErrNoRows) {
		return protocol.NodeUpdateAuto, nil
	}
	return p, err
}

// SetNodeUpdatePolicy sets the company's node update policy and records node_update_policy_changed; turning approval
// off wakes the nodes, which may be due now.
func (s *Store) SetNodeUpdatePolicy(ctx context.Context, company, policy, actor, person string) error {
	if policy != protocol.NodeUpdateAuto && policy != protocol.NodeUpdateApprove {
		return inputError("policy auto or approve required")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE companies SET node_update_policy=? WHERE id=? AND node_update_policy!=?", policy, company, policy)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	who := cmpOr(person, actor)
	summary := who + " set node updates to automatic: nodes update to the release at their next drain"
	if policy == protocol.NodeUpdateApprove {
		summary = who + " set node updates to approval: each node's update waits for an owner or admin"
	}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "node_update_policy_changed", EntityID: "company:" + company, CreatedAt: tx.now(), Actor: actor, Person: person, Ref: policy, Summary: summary}); err != nil {
		return err
	}
	tx.wakeNodes()
	return tx.Commit()
}

// NodeUpdateDue is the update Core offers node now for release (nil: none) and the node's platform. Under approval, a
// node behind the release raises node_update_available (attention: the Nodes page and Telegram ask) once per release.
func (s *Store) NodeUpdateDue(ctx context.Context, company, node, release string) (*NodeUpdateOffer, string, error) {
	if release == "" {
		return nil, "", nil
	}
	var n Node
	var report []byte
	var policy, asked string
	err := s.DB.QueryRowContext(ctx, "SELECT n.name,"+nodeUpdateSQL+",n.update_asked,coalesce(c.node_update_policy,'auto') FROM nodes n LEFT JOIN companies c ON c.id=n.company_id WHERE n.company_id=? AND n.id=?",
		company, node).Scan(&n.Name, &n.Version, &n.Platform, &report, &n.UpdateApproved, &n.UpdateApprovedAt, &n.UpdateNow, &asked, &policy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	scanNodeUpdate(&n, report)
	offer, wait := DecideNodeUpdate(n, policy, release)
	if wait == UpdateWaitApproval && asked != release {
		if err = s.askNodeUpdate(ctx, company, node, n.Name, n.Version, release); err != nil {
			return nil, "", err
		}
	}
	return offer, n.Platform, nil
}

func (s *Store) askNodeUpdate(ctx context.Context, company, node, name, version, release string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE nodes SET update_asked=? WHERE company_id=? AND id=? AND update_asked!=?", release, company, node, release)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // another poll asked
	}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "node_update_available", EntityID: "node:" + node, CreatedAt: tx.now(), Actor: "node:" + node, Attention: true, Ref: release,
		Summary: fmt.Sprintf("node %s runs %s; %s is available. Approve the update on the Nodes page", nodeLabel(name, node), cmpOr(version, "an unknown version"), release)}); err != nil {
		return err
	}
	return tx.Commit()
}

// ApproveNodeUpdate approves updating node to release (policy approve, or a retry after a failure: a new attempt) and,
// with now, asks it to update at once: its drain interrupts live runs, which are queued again. It records
// node_update_approved and wakes the node's poll.
func (s *Store) ApproveNodeUpdate(ctx context.Context, company, node, release string, now bool, actor, person string) (Node, error) {
	var n Node
	if release == "" {
		return n, fmt.Errorf("%w: this Core has no node release to update to", ErrConflict)
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return n, err
	}
	defer tx.Rollback()
	var report []byte
	err = tx.QueryRowContext(ctx, "SELECT n.name,"+nodeUpdateSQL+" FROM nodes n WHERE n.company_id=? AND n.id=? FOR UPDATE", company, node).
		Scan(&n.Name, &n.Version, &n.Platform, &report, &n.UpdateApproved, &n.UpdateApprovedAt, &n.UpdateNow)
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	if err != nil {
		return n, err
	}
	scanNodeUpdate(&n, report)
	if c, ok := protocol.CompareVersions(n.Version, release); !ok || c >= 0 {
		return n, fmt.Errorf("%w: node %s runs %s, not older than the release %s", ErrConflict, nodeLabel(n.Name, node), cmpOr(n.Version, "an unknown version"), release)
	}
	_, wait := DecideNodeUpdate(n, protocol.NodeUpdateApprove, release)
	if wait != "" { // not approved for this release yet, or its attempt failed: a new attempt
		n.UpdateApproved, n.UpdateApprovedAt = release, tx.now().Unix()
		if n.Update != nil && n.Update.Attempt == n.UpdateApprovedAt {
			n.UpdateApprovedAt++
		}
	}
	if now {
		n.UpdateNow = release
	}
	if _, err = tx.ExecContext(ctx, "UPDATE nodes SET update_approved=?,update_approved_at=?,update_now=? WHERE company_id=? AND id=?",
		n.UpdateApproved, n.UpdateApprovedAt, n.UpdateNow, company, node); err != nil {
		return n, err
	}
	what := "approved updating"
	if wait == UpdateWaitFailed {
		what = "retried updating"
	}
	when := " at its next drain (after its live runs)"
	if now {
		when = " now: its live runs are interrupted and queued again"
	}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "node_update_approved", EntityID: "node:" + node, CreatedAt: tx.now(), Actor: actor, Person: person, Ref: release,
		Summary: cmpOr(person, actor) + " " + what + " node " + nodeLabel(n.Name, node) + " to " + release + when}); err != nil {
		return n, err
	}
	tx.wakeNodes()
	return n, tx.Commit()
}

// applyUpdateReport records the update a node reports (register, heartbeat; nil: none in progress and none failed):
// a new state gets Core's time, node_update_started is recorded when the node begins a version, and
// node_update_failed (attention: feed, Telegram) once per failed attempt.
func applyUpdateReport(ctx context.Context, tx *Tx, node, name string, r *protocol.NodeUpdateStatus) error {
	var before []byte
	if err := tx.QueryRowContext(ctx, "SELECT update_report FROM nodes WHERE company_id=? AND id=?", tx.company, node).Scan(&before); err != nil {
		return err
	}
	var prev protocol.NodeUpdateStatus
	_ = json.Unmarshal(before, &prev)
	if r == nil || r.State == "" {
		if prev.State == "" {
			return nil
		}
		_, err := tx.ExecContext(ctx, "UPDATE nodes SET update_report='{}' WHERE company_id=? AND id=?", tx.company, node)
		return err
	}
	if err := r.Validate(); err != nil {
		return inputError(err.Error())
	}
	cur := *r
	cur.Runs = 0 // kept apart: it changes while the state does not
	same := prev.State == cur.State && prev.Version == cur.Version && prev.Attempt == cur.Attempt
	if same {
		cur.At = prev.At
	} else {
		cur.At = tx.now().Unix()
	}
	cur.Runs = r.Runs
	body, _ := json.Marshal(cur)
	if _, err := tx.ExecContext(ctx, "UPDATE nodes SET update_report=? WHERE company_id=? AND id=?", string(body), tx.company, node); err != nil {
		return err
	}
	if same {
		return nil
	}
	switch {
	case cur.State == protocol.UpdateDownloading && (prev.Version != cur.Version || prev.Attempt != cur.Attempt || prev.State == protocol.UpdateFailed):
		return nodeFeed(ctx, tx, node, name, "node_update_started", fmt.Sprintf("updating from %s to %s: downloading, then self-test, drain and switch", cmpOr(cur.From, "an unknown version"), cur.Version), cur.Version, false)
	case cur.State == protocol.UpdateFailed:
		return nodeFeed(ctx, tx, node, name, "node_update_failed", fmt.Sprintf("could not update to %s: %s. It keeps running %s; retry from the Nodes page", cur.Version, cur.Reason, cmpOr(cur.From, "its version")), cur.Version, true)
	}
	return nil
}

// nodeVersionChanged records node_updated when a node registers with another version than before, and clears what
// the update to it left (update now, approval of an older release).
func nodeVersionChanged(ctx context.Context, tx *Tx, r NodeRegistration, before string) error {
	if before == "" || before == r.Version {
		return nil
	}
	if _, err := tx.ExecContext(ctx, "UPDATE nodes SET update_now='' WHERE company_id=? AND id=?", tx.company, r.NodeID); err != nil {
		return err
	}
	return nodeFeed(ctx, tx, r.NodeID, r.Name, "node_updated", fmt.Sprintf("runs version %s (was %s)", cmpOr(r.Version, "unknown"), before), r.Version, false)
}
