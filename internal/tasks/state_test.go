package tasks

import (
	"strings"
	"testing"
)

func ready() Snapshot {
	return Snapshot{Task: Task{ID: "t", Status: "READY", Assignee: "dev"}, AgentConfigured: true, WakeSeq: 3, HandledSeq: 1,
		Pending: []Trigger{{Seq: 2, Kind: "ready", Actor: "owner", Authority: true}, {Seq: 3, Kind: "answer", Actor: "owner", Authority: true, RefID: "m0"}}}
}

func TestEvaluateReasons(t *testing.T) {
	live := &Run{ID: "r1", Status: "RUNNING", TaskID: "t"}
	inspect := Park{ID: "p1", Kind: ParkInspect, RefID: "r1", Reason: "run r1 ended with unknown outcome", Who: "owner"}
	ask := Park{ID: "p2", Kind: ParkAsk, RefID: "m1", Reason: "waiting for the answer to question m1", Who: "owner"}
	block := Park{ID: "p3", Kind: ParkBlock, Reason: "manual: waiting for keys", Who: "owner"}
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
		{"retries exhausted", func(s *Snapshot) {
			s.Parks = []Park{{ID: "p0", Kind: ParkEscalation, RefID: "m0", Reason: "retries_exhausted: 3 attempts", Who: "owner"}}
		}, ReasonParked + ParkEscalation, "owner"},
		{"inspection", func(s *Snapshot) { s.Parks = []Park{inspect} }, ReasonParked + ParkInspect, "owner"},
		{"lost run that may still run", func(s *Snapshot) {
			s.Task.Status = "IN_PROGRESS"
			s.CurrentRun = &Run{ID: "r1", Status: "ENDED", Lost: true}
		}, ReasonLiveRun, "node"},
		{"question to owner", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "message: waiting for answer"
			s.Parks = []Park{ask}
		}, ReasonParked + ParkAsk, "owner"},
		{"question to manager", func(s *Snapshot) {
			s.Parks = []Park{{ID: "p", Kind: ParkAsk, RefID: "m1", Reason: "q", Who: "agent:manager"}}
		}, ReasonParked + ParkAsk, "agent:manager"},
		{"manual block", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "manual: waiting for keys"
			s.Parks = []Park{block}
		}, ReasonParked + ParkBlock, "owner"},
		{"children", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "children: waiting for open children"
			s.Parks = []Park{{ID: "p", Kind: ParkChildren, Reason: "after the kids", Who: "owner"}}
			s.Children = []Child{{"k1", "READY", "ios"}, {"k2", "BACKLOG", "ios"}, {"k3", "READY", "web"}}
		}, ReasonParked + ParkChildren, "agent:ios,agent:web"},
		{"unassigned children", func(s *Snapshot) {
			s.Parks = []Park{{ID: "p", Kind: ParkChildren, Reason: "after the kids", Who: "owner"}}
			s.Children = []Child{{ID: "k1", Status: "BACKLOG"}}
		}, ReasonParked + ParkChildren, "children"},
		{"worktree reset", func(s *Snapshot) {
			s.Parks = []Park{{ID: "p", Kind: ParkBlock, RefID: ParkRefResetWorkspace, Reason: "waiting for the node to reset the worktree", Who: "owner"}}
		}, ReasonParked + ParkBlock, "node"},
		{"blocked status without park", func(s *Snapshot) { s.Task.Status, s.Task.BlockReason = "BLOCKED", "manual: x" }, ReasonIdle, "owner"},
		{"dependency", func(s *Snapshot) {
			s.Dependencies = []Dependency{{ID: "a", Status: "DONE"}, {ID: "b", Status: "IN_REVIEW"}}
		}, ReasonDependencies, "task:b"},
		{"artifact", func(s *Snapshot) {
			s.Task.Status, s.Task.BlockReason = "BLOCKED", "artifact: waiting for owner approval of layout@1"
			s.UnapprovedArtifacts = []Artifact{{ID: "layout", Version: "1"}}
		}, ReasonArtifacts, "owner"},
		{"in review by people", func(s *Snapshot) { s.Task.Status = "IN_REVIEW" }, ReasonIdle, "owner"},
		{"in review by an agent", func(s *Snapshot) { s.Task.Status, s.Task.Reviewer, s.Task.Assignee = "IN_REVIEW", "qa", "qa" }, ReasonIdle, "qa"},
		{"in review by a member", func(s *Snapshot) { s.Task.Status, s.Task.ReviewBy = "IN_REVIEW", "member:u1" }, ReasonIdle, "member:u1"},
		{"ready without trigger", func(s *Snapshot) { s.WakeSeq, s.HandledSeq, s.Pending = 3, 3, nil }, ReasonIdle, "owner"},
		{"rework", func(s *Snapshot) { s.Task.Status = "REWORK" }, ReasonIdle, "owner"},
		{"agent busy", func(s *Snapshot) { s.AgentRun = &Run{ID: "r2", Status: "RUNNING", TaskID: "other"} }, ReasonCapacity, "run:r2"},
		{"writer busy", func(s *Snapshot) { s.WriterRun = &Run{ID: "r3", Status: "QUEUED", TaskID: "other"} }, ReasonCapacity, "run:r3"},
		{"writer lost", func(s *Snapshot) { s.WriterRun = &Run{ID: "r3", Status: "ENDED", TaskID: "other", Lost: true} }, ReasonCapacity, "node"},
		{"project setup", func(s *Snapshot) { s.ProjectSetup = "project p's repository is being set up by task t0's run" }, ReasonProjectSetup, "node"},
		// Order: the first false conjunct wins.
		{"backlog before unassigned", func(s *Snapshot) { s.Task.Status, s.Task.Assignee = "BACKLOG", "" }, ReasonBacklog, "owner"},
		{"live run before parks", func(s *Snapshot) { s.CurrentRun = live; s.Parks = []Park{ask} }, ReasonLiveRun, "run:r1"},
		{"escalation before inspect", func(s *Snapshot) {
			s.Parks = []Park{inspect, {ID: "p0", Kind: ParkEscalation, RefID: "m0", Reason: "inspect: run r1", Who: "owner"}}
		}, ReasonParked + ParkEscalation, "owner"},
		{"inspect before ask", func(s *Snapshot) {
			s.Parks = []Park{ask, inspect}
		}, ReasonParked + ParkInspect, "owner"},
		{"parks before dependencies", func(s *Snapshot) {
			s.Parks = []Park{block}
			s.Dependencies = []Dependency{{ID: "b", Status: "READY"}}
		}, ReasonParked + ParkBlock, "owner"},
		{"dependencies before artifacts", func(s *Snapshot) {
			s.Dependencies = []Dependency{{ID: "b", Status: "READY"}}
			s.UnapprovedArtifacts = []Artifact{{ID: "x", Version: "1"}}
		}, ReasonDependencies, "task:b"},
		{"dependencies before capacity", func(s *Snapshot) {
			s.Dependencies = []Dependency{{ID: "b", Status: "READY"}}
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
			if len(v.Pending) != len(s.Pending) {
				t.Fatalf("pending triggers not reported: %+v", v)
			}
		})
	}
}

