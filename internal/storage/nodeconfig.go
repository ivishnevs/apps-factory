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

	"appfactory/internal/protocol"
)

// Node versions, detected labels and settings from the web (node-packaging-updates design parts B and C, 3.6).

// MinNodeVersion is the oldest node version the code supports (Store.MinNodeVersion's default; FACTORY_MIN_NODE_VERSION
// raises it). Raise it with a protocol change older nodes cannot follow. Empty: every node gets work (no node reported
// a version before node-packaging-updates).
const MinNodeVersion = ""

// nodeLabelsSQL is a node's labels for selection: the owner's (node.yaml or the web) and the detected ones.
const nodeLabelsSQL = "(labels::jsonb||detected_labels)"

// maxDetected bounds a node's detected labels.
const maxDetected = 32

func validDetected(labels []string) error {
	if len(labels) > maxDetected {
		return inputError(fmt.Sprintf("detected_labels: at most %d", maxDetected))
	}
	for _, l := range labels {
		if !protocol.ValidLabel(l) {
			return inputError("detected_labels: invalid label " + l)
		}
	}
	return nil
}

// updateRequired says why a node of version v gets no work ("" when it may work): below min_node_version.
func (s *Store) updateRequired(v string) string {
	if !protocol.VersionBelow(v, s.MinNodeVersion) {
		return ""
	}
	if v == "" {
		v = "unknown"
	}
	return "update required: version " + v + ", Core needs " + s.MinNodeVersion + " or newer"
}

// nodeVersionChecked records node_update_required (attention) when a node below min_node_version registers.
func nodeVersionChecked(ctx context.Context, tx *Tx, r NodeRegistration) error {
	why := tx.store.updateRequired(r.Version)
	if why == "" {
		return nil
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "node_update_required", EntityID: "node:" + r.NodeID, CreatedAt: tx.now(), Actor: "node:" + r.NodeID,
		Attention: true, Summary: "node " + nodeLabel(r.Name, r.NodeID) + " gets no work: " + why + "; reinstall it with the install command on the Nodes page"})
}

// NodeConfigView is a node's settings from the web: the desired settings and their version, the node's last report
// (nil: the node never reported one, it predates settings from the web) and each desired setting's state there:
// applied, refused with the reason, or pending until the node applied this version.
type NodeConfigView struct {
	Version   int64                                `json:"version"`
	Desired   protocol.NodeSettings                `json:"desired"`
	UpdatedBy string                               `json:"updated_by,omitempty"`
	UpdatedAt int64                                `json:"updated_at,omitempty"`
	Report    *protocol.NodeConfigReport           `json:"report,omitempty"`
	States    map[string]protocol.NodeSettingState `json:"states"`
}

// fill reads the desired settings and the report and judges each desired setting.
func (v *NodeConfigView) fill(desired, report []byte) {
	_ = json.Unmarshal(desired, &v.Desired)
	var r protocol.NodeConfigReport
	if json.Unmarshal(report, &r) == nil && r.Settings != nil {
		v.Report = &r
	}
	v.States = map[string]protocol.NodeSettingState{}
	for _, k := range v.Desired.Keys() {
		switch {
		case v.Report == nil:
			v.States[k] = protocol.NodeSettingState{State: protocol.SettingPending, Reason: "the node does not take settings from the web yet: update it"}
		case v.Report.Version < v.Version:
			v.States[k] = protocol.NodeSettingState{State: protocol.SettingPending, Reason: fmt.Sprintf("the node applied version %d, not %d yet", v.Report.Version, v.Version)}
		default:
			st, ok := v.Report.Settings[k]
			if !ok {
				st = protocol.NodeSettingState{State: protocol.SettingPending}
			}
			v.States[k] = st
		}
	}
}

