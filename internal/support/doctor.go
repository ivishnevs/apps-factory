package support

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"appfactory/internal/tasks"
)

// The company doctor (setup-assistant 3.1): nodes, disk, drivers and logins, failed runs by cause, stuck tasks and
// pending owner items, each with its fix or runbook. Core builds it (GET /doctor) from its own state;
// `factory doctor --company`, the owner MCP server and the Telegram digest show it.

// Severities of a doctor item.
const (
	Problem = "problem" // something does not run until it is fixed
	Warning = "warning" // it runs, but needs attention
)

// Item is one finding.
type Item struct {
	Area     string `json:"area"` // nodes, disk, drivers, checks, pauses, runs, tasks, pending
	Severity string `json:"severity"`
	Subject  string `json:"subject"`
	Answer
}

// DoctorNode is a node as the doctor sees it.
type DoctorNode struct {
	ID          string       `json:"id"`
	Name        string       `json:"name,omitempty"`
	Online      bool         `json:"online"`
	LastSeen    int64        `json:"last_seen,omitempty"`
	Paused      bool         `json:"paused,omitempty"`
	PauseReason string       `json:"pause_reason,omitempty"`
	Disk        string       `json:"disk,omitempty"` // ok, low, critical
	FreeBytes   uint64       `json:"free_bytes,omitempty"`
	Low         uint64       `json:"low_bytes,omitempty"`
	Critical    uint64       `json:"critical_bytes,omitempty"`
	Drivers     []NodeDriver `json:"drivers"`
	// Checks are the node's failing self-checks (git, forge, net.forge, stack checks).
	Checks []NodeCheck `json:"checks,omitempty"`
}

// NodeCheck is a failing node self-check.
type NodeCheck struct {
	Check  string `json:"check"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// Label is the node's name, else its ID.
func (n DoctorNode) Label() string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
}

// DoctorPause is an owner or quota pause.
type DoctorPause struct {
	Scope  string `json:"scope"`
	Target string `json:"target,omitempty"`
	Reason string `json:"reason,omitempty"`
	Until  int64  `json:"until,omitempty"`
}

// FailedRun is a failed run of the period.
type FailedRun struct {
	ID        string `json:"id"`
	TaskID    string `json:"task_id,omitempty"`
	Agent     string `json:"agent"`
	NodeID    string `json:"node_id,omitempty"`
	Outcome   string `json:"outcome"`
	NodeFault string `json:"node_fault,omitempty"`
	Result    string `json:"result,omitempty"`
}

// TaskWait is a task that does not run, with its verdict and its queued run's reason.
type TaskWait struct {
	TaskID   string        `json:"task_id"`
	Project  string        `json:"project"`
	Assignee string        `json:"assignee,omitempty"`
	Kind     string        `json:"kind,omitempty"`
	Verdict  tasks.Verdict `json:"verdict"`
	Queued   *QueuedWhy    `json:"queued,omitempty"`
}

// Pending are the owner's open items.
type Pending struct {
	Inbox     int `json:"inbox"`
	Approvals int `json:"approvals"`
	Alerts    int `json:"alerts"`
	Inspect   int `json:"inspect"`
	Reviews   int `json:"in_review"`
	Total     int `json:"total"`
}

// DoctorInput is what Core knows.
type DoctorInput struct {
	Company    string
	Now        time.Time
	Since      time.Time // the period of FailedRuns
	Nodes      []DoctorNode
	Pauses     []DoctorPause
	FailedRuns []FailedRun
	Tasks      []TaskWait
	Pending    Pending
}

// RunCause is the failed runs of one cause.
type RunCause struct {
	Cause string   `json:"cause"` // outcome, or node fault kind
	Count int      `json:"count"`
	Runs  []string `json:"runs"`
	Last  string   `json:"last"`
	Answer
}

// StuckTask is a task that waits for something only the owner, a node or its person clears.
type StuckTask struct {
	TaskID   string `json:"task_id"`
	Project  string `json:"project"`
	Assignee string `json:"assignee,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Answer
}

// CompanyDoctor is the doctor's report.
type CompanyDoctor struct {
	Company     string       `json:"company"`
	GeneratedAt int64        `json:"generated_at"`
	Since       int64        `json:"since"`
	OK          bool         `json:"ok"`
	Items       []Item       `json:"items"`
	Nodes       []DoctorNode `json:"nodes"`
	FailedRuns  []RunCause   `json:"failed_runs"`
	StuckTasks  []StuckTask  `json:"stuck_tasks"`
	Pending     Pending      `json:"pending"`
}

