package tasks

import (
	"fmt"
	"strings"
	"time"
)

// Reasons a task does not run now (trigger-log-scheduling D3), in evaluation order. Parks and
// triggers come from the trigger log; an escalation is a park (FAILED is retired, reliable-recovery).
const (
	ReasonTerminal     = "terminal"
	ReasonBacklog      = "backlog"
	ReasonUnassigned   = "unassigned"
	ReasonLiveRun      = "live_run"
	ReasonParked       = "parked:" // + park kind
	ReasonDependencies = "dependencies"
	ReasonArtifacts    = "artifacts"
	ReasonPaused       = "paused:" // + scope
	ReasonBackoff      = "backoff"
	ReasonIdle         = "idle"
	// ReasonPinnedNode: the node holding the task's unpushed work cannot continue it (task-worktrees D8).
	ReasonPinnedNode = "pinned_node_unavailable"
	// ReasonNoCapableNode: no online, unpaused node has the assignee's driver, labels and resources.
	ReasonNoCapableNode = "no_capable_node"
	ReasonCapacity      = "capacity"
	ReasonRunnable      = "runnable"
)

// Park kinds, ordered so that what a human must act on comes first.
const (
	ParkEscalation = "escalation"
	ParkInspect    = "inspect"
	ParkWait       = "wait"
	ParkAsk        = "ask"
	ParkBlock      = "block"
	ParkChildren   = "children"
)

var parkOrder = []string{ParkEscalation, ParkInspect, ParkWait, ParkAsk, ParkBlock, ParkChildren}

// Run is the state of a run relevant to scheduling.
type Run struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	TaskID string `json:"task_id,omitempty"`
	// Lost: Core ended the run lost while its runtime may still run (reliable-recovery D3); it holds its
	// task and workspace until its node proves the runtime gone.
	Lost bool `json:"lost,omitempty"`
}

// Live: the run is not ENDED (queued, claimed, running or cancelling), or lost with its runtime maybe running.
func (r Run) Live() bool {
	return r.Status != "ENDED" || r.Lost
}

// Park is an active hold on a task; Who clears it.
type Park struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	RefID  string `json:"ref_id,omitempty"`
	Reason string `json:"reason"`
	Who    string `json:"who"`
}

// Trigger is a logged reason to run the task.
type Trigger struct {
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Actor     string `json:"actor"`
	Authority bool   `json:"authority"`
	RefID     string `json:"ref_id,omitempty"`
}

type Dependency struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Snapshot is everything Evaluate reads; storage loads it, Core adds catalog facts.
type Snapshot struct {
	Task Task
	// Now is the evaluation time (for the retry backoff); zero ignores the backoff.
	Now time.Time
	// AgentConfigured: the assignee exists in the company's agent catalog.
	AgentConfigured bool
	// CurrentRun is the task's current run, if any.
	CurrentRun *Run
	// Parks are the task's active parks.
	Parks []Park
	// WakeSeq is the latest trigger, HandledSeq the latest consumed by a successful run; Pending
	// are the triggers in (HandledSeq, WakeSeq].
	WakeSeq, HandledSeq int64
	Pending             []Trigger
	Dependencies        []Dependency
	// UnapprovedArtifacts are required artifact versions without a current owner approval.
	UnapprovedArtifacts []Artifact
	// Paused are the pauses covering the task: "company", "project:<id>", "agent:<name>",
	// "quota_key:<key>". QuotaPaused lists every paused quota key; Core adds the assignee's one to Paused.
	Paused      []string
	QuotaPaused []string
	// AgentRun is the oldest live run of the assignee on another task; AgentLive counts them and
	// AgentProjectLive those in the task's project. AgentCap and AgentProjectCap are the assignee's limits
	// (Core fills them from the catalog; 0 means 1 and the agent cap).
	AgentRun                    *Run
	AgentLive, AgentProjectLive int
	AgentCap, AgentProjectCap   int
	// WriterRun is a run of another task holding the project workspace writer slot, including a lost run
	// whose runtime may still run (not released).
	WriterRun *Run
	// Nodes are the company's registered (protocol v2) nodes; none means v1 workers only and no node
	// reasons. Needs is what the assignee's runs need from a node (Core fills it from the catalog).
	Nodes []Node
	Needs NodeNeeds
	// PinnedNode holds the task's unpushed work ("" when not pinned); PinTimeout is how long it may be
	// offline before the task reports pinned_node_unavailable.
	PinnedNode string
	PinTimeout time.Duration
}

