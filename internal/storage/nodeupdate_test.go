package storage

import (
	"testing"

	"appfactory/internal/protocol"
)

// TestDecideNodeUpdate (node-packaging-updates 3.4): a node behind the release is offered it under auto, under
// approve once approved; a failed attempt is not offered again until a retry (a new approval); development builds,
// nodes without a version and nodes on or past the release never are; "update now" rides on the offer.
func TestDecideNodeUpdate(t *testing.T) {
	const rel = "2026.11.13.0900"
	failed := &protocol.NodeUpdateStatus{State: protocol.UpdateFailed, Version: rel}
	cases := []struct {
		name    string
		n       Node
		policy  string
		offered bool
		wait    string
		attempt int64
		now     bool
	}{
		{"auto, behind", Node{NodeRegistration: NodeRegistration{Version: "2026.11.12.1500"}}, protocol.NodeUpdateAuto, true, "", 0, false},
		{"auto, on the release", Node{NodeRegistration: NodeRegistration{Version: rel + "+abc"}}, protocol.NodeUpdateAuto, false, "", 0, false},
		{"auto, newer than Core", Node{NodeRegistration: NodeRegistration{Version: "2026.12.01"}}, protocol.NodeUpdateAuto, false, "", 0, false},
		{"dev build", Node{NodeRegistration: NodeRegistration{Version: "dev"}}, protocol.NodeUpdateAuto, false, "", 0, false},
		{"no version", Node{}, protocol.NodeUpdateAuto, false, "", 0, false},
		{"approve, not approved", Node{NodeRegistration: NodeRegistration{Version: "2026.11.12.1500"}}, protocol.NodeUpdateApprove, false, UpdateWaitApproval, 0, false},
		{"approve, an older release approved", Node{NodeRegistration: NodeRegistration{Version: "2026.11.12.1500"}, UpdateApproved: "2026.11.12.1600", UpdateApprovedAt: 5}, protocol.NodeUpdateApprove, false, UpdateWaitApproval, 0, false},
		{"approve, approved now", Node{NodeRegistration: NodeRegistration{Version: "2026.11.12.1500"}, UpdateApproved: rel, UpdateApprovedAt: 7, UpdateNow: rel}, protocol.NodeUpdateApprove, true, "", 7, true},
		{"auto, failed", Node{NodeRegistration: NodeRegistration{Version: "2026.11.12.1500", Update: failed}}, protocol.NodeUpdateAuto, false, UpdateWaitFailed, 0, false},
		{"auto, failed then retried", Node{NodeRegistration: NodeRegistration{Version: "2026.11.12.1500", Update: failed}, UpdateApproved: rel, UpdateApprovedAt: 9}, protocol.NodeUpdateAuto, true, "", 9, false},
		{"auto, an older release failed", Node{NodeRegistration: NodeRegistration{Version: "2026.11.12.1500", Update: &protocol.NodeUpdateStatus{State: protocol.UpdateFailed, Version: "2026.11.12.1600"}}}, protocol.NodeUpdateAuto, true, "", 0, false},
	}
	for _, c := range cases {
		offer, wait := DecideNodeUpdate(c.n, c.policy, rel)
		if (offer != nil) != c.offered || wait != c.wait || offer != nil && (offer.Version != rel || offer.Attempt != c.attempt || offer.Now != c.now) {
			t.Errorf("%s: %+v %q", c.name, offer, wait)
		}
	}
	if offer, _ := DecideNodeUpdate(Node{NodeRegistration: NodeRegistration{Version: "1.0"}}, protocol.NodeUpdateAuto, ""); offer != nil {
		t.Error("offered without a release")
	}
}
