package tasks

import (
	"cmp"
	"fmt"
	"slices"
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
	// ReasonSecretMissing: a secret the assignee's runs need is not set (core-secret-store D9); no run, no retry.
	ReasonSecretMissing = "secret_missing"
	ReasonIdle          = "idle"
	// ReasonPinnedNode: the node holding the task's unpushed work cannot continue it (task-worktrees D8).
	ReasonPinnedNode = "pinned_node_unavailable"
	// ReasonNoCapableNode: no online, unpaused node has the assignee's driver, labels and resources.
	ReasonNoCapableNode = "no_capable_node"
	// ReasonNoReadyNode: capable nodes exist but none is ready for the task's project (node-readiness): the task
	// waits for a node's fix with its retries untouched; it does not escalate.
	ReasonNoReadyNode = "no_ready_node"
	ReasonCapacity    = "capacity"
	// ReasonProjectSetup: another task's run sets the project's repository up first (project-creation D2).
	ReasonProjectSetup = "project_setup"
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
	// ParkRefResetWorkspace is the ref of the block park that waits for the node to reset a diverged worktree.
	ParkRefResetWorkspace = "reset_workspace"
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
	// Person names the person the dependency is assigned to (people-assignees); "" for an agent's task.
	Person string `json:"person,omitempty"`
}

// Child is an open child task (parent_task_id) of a task parked on its children.
type Child struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Assignee string `json:"assignee,omitempty"`
}

// Snapshot is everything Evaluate reads; storage loads it, Core adds catalog facts.
type Snapshot struct {
	Task Task
	// Now is the evaluation time (for the retry backoff); zero ignores the backoff.
	Now time.Time
	// AgentConfigured: the assignee exists in the company's agent catalog.
	AgentConfigured bool
	// Person is the name of the person a person's task is assigned to (people-assignees; storage fills it, else
	// the user id is shown).
	Person string
	// CurrentRun is the task's current run, if any.
	CurrentRun *Run
	// Parks are the task's active parks.
	Parks []Park
	// WakeSeq is the latest trigger, HandledSeq the latest consumed by a successful run; Pending
	// are the triggers in (HandledSeq, WakeSeq].
	WakeSeq, HandledSeq int64
	Pending             []Trigger
	Dependencies        []Dependency
	// Children are the task's open children, loaded when it has a children park (they clear it).
	Children []Child
	// UnapprovedArtifacts are required artifact versions without a current owner approval.
	UnapprovedArtifacts []Artifact
	// Paused are the pauses covering the task: "company", "project:<id>", "agent:<name>",
	// "quota_key:<key>". QuotaPaused lists every paused quota key; Core adds the assignee's one to Paused.
	Paused      []string
	QuotaPaused []string
	// TaskPause is an authority's pause of the task itself ("task" in Paused); nil when none.
	TaskPause *TaskPause
	// AgentRun is the oldest live run of the assignee on another task; AgentLive counts them and
	// AgentProjectLive those in the task's project. AgentCap and AgentProjectCap are the assignee's limits
	// (Core fills them from the catalog; 0 means 1 and the agent cap).
	AgentRun                    *Run
	AgentLive, AgentProjectLive int
	AgentCap, AgentProjectCap   int
	// Run kinds (work-and-conversation-runs): AgentRun, AgentLive and AgentProjectLive count the assignee's work
	// runs. AgentConversationRun is its oldest live conversation run on another task, AgentConversationProjects
	// the projects of those runs (AgentConversationLive their number). AgentConversationCap (0: none) and
	// AgentSharedLimit (conversations count against the work limit) come from the catalog.
	AgentConversationRun      *Run
	AgentConversationLive     int
	AgentConversationProjects []string
	// AgentConversationProjectLive counts those in the task's project (only the shared limit counts them for work).
	AgentConversationProjectLive int
	AgentConversationCap         int
	AgentSharedLimit             bool
	// Conversation is what a conversation task's live run does or waits for (storage fills it).
	Conversation *ConversationState
	// WriterRun is a run of another task holding the project workspace writer slot, including a lost run
	// whose runtime may still run (not released).
	WriterRun *Run
	// ProjectSetup says why the task waits for its project's repository setup, which another task's run
	// performs (project-creation D2); "" when it does not wait.
	ProjectSetup string
	// Nodes are the company's registered (protocol v2) nodes; none means no node
	// reasons. Needs is what the assignee's runs need from a node (Core fills it from the catalog).
	Nodes []Node
	Needs NodeNeeds
	// PinnedNode holds the task's unpushed work ("" when not pinned); PinTimeout is how long it may be
	// offline before the task reports pinned_node_unavailable.
	PinnedNode string
	PinTimeout time.Duration
	// Tie is the task's tie as storage loaded it (node-ties): its work tie (PinnedNode, since, why the work
	// could not be saved) or the session tie of its queued run; nil when none.
	Tie *Tie
	// MissingSecret names the first secret the assignee's runs need that is not set with its kind, as
	// "NAME (agent)" (Core fills it from the catalog and the company's secrets); "" when none.
	MissingSecret string
}