// Clearer names who or what removes the reason and how.
type Clearer struct {
	Who    string `json:"who"`
	Action string `json:"action"`
}

type Verdict struct {
	TaskID  string  `json:"task_id"`
	Status  string  `json:"status"`
	Reason  string  `json:"reason"`
	Detail  string  `json:"detail,omitempty"`
	Clearer Clearer `json:"clearer"`
	// Pending triggers the next run of the task will be briefed on.
	Pending []Trigger `json:"pending_triggers,omitempty"`
}

// Evaluate returns the first reason in D3 order why the task does not run now, or runnable.
func Evaluate(s Snapshot) Verdict {
	t := s.Task
	v := func(reason, detail, who, action string) Verdict {
		return Verdict{TaskID: t.ID, Status: t.Status, Reason: reason, Detail: detail, Clearer: Clearer{Who: who, Action: action}, Pending: s.Pending}
	}
	switch t.Status {
	case "DONE", "CANCELLED":
		return v(ReasonTerminal, "task is "+t.Status, "nobody", "final state")
	case "BACKLOG":
		return v(ReasonBacklog, "not released for scheduling", "owner", "factory tasks ready "+t.ID+" VERSION")
	}
	if t.Assignee == "" {
		return v(ReasonUnassigned, "no assignee", "owner", "assign an agent (factory tasks update "+t.ID+")")
	}
	if !s.AgentConfigured {
		return v(ReasonUnassigned, "assignee "+t.Assignee+" is not a configured agent", "owner", "assign a configured agent or add its config")
	}
	if r := s.CurrentRun; r != nil && r.Live() {
		// A queued run nobody can claim: the node reason says why it waits (trigger-log-scheduling D4).
		if n := s.nodeReason(true); r.Status == "QUEUED" && n.Reason != "" {
			return v(n.Reason, n.Detail+" (run "+r.ID+" is QUEUED)", n.Clearer.Who, n.Clearer.Action)
		}
		if r.Lost {
			return v(ReasonLiveRun, "run "+r.ID+" is lost and its runtime may still run", "node", "the node reports the runtime gone, or factory runs resolve "+r.ID+" continue|retry|cancel EVIDENCE")
		}
		return v(ReasonLiveRun, "run "+r.ID+" is "+r.Status, "run:"+r.ID, "wait for it to finish or factory runs cancel "+r.ID)
	}
	for _, kind := range parkOrder {
		if p, ok := park(s, kind); ok {
			return v(ReasonParked+kind, p.Detail, p.Clearer.Who, p.Clearer.Action)
		}
	}
	for _, d := range s.Dependencies {
		if d.Status != "DONE" {
			return v(ReasonDependencies, fmt.Sprintf("dependency %s is %s", d.ID, d.Status), "task:"+d.ID, "dependency must reach DONE")
		}
	}
	if len(s.UnapprovedArtifacts) > 0 {
		a := s.UnapprovedArtifacts[0]
		return v(ReasonArtifacts, fmt.Sprintf("artifact %s@%s is not approved", a.ID, a.Version), "owner", "approve the artifact version (factory artifacts approval ...)")
	}
	if len(s.Paused) > 0 {
		scope := s.Paused[0]
		target := strings.Replace(scope, ":", " ", 1)
		if scope == "company" {
			target = "--all"
		}
		if strings.HasPrefix(scope, "quota_key:") {
			return v(ReasonPaused+scope, "subscription limit reached", "time", "the pause ends when the limit resets (or factory resume "+target+")")
		}
		return v(ReasonPaused+scope, "paused by the owner", "owner", "factory resume "+target)
	}
	if t.Status == "READY" && t.NotBefore > 0 && !s.Now.IsZero() && s.Now.Unix() < t.NotBefore {
		return v(ReasonBackoff, fmt.Sprintf("automatic retry after %s (%d failed in a row, %d of %d attempts without progress)", time.Unix(t.NotBefore, 0).UTC().Format(time.RFC3339), t.FailStreak, t.AttemptsNoProgress, 3), "time", "the retry starts after the backoff (or factory tasks ready "+t.ID+" VERSION now)")
	}
	switch t.Status {
	case "IN_REVIEW":
		return v(ReasonIdle, "submitted, waiting for review", "reviewer", "factory tasks review "+t.ID+" VERSION accept|reject")
	case "REWORK":
		return v(ReasonIdle, "review rejected; rework is not released", "owner", "factory tasks ready "+t.ID+" VERSION")
	case "IN_PROGRESS":
		return v(ReasonIdle, "in progress without a live run", "owner", "inspect the last attempt (factory tasks attempts "+t.ID+")")
	case "BLOCKED":
		return v(ReasonIdle, t.BlockReason, "owner", "factory tasks ready "+t.ID+" VERSION")
	case "READY":
		if s.WakeSeq <= s.HandledSeq {
			return v(ReasonIdle, "no unhandled trigger", "owner", "factory tasks ready "+t.ID+" VERSION")
		}
	}
	if n := s.nodeReason(false); n.Reason != "" {
		return v(n.Reason, n.Detail, n.Clearer.Who, n.Clearer.Action)
	}
	if r := s.AgentRun; r != nil {
		live, limit := max(s.AgentLive, 1), max(s.AgentCap, 1)
		projectLimit := s.AgentProjectCap
		if projectLimit == 0 {
			projectLimit = limit
		}
		if live >= limit {
			return v(ReasonCapacity, fmt.Sprintf("agent %s is busy with %d of %d runs, oldest %s (task %s)", t.Assignee, live, limit, r.ID, r.TaskID), "run:"+r.ID, "wait for the agent's current run")
		}
		if s.AgentProjectLive >= projectLimit {
			return v(ReasonCapacity, fmt.Sprintf("agent %s is busy with %d of %d runs in this project", t.Assignee, s.AgentProjectLive, projectLimit), "run:"+r.ID, "wait for the agent's current run in this project")
		}
	}
	if r := s.WriterRun; r != nil {
		if r.Lost {
			return v(ReasonCapacity, fmt.Sprintf("project workspace writer slot held by lost run %s whose runtime may still run (task %s)", r.ID, r.TaskID), "node", "the node reports the runtime gone, or factory runs resolve "+r.ID+" continue|retry|cancel EVIDENCE")
		}
		return v(ReasonCapacity, fmt.Sprintf("project workspace writer slot held by run %s (task %s)", r.ID, r.TaskID), "run:"+r.ID, "wait for the run in the project workspace")
	}
	if n := s.nodeReason(true); n.Reason != "" {
		return v(n.Reason, n.Detail, n.Clearer.Who, n.Clearer.Action)
	}
	return v(ReasonRunnable, "", "scheduler", "dispatch on the next tick")
}

