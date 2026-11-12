package tasks

import (
	"strings"
	"testing"
)

// TestPersonVerdict: a person's task is never runnable and never "not a configured agent"; it says who has it and
// what they do next, and its dependents wait for it naming the person (people-assignees 1.1, 2.2).
func TestPersonVerdict(t *testing.T) {
	s := ready()
	s.Task.Assignee, s.AgentConfigured, s.Person = "member:u1", false, "Ilya"
	v := Evaluate(s)
	if v.Reason != ReasonIdle || v.Detail != "assigned to Ilya" || v.Clearer.Who != "member:u1" || !strings.Contains(v.Clearer.Action, "start") {
		t.Fatalf("%+v", v)
	}
	s.Person = ""
	if v = Evaluate(s); v.Detail != "assigned to u1" {
		t.Fatalf("%+v", v)
	}
	s.Task.Status = "IN_PROGRESS"
	if v = Evaluate(s); v.Reason != ReasonIdle || !strings.Contains(v.Clearer.Action, "submit") {
		t.Fatalf("%+v", v)
	}
	s.Task.Status, s.Task.Implementer, s.Task.Submission = "READY", "member:u1", &Submission{Summary: "menu"}
	if v = Evaluate(s); !strings.Contains(v.Detail, "approved") || !strings.Contains(v.Clearer.Action, "done") {
		t.Fatalf("%+v", v)
	}
	s.Task.ReviewFeedback = "fix the font"
	if v = Evaluate(s); !strings.Contains(v.Detail, "changes requested: fix the font") {
		t.Fatalf("%+v", v)
	}
	s.Task.Status, s.Task.ReviewBy = "IN_REVIEW", "owner"
	if v = Evaluate(s); v.Reason != ReasonIdle || v.Clearer.Who != "owner" {
		t.Fatalf("%+v", v)
	}
	// Open dependencies come first, as for an agent's task.
	s = ready()
	s.Task.Assignee, s.AgentConfigured = "member:u1", false
	s.Dependencies = []Dependency{{ID: "d", Status: "READY"}}
	if v = Evaluate(s); v.Reason != ReasonDependencies {
		t.Fatalf("%+v", v)
	}
	// An agent's task waiting on a person's task names the person.
	s = ready()
	s.Dependencies = []Dependency{{ID: "p", Status: "IN_PROGRESS", Person: "Ilya"}}
	if v = Evaluate(s); v.Reason != ReasonDependencies || v.Detail != "waits for p, with Ilya (IN_PROGRESS)" {
		t.Fatalf("%+v", v)
	}
	// An unknown agent stays unassigned.
	s = ready()
	s.AgentConfigured = false
	if v = Evaluate(s); v.Reason != ReasonUnassigned {
		t.Fatalf("%+v", v)
	}
}

func TestParsePullRequest(t *testing.T) {
	for in, want := range map[string]int{"#200": 200, "200": 200, "https://github.com/o/r/pull/200": 200, " #7 ": 7} {
		if p, ok := ParsePullRequest(in); !ok || p.Number != want {
			t.Fatal(in, p, ok)
		}
	}
	for _, bad := range []string{"", "#0", "pr", "github.com/o/r/pull/1", "https://github.com/o/r/issues/2"} {
		if _, ok := ParsePullRequest(bad); ok {
			t.Fatal(bad)
		}
	}
	if u, ok := PersonUser("member:alice"); !ok || u != "alice" || IsPerson("alice") || IsPerson("member:") {
		t.Fatal(u, ok)
	}
}