// ConversationState is a conversation task's live run (work-and-conversation-runs): a started run handles the
// messages of Senders since Started (Unix seconds), up to trigger BriefTo; a queued run says in Waiting why it is not
// claimed, in plain words.
type ConversationState struct {
	Senders []string
	Started int64
	BriefTo int64
	Waiting string
}

// TaskPause is an authority's pause of one task (hierarchy D2).
type TaskPause struct {
	Actor  string `json:"actor"`
	Rank   int    `json:"rank"`
	Reason string `json:"reason,omitempty"`
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
	// Wait is the capacity case in structured form (task-board-ux 3.1), set when Reason is capacity.
	Wait *Wait `json:"wait,omitempty"`
	// Tie is the task's tie to a node (node-ties), whatever the reason.
	Tie *Tie `json:"tie,omitempty"`
}

// Wait kinds of a capacity verdict.
const (
	WaitAgent        = "agent"         // the assignee runs Limit of Limit runs
	WaitAgentProject = "agent_project" // the assignee's per-project limit
	WaitWriter       = "writer"        // another run holds the project workspace
	WaitPinnedNode   = "pinned_node"   // the task's unpushed work is on a busy, offline or paused node
	WaitNodes        = "nodes"         // every capable node is full
	// WaitAgentConversation: the assignee's conversation cap (work-and-conversation-runs).
	WaitAgentConversation = "agent_conversation"
)

// sharedConversationRun is the assignee's oldest live conversation run when conversations share the work limit.
func (s Snapshot) sharedConversationRun() *Run {
	if s.AgentSharedLimit {
		return s.AgentConversationRun
	}
	return nil
}

