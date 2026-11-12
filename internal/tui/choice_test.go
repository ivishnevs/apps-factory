package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/approvals"
	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// TestInboxChoiceApproval (artifact-choice-approvals 3.1): a choice approval shows its options with label, note and
// recommendation, the dropped ones collapsed below with the delegate's reasons; ←/→ focus an option (dropped ones
// included), enter chooses it with an optional comment, c asks for changes, n rejects all, y only explains.
func TestInboxChoiceApproval(t *testing.T) {
	m, c := demoModel(t, 140, 50)
	ov := m.ov
	opt := func(v, label, note, dropped string) approvals.Option {
		o := approvals.Option{Artifact: tasks.Artifact{ID: "variations", Version: v, URI: "https://example.com/" + v + ".png"}, Label: label, Note: note, Dropped: dropped}
		if dropped != "" {
			o.DroppedBy = "lead"
		}
		return o
	}
	ap := approvals.Approval{ID: "ap-choice", Kind: "artifact", ProjectID: "project-2", Agent: "designer", TaskID: "P2-4", MessageID: "m-choice", Status: "PENDING", Version: 2,
		CreatedAt: DemoAt.Unix() - 60, ExpiresAt: DemoAt.Unix() + 3600, Reason: "three paywall variations", Delegate: "lead", Escalated: true,
		Options: []approvals.Option{opt("v1", "V1 Mascot", "friendly", ""), opt("v2", "V2 Editorial", "", "off-brief palette"), opt("v3", "V3 Dark", "dark theme", "")}, Recommended: 1}
	ov.Approvals = []approvals.Approval{ap}
	ov.Inbox = []messaging.Message{{ID: "m-choice", ProjectID: "project-2", TaskID: "P2-4", Sender: "designer", Recipient: "owner", MessageType: "question", Body: "Pick a paywall", Status: "ESCALATED", Version: 1, CreatedAt: DemoAt.Unix() - 60, ApprovalID: "ap-choice"}}
	m.Update(overviewMsg{ov: ov})
	press(m, "5")
	s := ansi.Strip(m.Render())
	for _, want := range []string{"Choose one of 3 versions", "1. V1 Mascot", "★ recommended", "friendly", "dropped by lead (still choosable):", "2. V2 Editorial", "✕ off-brief palette", "enter  choose", "c  changes", "n  reject all"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	// Kept options first, then the dropped one: right twice focuses V2, which is still choosable.
	if strings.Index(s, "3. V3 Dark") > strings.Index(s, "2. V2 Editorial") {
		t.Fatal("dropped option before a kept one")
	}
	key(m, "y")
	if !m.toastEr || !strings.Contains(m.toast, "enter chooses") {
		t.Fatal(m.toast)
	}
	press(m, "right", "right")
	key(m, "enter")
	typeText(m, "the darker one")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "choose" || a.ApprovalID != "ap-choice" || a.Option != 2 || a.Text != "the darker one" || a.Version != 2 {
		t.Fatalf("%+v", a)
	}
	key(m, "left")
	key(m, "enter")
	key(m, "enter") // no comment: choosing needs none
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "choose" || a.Option != 3 || a.Text != "" {
		t.Fatalf("%+v", a)
	}
	key(m, "c")
	key(m, "enter")
	if m.input == nil || !strings.Contains(m.toast, "say what to change") {
		t.Fatal("changes without text accepted")
	}
	typeText(m, "mix V1's icon with V3's palette")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "changes" || a.Text != "mix V1's icon with V3's palette" {
		t.Fatalf("%+v", a)
	}
	key(m, "n")
	typeText(m, "none follows the brief")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "reject" || a.Text != "none follows the brief" {
		t.Fatalf("%+v", a)
	}
}
