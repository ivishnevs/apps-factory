package storage

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/protocol"
)

func nodeNamed(t *testing.T, s *Store, id string) Node {
	t.Helper()
	nodes, err := s.Nodes(context.Background(), DefaultCompany)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.NodeID == id {
			return n
		}
	}
	t.Fatalf("node %s not listed", id)
	return Node{}
}

// TestDetectedLabelsSelect (node-packaging-updates 3.6): labels a node detects select runs like the labels its owner
// sets (claim, offers, queued reasons), and the Nodes list keeps them apart; a heartbeat's capacity updates both.
func TestDetectedLabelsSelect(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	register(t, s, "mac", 1, func(r *NodeRegistration) {
		r.Labels, r.DetectedLabels, r.Version = []string{"team-a"}, []string{"macos", "arm64", "xcode"}, "2026.11.12.1500"
	})
	n := nodeNamed(t, s, "mac")
	if !slices.Equal(n.Labels, []string{"team-a"}) || !slices.Equal(n.DetectedLabels, []string{"macos", "arm64", "xcode"}) || n.Version != "2026.11.12.1500" {
		t.Fatalf("%+v", n)
	}
	queue(t, s, "ios", "p1", "ios-dev", `{"spec":{"labels":["macos","xcode","team-a"]}}`)
	offers, err := s.Offers(ctx, DefaultCompany, "mac", 1, map[string]int{"codex": 1}, nil)
	if err != nil || len(offers) != 1 {
		t.Fatalf("offers %+v %v", offers, err)
	}
	if q, err := s.QueuedWhy(ctx, DefaultCompany, "ios"); err != nil || q.Reason != QueuedUnclaimed {
		t.Fatalf("%+v %v", q, err)
	}
	if _, err = claimV2(s, "ios", "mac", 1, "1-0-1"); err != nil {
		t.Fatal(err)
	}
	// A run needing a GPU: the node detects none, then a heartbeat reports one.
	queue(t, s, "ml", "p2", "ml-dev", `{"spec":{"labels":["gpu"]}}`)
	_, err = claimV2(s, "ml", "mac", 1, "1-0-2")
	refused(t, err, true, "capacity")
	if _, err = s.NodeHeartbeat(ctx, DefaultCompany, "mac", NodeHeartbeat{Epoch: 1, Health: json.RawMessage("{}"),
		Capacity: &NodeCapacity{Slots: 2, ConversationSlots: 1, Labels: []string{"team-a"}, DetectedLabels: []string{"macos", "arm64", "xcode", "gpu"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err = claimV2(s, "ml", "mac", 1, "1-0-2"); err != nil {
		t.Fatal(err)
	}
	if n = nodeNamed(t, s, "mac"); n.ConversationSlots != 1 || !slices.Contains(n.DetectedLabels, "gpu") {
		t.Fatalf("%+v", n)
	}
	// Invalid detected labels are refused.
	if _, err = s.RegisterNode(ctx, DefaultCompany, NodeRegistration{NodeID: "bad", Name: "bad", Epoch: 1, ProtocolVersion: 2, Slots: 1, DetectedLabels: []string{"os:macos"}}); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

// TestMinNodeVersion (node-packaging-updates C): a node below min_node_version registers and is listed with
// update_required, gets no offer, its claims are refused with update_required, queued runs say so, and its register
// raises node_update_required; a supported or development version works.
func TestMinNodeVersion(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	s.MinNodeVersion = "2026.11.12"
	register(t, s, "old", 1, func(r *NodeRegistration) { r.Version = "2026.11.01.0900" })
	register(t, s, "older", 1) // no version at all: it predates version reporting
	register(t, s, "new", 1, func(r *NodeRegistration) { r.Version = "2026.11.12.1530+abc123" })
	register(t, s, "dev", 1, func(r *NodeRegistration) { r.Version = "dev" })
	for id, want := range map[string]bool{"old": true, "older": true, "new": false, "dev": false} {
		if n := nodeNamed(t, s, id); n.UpdateRequired != want || n.MinVersion != "2026.11.12" {
			t.Fatalf("%s: %+v", id, n)
		}
	}
	if ev := nodeEvents(t, s, "node_update_required"); len(ev) != 2 || !ev[0].Attention || !strings.Contains(ev[0].Summary, "Core needs 2026.11.12") {
		t.Fatalf("%+v", ev)
	}
	queue(t, s, "r1", "p1", "dev", "")
	if offers, err := s.Offers(ctx, DefaultCompany, "old", 1, map[string]int{"codex": 2}, nil); err != nil || len(offers) != 0 {
		t.Fatalf("old node offered %+v %v", offers, err)
	}
	_, err := claimV2(s, "r1", "old", 1, "1-0-1")
	refused(t, err, true, "update_required")
	var r *Refusal
	if errors.As(err, &r); !strings.Contains(r.Detail, "version 2026.11.01.0900, Core needs 2026.11.12") {
		t.Fatal(r.Detail)
	}
	q, err := s.QueuedWhy(ctx, DefaultCompany, "r1")
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, n := range q.Nodes {
		reasons[n.Node] = n.Reason
	}
	if reasons["old"] != QueuedNodeUpdate || reasons["older"] != QueuedNodeUpdate || reasons["new"] == QueuedNodeUpdate {
		t.Fatalf("%+v", q.Nodes)
	}
	if offers, err := s.Offers(ctx, DefaultCompany, "new", 1, map[string]int{"codex": 1}, nil); err != nil || len(offers) != 1 {
		t.Fatalf("new node: %+v %v", offers, err)
	}
	if _, err = claimV2(s, "r1", "dev", 1, "1-0-2"); err != nil {
		t.Fatal(err)
	}
}

// TestNodeConfig (node-packaging-updates B): the owner's settings get a new version with a feed event naming who
// changed what; a stale base is a conflict; every setting stays pending until the node reports the version, then
// shows applied or refused (one node_config_refused per version); invalid settings are refused before they are kept.
func TestNodeConfig(t *testing.T) {
	s, _ := clockStore(t)
	ctx := context.Background()
	co := DefaultCompany
	register(t, s, "n1", 1)
	if v, err := s.NodeConfig(ctx, co, "n1"); err != nil || v.Version != 0 || len(v.States) != 0 {
		t.Fatalf("%+v %v", v, err)
	}
	if _, err := s.NodeConfig(ctx, co, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	slots, labels, on := 4, []string{"ios", "team-a", "ios"}, true
	v, err := s.SetNodeConfig(ctx, co, "n1", protocol.NodeSettings{Slots: &slots, Labels: &labels, Drivers: map[string]protocol.NodeDriverSettings{"claude": {Enabled: &on}}}, 0, "user:u1", "Ann")
	if err != nil {
		t.Fatal(err)
	}
	if v.Version != 1 || !slices.Equal(*v.Desired.Labels, []string{"ios", "team-a"}) || v.UpdatedBy != "user:u1" {
		t.Fatalf("%+v", v)
	}
	for _, k := range []string{"slots", "labels", "drivers.claude"} {
		if st := v.States[k]; st.State != protocol.SettingPending || !strings.Contains(st.Reason, "update it") {
			t.Fatalf("%s: %+v", k, st)
		}
	}
	ev := nodeEvents(t, s, "node_config_changed")
	if len(ev) != 1 || ev[0].Actor != "user:u1" || ev[0].Person != "Ann" || !strings.Contains(ev[0].Summary, "Ann changed the settings of node mini-n1 (n1): slots 4, labels ios, team-a, claude on") {
		t.Fatalf("%+v", ev)
	}
	if _, err = s.SetNodeConfig(ctx, co, "n1", protocol.NodeSettings{}, 5, "owner", ""); !errors.Is(err, ErrConflict) {
		t.Fatal("stale base", err)
	}
	bad := 0
	if _, err = s.SetNodeConfig(ctx, co, "n1", protocol.NodeSettings{Slots: &bad}, 0, "owner", ""); !errors.Is(err, ErrInput) {
		t.Fatal("slots 0", err)
	}
	// The poll and heartbeat carry the version.
	a, err := s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 1, Health: json.RawMessage("{}")})
	if err != nil || a.ConfigVersion != 1 {
		t.Fatalf("%+v %v", a, err)
	}
	// The node applied version 1: slots capped, labels refused.
	report := &protocol.NodeConfigReport{Version: 1, Settings: map[string]protocol.NodeSettingState{
		"slots":          {State: protocol.SettingApplied, Reason: "capped at slots_max 3"},
		"labels":         {State: protocol.SettingRefused, Reason: "node.yaml keeps it local (remote: {labels: false})"},
		"drivers.claude": {State: protocol.SettingApplied}}}
	for range 2 {
		if _, err = s.NodeHeartbeat(ctx, co, "n1", NodeHeartbeat{Epoch: 1, Health: json.RawMessage("{}"), Config: report,
			Capacity: &NodeCapacity{Slots: 3, ConversationSlots: 1, Labels: []string{}, DetectedLabels: []string{"linux"}}}); err != nil {
			t.Fatal(err)
		}
	}
	n := nodeNamed(t, s, "n1")
	if n.Slots != 3 || n.Config.Version != 1 || n.Config.Report == nil || n.Config.States["slots"].State != protocol.SettingApplied ||
		n.Config.States["labels"].State != protocol.SettingRefused || n.Config.States["drivers.claude"].State != protocol.SettingApplied {
		t.Fatalf("%+v", n.Config)
	}
	if ev := nodeEvents(t, s, "node_config_refused"); len(ev) != 1 || !ev[0].Attention || !strings.Contains(ev[0].Summary, "labels: node.yaml keeps it local") {
		t.Fatalf("%+v", ev)
	}
	// Version 2 is pending until the node applies it.
	if v, err = s.SetNodeConfig(ctx, co, "n1", protocol.NodeSettings{Slots: &slots}, 1, "owner", ""); err != nil || v.States["slots"].State != protocol.SettingPending || !strings.Contains(v.States["slots"].Reason, "applied version 1, not 2") {
		t.Fatalf("%+v %v", v, err)
	}
	// The row goes with the node.
	if _, err = s.DB.ExecContext(ctx, "DELETE FROM nodes WHERE company_id=? AND id=?", co, "n1"); err != nil {
		t.Fatal(err)
	}
	if version, err := s.NodeConfigVersion(ctx, co, "n1"); err != nil || version != 0 {
		t.Fatal(version, err)
	}
}
