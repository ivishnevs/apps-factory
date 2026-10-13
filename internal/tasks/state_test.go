package tasks

import (
	"strings"
	"testing"
)

func ready() Snapshot {
	return Snapshot{Task: Task{ID: "t", Status: "READY", Assignee: "dev"}, AgentConfigured: true, MaxRetries: 2}
}

func TestEvaluateReasons(t *testing.T) {
	live := &Run{ID: "r1", Status: "RUNNING", TaskID: "t"}
	cases := []struct {
		name   string
		change func(*Snapshot)
		reason string
		who    string
	}{
		{"runnable", func(*Snapshot) {}, ReasonRunnable, "scheduler"},
		{"done", func(s *Snapshot) { s.Task.Status = "DONE" }, ReasonTerminal, "nobody"},
		{"cancelled", func(s *Snapshot) { s.Task.Status = "CANCELLED" }, ReasonTerminal, "nobody"},
		{"backlog", func(s *Snapshot) { s.Task.Status = "BACKLOG" }, ReasonBacklog, "owner"},
		{"unassigned", func(s *Snapshot) { s.Task.Assignee = "" }, ReasonUnassigned, "owner"},
		{"unknown agent", func(s *Snapshot) { s.AgentConfigured = false }, ReasonUnassigned, "owner"},
		{"live run", func(s *Snapshot) { s.Task.Status = "IN_PROGRESS"; s.CurrentRun = live }, ReasonLiveRun, "run:r1"},
		{"queued run", func(s *Snapshot) { s.CurrentRun = &Run{ID: "r1", Status: "QUEUED"} }, ReasonLiveRun, "run:r1"},
		{"retries exhausted", func(s *Snapshot) { s.Task.Status, s.Task.Attempt, s.Task.RetryEligible = "FAILED", 3, true }, ReasonParked + ParkEscalation, "owner"},
		{"failure without retry", func(s *Snapshot) { s.Task.Status, s.Task.Attempt = "FAILED", 1 }, ReasonParked + ParkEscalation, "owner"},
		{"unknown outcome", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "run: outcome unknown"
			s.CurrentRun = &Run{ID: "r1", Status: "UNKNOWN"}
		}, ReasonParked + ParkInspect, "owner"},
		{"question to owner", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "message: waiting for answer"
			s.WaitMessage, s.WaitMessageRecipient = "m1", "owner"
		}, ReasonParked + ParkAsk, "owner"},
		{"question to manager", func(s *Snapshot) { s.WaitMessage, s.WaitMessageRecipient = "m1", "manager" }, ReasonParked + ParkAsk, "agent:manager"},
		{"manual block", func(s *Snapshot) { s.Task.Status, s.Task.BlockReason = "BLOCKED", "manual: waiting for keys" }, ReasonParked + ParkBlock, "owner"},
		{"openspec block", func(s *Snapshot) { s.Task.Status, s.Task.BlockReason = "BLOCKED", "openspec: change missing" }, ReasonParked + ParkBlock, "owner"},
		{"dependency", func(s *Snapshot) { s.Dependencies = []Dependency{{"a", "DONE"}, {"b", "IN_REVIEW"}} }, ReasonDependencies, "task:b"},
		{"artifact", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "artifact: waiting for owner approval of layout@1"
			s.UnapprovedArtifacts = []Artifact{{ID: "layout", Version: "1"}}
		}, ReasonArtifacts, "owner"},
		{"retry pending", func(s *Snapshot) { s.Task.Status, s.Task.Attempt, s.Task.RetryEligible = "FAILED", 1, true }, ReasonBackoff, "scheduler"},
		{"in review", func(s *Snapshot) { s.Task.Status = "IN_REVIEW" }, ReasonIdle, "reviewer"},
		{"rework", func(s *Snapshot) { s.Task.Status = "REWORK" }, ReasonIdle, "owner"},
		{"agent busy", func(s *Snapshot) { s.AgentRun = &Run{ID: "r2", Status: "RUNNING", TaskID: "other"} }, ReasonCapacity, "run:r2"},
		{"writer busy", func(s *Snapshot) { s.WriterRun = &Run{ID: "r3", Status: "QUEUED", TaskID: "other"} }, ReasonCapacity, "run:r3"},
		{"project quarantined", func(s *Snapshot) { s.WriterRun = &Run{ID: "r3", Status: "UNKNOWN", TaskID: "other"} }, ReasonCapacity, "owner"},
		// Order: the first false conjunct wins.
		{"backlog before unassigned", func(s *Snapshot) { s.Task.Status, s.Task.Assignee = "BACKLOG", "" }, ReasonBacklog, "owner"},
		{"live run before parks", func(s *Snapshot) { s.CurrentRun = live; s.WaitMessage = "m1" }, ReasonLiveRun, "run:r1"},
		{"escalation before inspect", func(s *Snapshot) {
			s.Task.Status = "FAILED"
			s.CurrentRun = &Run{ID: "r1", Status: "UNKNOWN"}
		}, ReasonParked + ParkEscalation, "owner"},
		{"inspect before ask", func(s *Snapshot) {
			s.CurrentRun = &Run{ID: "r1", Status: "UNKNOWN"}
			s.WaitMessage, s.WaitMessageRecipient = "m1", "owner"
		}, ReasonParked + ParkInspect, "owner"},
		{"parks before dependencies", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "manual: x"
			s.Dependencies = []Dependency{{"b", "READY"}}
		}, ReasonParked + ParkBlock, "owner"},
		{"dependencies before artifacts", func(s *Snapshot) {
			s.Dependencies = []Dependency{{"b", "READY"}}
			s.UnapprovedArtifacts = []Artifact{{ID: "x", Version: "1"}}
		}, ReasonDependencies, "task:b"},
		{"dependencies before capacity", func(s *Snapshot) {
			s.Dependencies = []Dependency{{"b", "READY"}}
			s.AgentRun = &Run{ID: "r2", Status: "RUNNING"}
		}, ReasonDependencies, "task:b"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := ready()
			c.change(&s)
			v := Evaluate(s)
			if v.Reason != c.reason || v.Clearer.Who != c.who || v.TaskID != "t" || v.Status != s.Task.Status {
				t.Fatalf("%+v, want %s by %s", v, c.reason, c.who)
			}
			if v.Reason != ReasonRunnable && (v.Detail == "" || v.Clearer.Action == "") {
				t.Fatalf("reason without detail or action: %+v", v)
			}
		})
	}
}

func TestEvaluateNamesTheResolveCommand(t *testing.T) {
	s := ready()
	s.WriterRun = &Run{ID: "lost", Status: "UNKNOWN", TaskID: "other"}
	if v := Evaluate(s); !strings.Contains(v.Clearer.Action, "factory runs resolve lost") {
		t.Fatal(v)
	}
}