// stuckQueued are the queued reasons only someone else clears; agent_busy, no_slot, resource_busy and unclaimed
// clear by themselves.
var stuckQueued = map[string]bool{"paused": true, "node_paused": true, "affinity": true, "pinned": true, "node_disk_critical": true,
	"driver_disabled": true, "node_offline": true, "no_node": true, "not_ready": true, "no_ready_node": true, "labels": true}

// Stuck reports whether a task waits for something only the owner, a node or a person clears.
func Stuck(w TaskWait) bool {
	r := w.Verdict.Reason
	switch {
	case r == tasks.ReasonLiveRun:
		return w.Queued != nil && stuckQueued[w.Queued.Reason] && (w.Queued.Reason != "affinity" || w.Queued.Inactive != "")
	case strings.HasPrefix(r, tasks.ReasonPaused):
		return true
	case r == tasks.ReasonParked+tasks.ParkEscalation, r == tasks.ReasonParked+tasks.ParkInspect:
		return true
	}
	switch r {
	case tasks.ReasonSecretMissing, tasks.ReasonPinnedNode, tasks.ReasonNoCapableNode, tasks.ReasonNodeDiskCritical, tasks.ReasonNoReadyNode:
		return true
	}
	return false
}

// BuildDoctor judges the company's state.
func BuildDoctor(in DoctorInput) CompanyDoctor {
	d := CompanyDoctor{Company: in.Company, GeneratedAt: in.Now.Unix(), Since: in.Since.Unix(), Nodes: in.Nodes, Pending: in.Pending,
		Items: []Item{}, FailedRuns: []RunCause{}, StuckTasks: []StuckTask{}}
	if d.Nodes == nil {
		d.Nodes = []DoctorNode{}
	}
	add := func(area, sev, subject string, a Answer) {
		d.Items = append(d.Items, Item{Area: area, Severity: sev, Subject: subject, Answer: a})
	}
	usable := 0
	if len(in.Nodes) == 0 {
		add("nodes", Problem, "company", Answer{Cause: "no node is registered: nothing can run", Fix: "add a machine: factory nodes enroll, then factory-node enroll CODE and factory-node run", Runbook: RunbookAddMachine})
	}
	for _, n := range in.Nodes {
		label := n.Label()
		switch {
		case !n.Online:
			add("nodes", Problem, label, Answer{Cause: "offline" + lastSeen(in.Now, n.LastSeen), Fix: "start factory-node run on " + label + " (or its launchd/systemd service) and keep the machine awake", Runbook: RunbookAddMachine})
		case n.Paused:
			add("pauses", Warning, label, Answer{Cause: "node paused by the owner" + reason(n.PauseReason), Fix: "factory resume --node " + n.ID, Runbook: RunbookPauses})
		}
		switch n.Disk {
		case "critical":
			add("disk", Problem, label, Answer{Cause: fmt.Sprintf("disk critical: %s free (critical %s): no work or review run starts", tasks.GiB(n.FreeBytes), tasks.GiB(n.Critical)),
				Fix: "free disk on " + label + " (closed worktrees, DerivedData, simulators); it resumes by itself above " + tasks.GiB(n.Critical), Runbook: RunbookDisk})
		case "low":
			add("disk", Warning, label, Answer{Cause: fmt.Sprintf("disk low: %s free (low %s)", tasks.GiB(n.FreeBytes), tasks.GiB(n.Low)), Fix: "free disk on " + label + " before it reaches " + tasks.GiB(n.Critical), Runbook: RunbookDisk})
		}
		for _, dr := range n.Drivers {
			if dr.OK {
				if n.Online && !n.Paused {
					usable++
				}
				continue
			}
			add("drivers", Problem, label+" "+dr.Name, Driver(label, dr))
		}
		for _, c := range n.Checks {
			a := Answer{Cause: c.Check + " fails: " + c.Detail, Fix: c.Hint, Runbook: driverRunbook(c.Check + " " + c.Detail)}
			add("checks", Warning, label+" "+c.Check, a)
		}
	}
	if len(in.Nodes) > 0 && usable == 0 {
		add("nodes", Problem, "company", Answer{Cause: "no online, unpaused node has a usable driver: nothing can run", Fix: "fix the node problems listed here", Runbook: RunbookNoAnswer})
	}
	for _, p := range in.Pauses {
		if p.Scope == "node" {
			continue // listed with its node
		}
		subject := p.Scope
		if p.Target != "" {
			subject += " " + p.Target
		}
		a := Answer{Cause: "paused" + reason(p.Reason), Fix: "factory resume " + resumeArg(p), Runbook: RunbookPauses}
		if p.Until > 0 {
			a.Cause += "; ends by itself at " + time.Unix(p.Until, 0).UTC().Format(time.RFC3339)
		}
		add("pauses", Warning, subject, a)
	}
	// Failed runs by cause: the node fault kind, else the outcome.
	byCause := map[string]*RunCause{}
	order := []string{}
	for _, r := range in.FailedRuns {
		cause := r.Outcome
		if r.NodeFault != "" {
			cause = "node fault: " + r.NodeFault
		}
		c := byCause[cause]
		if c == nil {
			c = &RunCause{Cause: cause, Answer: Outcome(r.Outcome, r.NodeFault, r.Result), Last: r.ID}
			byCause[cause] = c
			order = append(order, cause)
		}
		c.Count++
		if len(c.Runs) < 5 {
			c.Runs = append(c.Runs, r.ID)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return byCause[order[i]].Count > byCause[order[j]].Count })
	for _, cause := range order {
		c := byCause[cause]
		d.FailedRuns = append(d.FailedRuns, *c)
		sev := Warning
		if strings.HasPrefix(cause, "node fault") || c.Count >= 3 {
			sev = Problem
		}
		add("runs", sev, fmt.Sprintf("%d failed run(s): %s", c.Count, cause), c.Answer)
	}
	for _, w := range in.Tasks {
		if !Stuck(w) {
			continue
		}
		a := Waiting(w.Verdict, w.Queued)
		d.StuckTasks = append(d.StuckTasks, StuckTask{TaskID: w.TaskID, Project: w.Project, Assignee: w.Assignee, Kind: w.Kind, Answer: a})
		subject := "task " + w.TaskID
		if w.Kind == tasks.KindConversation {
			subject = "chat with " + w.Assignee + " in " + w.Project
		}
		add("tasks", Problem, subject, a)
	}
	if in.Pending.Total > 0 {
		add("pending", Warning, "owner", Answer{Cause: fmt.Sprintf("%d item(s) wait for you: inbox %d, approvals %d, alerts %d, runs to inspect %d", in.Pending.Total, in.Pending.Inbox, in.Pending.Approvals, in.Pending.Alerts, in.Pending.Inspect),
			Fix: "answer them in the web Inbox, with factory pending, or in Telegram (/inbox)"})
	}
	sort.SliceStable(d.Items, func(i, j int) bool { return d.Items[i].Severity == Problem && d.Items[j].Severity != Problem })
	d.OK = true
	for _, it := range d.Items {
		if it.Severity == Problem {
			d.OK = false
		}
	}
	return d
}

