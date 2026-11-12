package web

import (
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/tasks"
)

// TestChoiceMaterialApproval (artifact-choice-approvals 3.4): a version of a choice shows its own state in the
// Library and the task's materials — chosen (approved), not chosen, or still waiting — with its label and the chosen
// one's.
func TestChoiceMaterialApproval(t *testing.T) {
	opt := func(v, label string) approvals.Option {
		return approvals.Option{Artifact: tasks.Artifact{ID: "paywall", Version: v, URI: "https://example.com/" + v}, Label: label}
	}
	a := approvals.Approval{ID: "ap", Kind: "artifact", Status: "PENDING", Options: []approvals.Option{opt("v1", "V1 Mascot"), opt("v2", "V2 Editorial")}}
	if m := materialApproval(a, "paywall", "v1"); m.Status != "PENDING" || m.Option != "V1 Mascot" || m.Chosen != "" {
		t.Fatalf("%+v", m)
	}
	a.Status, a.Chosen = "APPROVED", 2
	for v, want := range map[string][3]string{"v1": {approvals.NotChosen, "V1 Mascot", "V2 Editorial"}, "v2": {"APPROVED", "V2 Editorial", "V2 Editorial"}} {
		if m := materialApproval(a, "paywall", v); m.Status != want[0] || m.Option != want[1] || m.Chosen != want[2] {
			t.Fatalf("%s: %+v", v, m)
		}
	}
	// A yes/no approval is as before.
	single := approvals.Approval{ID: "s", Status: "REJECTED", Artifact: &tasks.Artifact{ID: "icon", Version: "v1"}}
	if m := materialApproval(single, "icon", "v1"); m.Status != "REJECTED" || m.Option != "" {
		t.Fatalf("%+v", m)
	}
}