func TestEvaluateNamesTheResolveCommand(t *testing.T) {
	s := ready()
	s.WriterRun = &Run{ID: "lost", Status: "ENDED", TaskID: "other", Lost: true}
	if v := Evaluate(s); !strings.Contains(v.Clearer.Action, "factory runs resolve lost") {
		t.Fatal(v)
	}
}

// TestCapacityWait: a capacity verdict carries its case in structured form (task-board-ux 3.1), so the web can
// say "AGENT runs N of N" instead of guessing from the detail text.
func TestCapacityWait(t *testing.T) {
	cases := []struct {
		name string
		set  func(*Snapshot)
		want Wait
	}{
		{"agent limit", func(s *Snapshot) {
			s.AgentRun, s.AgentLive, s.AgentCap = &Run{ID: "r2", Status: "RUNNING", TaskID: "other"}, 2, 2
		}, Wait{Kind: WaitAgent, Agent: "dev", Live: 2, Limit: 2, TaskID: "other", RunID: "r2"}},
		{"agent project limit", func(s *Snapshot) {
			s.AgentRun, s.AgentLive, s.AgentCap, s.AgentProjectLive, s.AgentProjectCap = &Run{ID: "r2", Status: "RUNNING", TaskID: "other"}, 1, 3, 1, 1
		}, Wait{Kind: WaitAgentProject, Agent: "dev", Live: 1, Limit: 1, TaskID: "other", RunID: "r2"}},
		{"writer", func(s *Snapshot) { s.WriterRun = &Run{ID: "r3", Status: "RUNNING", TaskID: "other"} }, Wait{Kind: WaitWriter, TaskID: "other", RunID: "r3"}},
		{"lost writer", func(s *Snapshot) { s.WriterRun = &Run{ID: "r3", Status: "ENDED", TaskID: "other", Lost: true} }, Wait{Kind: WaitWriter, TaskID: "other", RunID: "r3", Lost: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := ready()
			c.set(&s)
			v := Evaluate(s)
			if v.Reason != ReasonCapacity || v.Wait == nil || *v.Wait != c.want {
				t.Fatalf("verdict %+v wait %+v, want %+v", v, v.Wait, c.want)
			}
		})
	}
	if v := Evaluate(ready()); v.Wait != nil {
		t.Fatalf("runnable verdict has a wait: %+v", v.Wait)
	}
}