// park returns the first active park of a kind.
func park(s Snapshot, kind string) (Verdict, bool) {
	t := s.Task
	hold := func(detail, who, action string) (Verdict, bool) {
		return Verdict{Detail: detail, Clearer: Clearer{Who: who, Action: action}}, true
	}
	if kind == ParkEscalation {
		for _, p := range s.Parks {
			if p.Kind == ParkEscalation {
				return hold(p.Reason, p.Who, "answer escalation question "+p.RefID+" (factory answer "+p.RefID+" ANSWER)")
			}
		}
		return Verdict{}, false
	}
	for _, p := range s.Parks {
		if p.Kind != kind {
			continue
		}
		switch kind {
		case ParkInspect:
			return hold(p.Reason, p.Who, "inspect the workspace, then factory runs resolve "+p.RefID+" continue|retry|cancel EVIDENCE")
		case ParkAsk:
			return hold(p.Reason, p.Who, "answer message "+p.RefID)
		case ParkBlock:
			return hold(p.Reason, p.Who, "remove the cause, then factory tasks ready "+t.ID+" VERSION")
		case ParkWait:
			return hold(p.Reason, "time", "wakes at its time; a directive or authority comment wakes it now (factory comment "+t.ID+" TEXT)")
		default:
			return hold(p.Reason, p.Who, "clear the "+kind+" park")
		}
	}
	return Verdict{}, false
}