// NodeConfig is the node's settings from the web (version 0 and no settings when none were set).
func (s *Store) NodeConfig(ctx context.Context, company, node string) (NodeConfigView, error) {
	v := NodeConfigView{States: map[string]protocol.NodeSettingState{}}
	var desired, report []byte
	err := s.DB.QueryRowContext(ctx, `SELECT coalesce(c.version,0),coalesce(c.config,'{}'::jsonb),coalesce(c.updated_by,''),coalesce(c.updated_at,0),n.config_report
 FROM nodes n LEFT JOIN node_configs c ON c.company_id=n.company_id AND c.node_id=n.id WHERE n.company_id=? AND n.id=?`, company, node).Scan(&v.Version, &desired, &v.UpdatedBy, &v.UpdatedAt, &report)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.fill(desired, report)
	return v, nil
}

// NodeConfigVersion is the version of the node's settings from the web (0: none).
func (s *Store) NodeConfigVersion(ctx context.Context, company, node string) (int64, error) {
	var v int64
	err := s.DB.QueryRowContext(ctx, "SELECT version FROM node_configs WHERE company_id=? AND node_id=?", company, node).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return v, err
}

// SetNodeConfig replaces the node's desired settings as a new version (base, when not 0, must be the current one:
// another change in between is a conflict), records node_config_changed with who changed what, and wakes the
// node's poll, which carries the new version. Empty settings hand every setting back to node.yaml.
func (s *Store) SetNodeConfig(ctx context.Context, company, node string, settings protocol.NodeSettings, base int64, actor, person string) (NodeConfigView, error) {
	if err := settings.Validate(); err != nil {
		return NodeConfigView{}, inputError(err.Error())
	}
	if settings.Labels != nil {
		uniq := slices.Compact(slices.Sorted(slices.Values(*settings.Labels)))
		settings.Labels = &uniq
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return NodeConfigView{}, err
	}
	defer tx.Rollback()
	var name string
	if err = tx.QueryRowContext(ctx, "SELECT name FROM nodes WHERE company_id=? AND id=?", company, node).Scan(&name); errors.Is(err, sql.ErrNoRows) {
		return NodeConfigView{}, ErrNotFound
	} else if err != nil {
		return NodeConfigView{}, err
	}
	var current int64
	if err = tx.QueryRowContext(ctx, "SELECT version FROM node_configs WHERE company_id=? AND node_id=?", company, node).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return NodeConfigView{}, err
	}
	if base != 0 && base != current {
		return NodeConfigView{}, fmt.Errorf("%w: the node's settings changed meanwhile (version %d, not %d): reload and change them again", ErrConflict, current, base)
	}
	body, _ := json.Marshal(settings)
	now := tx.now().Unix()
	if _, err = tx.ExecContext(ctx, `INSERT INTO node_configs(company_id,node_id,version,config,updated_by,updated_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(company_id,node_id) DO UPDATE SET version=excluded.version,config=excluded.config,updated_by=excluded.updated_by,updated_at=excluded.updated_at`,
		company, node, current+1, string(body), actor, now); err != nil {
		return NodeConfigView{}, err
	}
	who := person
	if who == "" {
		who = actor
	}
	item := FeedItem{EventID: protocol.ID(), Type: "node_config_changed", EntityID: "node:" + node, CreatedAt: tx.now(), Actor: actor, Person: person, Ref: fmt.Sprint(current + 1),
		Summary: who + " changed the settings of node " + nodeLabel(name, node) + ": " + describeSettings(settings)}
	if err = feed(ctx, tx, item); err != nil {
		return NodeConfigView{}, err
	}
	tx.wakeNodes()
	if err = tx.Commit(); err != nil {
		return NodeConfigView{}, err
	}
	return s.NodeConfig(ctx, company, node)
}