// answering says what a started conversation run handles, in plain words: "answering now: messages from producer
// and gameplay-dev (started 2 min ago)", and that newer messages wait for the next run.
func (c ConversationState) answering(now time.Time, wakeSeq int64) string {
	s := "answering now"
	if len(c.Senders) > 0 {
		s += ": messages from " + joinAnd(c.Senders)
	}
	if c.Started > 0 && !now.IsZero() {
		s += " (started " + ago(now.Sub(time.Unix(c.Started, 0))) + ")"
	}
	if wakeSeq > c.BriefTo {
		s += "; newer messages are next"
	}
	return s
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d/time.Minute))
	}
	return fmt.Sprintf("%dh%02d ago", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// joinAnd joins names as "a, b and c".
func joinAnd(xs []string) string {
	if len(xs) <= 1 {
		return strings.Join(xs, "")
	}
	return strings.Join(xs[:len(xs)-1], ", ") + " and " + xs[len(xs)-1]
}

func uniq(xs []string) []string {
	var out []string
	for _, x := range xs {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

// Wait says what a queued task waits for: the agent's limit, the project workspace, its node or full nodes.
type Wait struct {
	Kind   string `json:"kind"`
	Agent  string `json:"agent,omitempty"`
	Live   int    `json:"live,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	TaskID string `json:"task_id,omitempty"` // the oldest run's task holding the slot
	RunID  string `json:"run_id,omitempty"`
	Node   string `json:"node,omitempty"`
	Lost   bool   `json:"lost,omitempty"`
}

// Evaluate returns the first reason in D3 order why the task does not run now, or runnable, with the task's tie
// to a node when it has one (node-ties).
func Evaluate(s Snapshot) Verdict {
	v := evaluate(s)
	if v.Reason != ReasonTerminal {
		v.Tie = s.tie()
	}
	return v
}

func evaluate(s Snapshot) Verdict {
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
	user, person := PersonUser(t.Assignee)
	if !s.AgentConfigured && !person {
		return v(ReasonUnassigned, "assignee "+t.Assignee+" is not a configured agent", "owner", "assign a configured agent or add its config")
	}
	if r := s.CurrentRun; r != nil && r.Live() {
		// A conversation's clearer says in plain words what its run does or waits for (work-and-conversation-runs).
		if c := s.Conversation; c != nil && t.Kind == KindConversation && !r.Lost {
			reason := ReasonLiveRun
			if n := s.nodeReason(true); r.Status == "QUEUED" && n.Reason != "" {
				reason = n.Reason
			}
			text := c.Waiting
			if r.Status != "QUEUED" {
				text = c.answering(s.Now, s.WakeSeq)
			}
			if text != "" {
				return v(reason, text, "run:"+r.ID, text)
			}
		}
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
			if d.Person != "" {
				// A person's task is waited for, not escalated (people-assignees).
				return v(ReasonDependencies, fmt.Sprintf("waits for %s, with %s (%s)", d.ID, d.Person, d.Status), "task:"+d.ID, d.Person+" moves it to DONE")
			}
			return v(ReasonDependencies, fmt.Sprintf("dependency %s is %s", d.ID, d.Status), "task:"+d.ID, "dependency must reach DONE")
		}
	}
	if len(s.UnapprovedArtifacts) > 0 {
		a := s.UnapprovedArtifacts[0]
		return v(ReasonArtifacts, fmt.Sprintf("artifact %s@%s is not approved", a.ID, a.Version), "owner", "approve the artifact version (factory artifacts approval ...)")
	}
	if person && t.Status != "IN_REVIEW" && t.Status != "BLOCKED" {
		return personVerdict(v, t, cmp.Or(s.Person, user))
	}
	if s.MissingSecret != "" {
		return v(ReasonSecretMissing, "secret "+s.MissingSecret+" is not set", "owner", "set it on the Secrets page or with factory secrets set NAME")
	}
	if len(s.Paused) > 0 {
		scope := s.Paused[0]
		target := strings.Replace(scope, ":", " ", 1)
		if scope == "company" {
			target = "--all"
		}
		if scope == "task" && s.TaskPause != nil {
			detail := "paused by " + s.TaskPause.Actor
			if s.TaskPause.Reason != "" {
				detail += ": " + s.TaskPause.Reason
			}
			return v(ReasonPaused+scope, detail, s.TaskPause.Actor, "factory tasks resume "+t.ID+" VERSION (the pauser or a higher authority)")
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
		// Who the review waits for (human-review-visibility): the agent reviewer, else the task's person
		// (member:USER), else the owner.
		who := "owner"
		if t.Reviewer != "" {
			who = t.Reviewer
		} else if strings.HasPrefix(t.ReviewBy, "member:") {
			who = t.ReviewBy
		}
		return v(ReasonIdle, "submitted, waiting for review by "+who, who, "factory tasks review "+t.ID+" VERSION accept|reject")
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
	conversation := t.Kind == KindConversation
	if conversation && !s.AgentSharedLimit {
		// A conversation does not wait behind work (work-and-conversation-runs); only its own cap, when set.
		if r := s.AgentConversationRun; r != nil && s.AgentConversationCap > 0 && s.AgentConversationLive >= s.AgentConversationCap {
			text := "waiting for a free conversation run: busy in " + joinAnd(uniq(s.AgentConversationProjects))
			return wait(v(ReasonCapacity, fmt.Sprintf("%s (%d of %d conversation runs)", text, s.AgentConversationLive, s.AgentConversationCap), "run:"+r.ID, text),
				Wait{Kind: WaitAgentConversation, Agent: t.Assignee, Live: s.AgentConversationLive, Limit: s.AgentConversationCap, TaskID: r.TaskID, RunID: r.ID})
		}
	} else if r := cmp.Or(s.AgentRun, s.sharedConversationRun()); r != nil {
		live, limit, projectLive := max(s.AgentLive, 1), max(s.AgentCap, 1), s.AgentProjectLive
		if s.AgentSharedLimit {
			// Conversations count against the work limit, as before the change (share_work_limit).
			live, projectLive = max(s.AgentLive+s.AgentConversationLive, 1), s.AgentProjectLive+s.AgentConversationProjectLive
		}
		projectLimit := s.AgentProjectCap
		if projectLimit == 0 {
			projectLimit = limit
		}
		if conversation && (live >= limit || projectLive >= projectLimit) {
			text := "waiting for " + t.Assignee + "'s work run (conversations share its limit)"
			return wait(v(ReasonCapacity, fmt.Sprintf("%s: busy with %d of %d runs, oldest %s (task %s)", text, live, limit, r.ID, r.TaskID), "run:"+r.ID, text),
				Wait{Kind: WaitAgent, Agent: t.Assignee, Live: live, Limit: limit, TaskID: r.TaskID, RunID: r.ID})
		}
		if live >= limit {
			return wait(v(ReasonCapacity, fmt.Sprintf("agent %s is busy with %d of %d runs, oldest %s (task %s)", t.Assignee, live, limit, r.ID, r.TaskID), "run:"+r.ID, "wait for the agent's current run"),
				Wait{Kind: WaitAgent, Agent: t.Assignee, Live: live, Limit: limit, TaskID: r.TaskID, RunID: r.ID})
		}
		if projectLive >= projectLimit {
			return wait(v(ReasonCapacity, fmt.Sprintf("agent %s is busy with %d of %d runs in this project", t.Assignee, projectLive, projectLimit), "run:"+r.ID, "wait for the agent's current run in this project"),
				Wait{Kind: WaitAgentProject, Agent: t.Assignee, Live: projectLive, Limit: projectLimit, TaskID: r.TaskID, RunID: r.ID})
		}
	}
	if r := s.WriterRun; r != nil {
		if r.Lost {
			return wait(v(ReasonCapacity, fmt.Sprintf("project workspace writer slot held by lost run %s whose runtime may still run (task %s)", r.ID, r.TaskID), "node", "the node reports the runtime gone, or factory runs resolve "+r.ID+" continue|retry|cancel EVIDENCE"),
				Wait{Kind: WaitWriter, TaskID: r.TaskID, RunID: r.ID, Lost: true})
		}
		return wait(v(ReasonCapacity, fmt.Sprintf("project workspace writer slot held by run %s (task %s)", r.ID, r.TaskID), "run:"+r.ID, "wait for the run in the project workspace"),
			Wait{Kind: WaitWriter, TaskID: r.TaskID, RunID: r.ID})
	}
	if s.ProjectSetup != "" {
		return v(ReasonProjectSetup, s.ProjectSetup, "node", "wait for the project's repository setup (its first task's run); a failed setup names its reason in the project's feed")
	}
	if n := s.nodeReason(true); n.Reason != "" {
		return v(n.Reason, n.Detail, n.Clearer.Who, n.Clearer.Action)
	}
	return v(ReasonRunnable, "", "scheduler", "dispatch on the next tick")
}

// personVerdict explains a person's task (people-assignees): Core runs nothing for it; the person moves it through
// the same statuses an agent's run would.
func personVerdict(v func(reason, detail, who, action string) Verdict, t Task, name string) Verdict {
	who, detail := t.Assignee, "assigned to "+name
	switch {
	case t.Status == "IN_PROGRESS":
		return v(ReasonIdle, detail+", in progress", who, name+" submits it for review with the pull request (factory tasks submit-pr "+t.ID+" VERSION PR SUMMARY)")
	case t.Status == "REWORK" || t.Status == "READY" && t.ReviewFeedback != "":
		return v(ReasonIdle, detail+"; changes requested: "+t.ReviewFeedback, who, name+" reworks it and submits it again")
	case t.Status == "READY" && t.Implementer == t.Assignee && t.Submission != nil:
		return v(ReasonIdle, detail+"; approved", who, name+" merges the pull request, then marks it done (factory tasks done "+t.ID+" VERSION)")
	}
	return v(ReasonIdle, detail, who, name+" starts it on the board (or factory tasks start "+t.ID+" VERSION)")
}

// wait attaches the structured capacity case to a verdict.
func wait(v Verdict, w Wait) Verdict {
	v.Wait = &w
	return v
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
			if p.RefID == ParkRefResetWorkspace {
				return hold(p.Reason, "node", "the node resets the worktree from the remote branch; the task runs again on its reset_done")
			}
			return hold(p.Reason, p.Who, "remove the cause, then factory tasks ready "+t.ID+" VERSION")
		case ParkChildren:
			// The children's assignees close it, not the owner; an authority may release it early.
			var ids, who []string
			for _, c := range s.Children {
				ids = append(ids, c.ID)
				if c.Assignee != "" && !slices.Contains(who, "agent:"+c.Assignee) {
					who = append(who, "agent:"+c.Assignee)
				}
			}
			if len(who) == 0 {
				who = []string{"children"}
			}
			what := "its open children"
			if len(ids) > 0 {
				what = strings.Join(ids, ", ")
			}
			return hold(p.Reason, strings.Join(who, ","), "wait for "+what+" to close (or factory tasks ready "+t.ID+" VERSION to release it early)")
		case ParkWait:
			return hold(p.Reason, "time", "wakes at its time; a directive or authority comment wakes it now (factory comment "+t.ID+" TEXT)")
		default:
			return hold(p.Reason, p.Who, "clear the "+kind+" park")
		}
	}
	return Verdict{}, false
}
