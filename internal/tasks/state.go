package tasks

import (
	"fmt"
	"strings"
)

// Reasons a task does not run now (trigger-log-scheduling D3), in evaluation order. Until the
// trigger log and parks exist, Snapshot derives them from task status, block_reason,
// wait_message_id and run state; Evaluate is diagnostic and does not drive the scheduler.
const (
	ReasonTerminal     = "terminal"
	ReasonBacklog      = "backlog"
	ReasonUnassigned   = "unassigned"
	ReasonLiveRun      = "live_run"
	ReasonParked       = "parked:" // + park kind
	ReasonDependencies = "dependencies"
	ReasonArtifacts    = "artifacts"
	ReasonBackoff      = "backoff"
	ReasonIdle         = "idle"
	ReasonCapacity     = "capacity"
	ReasonRunnable     = "runnable"
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
}

func (r Run) Live() bool {
	return r.Status == "QUEUED" || r.Status == "RUNNING" || r.Status == "CANCELLING"
}

type Dependency struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Snapshot is everything Evaluate reads; storage loads it, Core adds catalog facts.
type Snapshot struct {
	Task Task
	// AgentConfigured: the assignee exists in the company's agent catalog.
	AgentConfigured bool
	// CurrentRun is the task's current run, if any.
	CurrentRun *Run
	// WaitMessage is the unanswered blocking question the task waits on; Recipient answers it.
	WaitMessage          string
	WaitMessageRecipient string
	Dependencies         []Dependency
	// UnapprovedArtifacts are required artifact versions without a current owner approval.
	UnapprovedArtifacts []Artifact
	// MaxRetries is the attempt limit of the assignee's captured config for a failed task.
	MaxRetries int
	// AgentRun is a live run of the assignee on another task (one run per agent).
	AgentRun *Run
	// WriterRun is a run of another task holding the project/workspace writer, including UNKNOWN.
	WriterRun *Run
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
}

// Evaluate returns the first reason in D3 order why the task does not run now, or runnable.
func Evaluate(s Snapshot) Verdict {
	t := s.Task
	v := func(reason, detail, who, action string) Verdict {
		return Verdict{TaskID: t.ID, Status: t.Status, Reason: reason, Detail: detail, Clearer: Clearer{Who: who, Action: action}}
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
	if t.Status == "FAILED" {
		return v(ReasonBackoff, fmt.Sprintf("attempt %d of %d failed", t.Attempt, s.MaxRetries+1), "scheduler", "automatic retry on the next tick")
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
	}
	if r := s.AgentRun; r != nil {
		return v(ReasonCapacity, fmt.Sprintf("agent %s is busy with run %s (task %s)", t.Assignee, r.ID, r.TaskID), "run:"+r.ID, "wait for the agent's current run")
	}
	if r := s.WriterRun; r != nil {
		if r.Status == "UNKNOWN" {
			return v(ReasonCapacity, fmt.Sprintf("project writer held by run %s with unknown outcome (task %s)", r.ID, r.TaskID), "owner", "inspect the workspace, then factory runs resolve "+r.ID+" STATUS EVIDENCE")
		}
		return v(ReasonCapacity, fmt.Sprintf("project writer held by run %s (task %s)", r.ID, r.TaskID), "run:"+r.ID, "wait for the project's current run")
	}
	return v(ReasonRunnable, "", "scheduler", "dispatch on the next tick")
}

// park derives the current model's holds as D3 parks.
func park(s Snapshot, kind string) (Verdict, bool) {
	t := s.Task
	hold := func(detail, who, action string) (Verdict, bool) {
		return Verdict{Detail: detail, Clearer: Clearer{Who: who, Action: action}}, true
	}
	switch kind {
	case ParkEscalation:
		if t.Status == "FAILED" && (!t.RetryEligible || t.Attempt > s.MaxRetries) {
			return hold(fmt.Sprintf("attempt %d failed; automatic retries exhausted or not allowed", t.Attempt), "owner", "factory tasks ready "+t.ID+" VERSION to retry, or cancel")
		}
	case ParkInspect:
		if r := s.CurrentRun; r != nil && r.Status == "UNKNOWN" {
			return hold("run "+r.ID+" ended with unknown outcome", "owner", "inspect the workspace, then factory runs resolve "+r.ID+" STATUS EVIDENCE")
		}
	case ParkAsk:
		if s.WaitMessage != "" {
			who := s.WaitMessageRecipient
			if who != "owner" {
				who = "agent:" + who
			}
			return hold("waiting for the answer to question "+s.WaitMessage, who, "answer message "+s.WaitMessage)
		}
	case ParkBlock:
		if t.Status == "BLOCKED" && (strings.HasPrefix(t.BlockReason, "manual:") || strings.HasPrefix(t.BlockReason, "openspec:")) {
			return hold(t.BlockReason, "owner", "remove the cause, then factory tasks ready "+t.ID+" VERSION")
		}
	}
	return Verdict{}, false
}