// describeSettings says what settings set in words: "slots 4, labels ios, drivers.claude on".
func describeSettings(v protocol.NodeSettings) string {
	var parts []string
	if v.Slots != nil {
		parts = append(parts, fmt.Sprintf("slots %d", *v.Slots))
	}
	if v.ConversationSlots != nil {
		parts = append(parts, fmt.Sprintf("conversation slots %d", *v.ConversationSlots))
	}
	if v.Labels != nil {
		parts = append(parts, "labels "+cmpOr(strings.Join(*v.Labels, ", "), "none"))
	}
	for _, name := range protocol.RemoteDrivers {
		if d, ok := v.Drivers[name]; ok && d.Enabled != nil {
			parts = append(parts, name+map[bool]string{true: " on", false: " off"}[*d.Enabled])
		}
	}
	if d := v.Disk; d != nil {
		for _, w := range []struct {
			name string
			v    *float64
		}{{"disk low", d.LowGiB}, {"disk critical", d.CriticalGiB}, {"conversation floor", d.ConversationFloorGiB}} {
			if w.v != nil {
				parts = append(parts, fmt.Sprintf("%s %g GiB", w.name, *w.v))
			}
		}
	}
	if len(parts) == 0 {
		return "all from node.yaml"
	}
	return strings.Join(parts, ", ")
}

func cmpOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// applyNodeReport records what a heartbeat says about the node's capacity and settings: new slots or labels take
// effect for the next claims (and wake the polls), a report is kept for the Nodes page, and a version the node
// refused settings of is recorded once as node_config_refused. It returns the desired settings' version.
func applyNodeReport(ctx context.Context, tx *Tx, node, name string, c *NodeCapacity, r *protocol.NodeConfigReport) (int64, error) {
	if c != nil {
		switch {
		case c.Slots < 1 || c.Slots > protocol.MaxNodeSlots || c.ConversationSlots < 0 || c.ConversationSlots > protocol.MaxNodeSlots:
			return 0, inputError("capacity: slots 1..64 and conversation_slots 0..64 required")
		case len(c.Labels) > 64:
			return 0, inputError("capacity: at most 64 labels")
		}
		if err := validDetected(c.DetectedLabels); err != nil {
			return 0, err
		}
		labels, _ := json.Marshal(nonNil(c.Labels))
		detected, _ := json.Marshal(nonNil(c.DetectedLabels))
		res, err := tx.ExecContext(ctx, `UPDATE nodes SET slots=?,conversation_slots=?,labels=?,detected_labels=? WHERE company_id=? AND id=?
 AND (slots!=? OR conversation_slots!=? OR labels::jsonb!=?::jsonb OR detected_labels!=?::jsonb)`,
			c.Slots, c.ConversationSlots, string(labels), string(detected), tx.company, node, c.Slots, c.ConversationSlots, string(labels), string(detected))
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			tx.wakeNodes() // more room or other labels: queued runs may fit now
		}
	}
	var version int64
	if err := tx.QueryRowContext(ctx, "SELECT version FROM node_configs WHERE company_id=? AND node_id=?", tx.company, node).Scan(&version); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if r == nil {
		return version, nil
	}
	if r.Settings == nil {
		r.Settings = map[string]protocol.NodeSettingState{}
	}
	var before []byte
	if err := tx.QueryRowContext(ctx, "SELECT config_report FROM nodes WHERE company_id=? AND id=?", tx.company, node).Scan(&before); err != nil {
		return 0, err
	}
	var prev protocol.NodeConfigReport
	_ = json.Unmarshal(before, &prev)
	body, _ := json.Marshal(r)
	if _, err := tx.ExecContext(ctx, "UPDATE nodes SET config_report=? WHERE company_id=? AND id=?", string(body), tx.company, node); err != nil {
		return 0, err
	}
	if r.Version == 0 || r.Version == prev.Version && prev.Settings != nil {
		return version, nil
	}
	var refused []string
	for _, k := range slices.Sorted(maps.Keys(r.Settings)) {
		if st := r.Settings[k]; st.State == protocol.SettingRefused {
			refused = append(refused, k+": "+st.Reason)
		}
	}
	if len(refused) == 0 {
		return version, nil
	}
	return version, feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "node_config_refused", EntityID: "node:" + node, CreatedAt: tx.now(), Actor: "node:" + node, Attention: true,
		Ref: fmt.Sprint(r.Version), Summary: "node " + nodeLabel(name, node) + " keeps its node.yaml for " + strings.Join(refused, "; ")})
}
