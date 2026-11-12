package messaging

import "testing"

// TestPeopleTextChoice (artifact-choice-approvals): people read a choice request without the agents' options list
// (they see the options themselves); the delegate's words stay, and other messages keep their numbered lists.
func TestPeopleTextChoice(t *testing.T) {
	id := "0123456789abcdef0123456789abcdef"
	body := "Artifact choice approval " + id + ": three paywall variations\nOptions:\n1. V1 Mascot (paywall@v1) — friendly (recommended)\n2. V2 (paywall@v2)\nlead handed it to you: no owner answer covers it"
	if got, want := PeopleText(body), "three paywall variations\n\nlead handed it to you: no owner answer covers it"; got != want {
		t.Fatalf("%q", got)
	}
	plain := "Plan:\n1. Ask bob (bob@example.com)\n2. Ship"
	if got := PeopleText(plain); got != plain {
		t.Fatalf("%q", got)
	}
}