func reason(r string) string {
	if r == "" {
		return ""
	}
	return ": " + r
}

func lastSeen(now time.Time, at int64) string {
	if at == 0 {
		return ""
	}
	return ", last seen " + now.Sub(time.Unix(at, 0)).Round(time.Minute).String() + " ago"
}

func resumeArg(p DoctorPause) string {
	switch p.Scope {
	case "company":
		return "--all"
	case "project", "agent", "node":
		return "--" + p.Scope + " " + p.Target
	case "quota_key":
		return "--quota-key " + p.Target
	}
	return "--" + p.Scope + " " + p.Target
}

// Text is the report for a terminal or a chat: problems first, each with its fix and runbook.
func (d CompanyDoctor) Text() string {
	var s strings.Builder
	if d.OK && len(d.Items) == 0 {
		fmt.Fprintf(&s, "%s: all good (%d nodes, no failed runs or stuck tasks since %s)\n", d.Company, len(d.Nodes), time.Unix(d.Since, 0).UTC().Format("2006-01-02 15:04 UTC"))
		return s.String()
	}
	problems := 0
	for _, it := range d.Items {
		if it.Severity == Problem {
			problems++
		}
	}
	fmt.Fprintf(&s, "%s: %d problem(s), %d warning(s)\n", d.Company, problems, len(d.Items)-problems)
	for _, it := range d.Items {
		mark := "!"
		if it.Severity != Problem {
			mark = "-"
		}
		fmt.Fprintf(&s, "%s [%s] %s: %s\n", mark, it.Area, it.Subject, it.Cause)
		if it.Fix != "" {
			fmt.Fprintf(&s, "    fix: %s\n", it.Fix)
		}
		if it.Runbook != "" {
			fmt.Fprintf(&s, "    runbook: %s\n", RunbookRef(it.Runbook))
		}
	}
	return s.String()
}

// Summary is the report's problems in a few lines for the Telegram digest ("" when there are none).
func (d CompanyDoctor) Summary(limit int) string {
	lines := []string{}
	for _, it := range d.Items {
		if it.Severity != Problem {
			continue
		}
		l := "• " + it.Subject + ": " + it.Cause
		if it.Fix != "" {
			l += " — " + it.Fix
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return ""
	}
	more := ""
	if len(lines) > limit {
		more = fmt.Sprintf("\n…and %d more (factory doctor --company)", len(lines)-limit)
		lines = lines[:limit]
	}
	return "Needs fixing:\n" + strings.Join(lines, "\n") + more
}
