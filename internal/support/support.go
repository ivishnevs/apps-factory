// Package support turns Factory's own state into answers for the owner (setup-assistant): why a task, chat or run
// waits, what a run's failure and token usage mean, each with its fix and the runbook of the built-in factory-setup
// skill that walks through it. Core's company doctor (GET /doctor), the owner MCP server (factory mcp) and
// `factory doctor --company` share these texts, so every surface names the same cause and the same fix.
package support

import (
	"fmt"
	"sort"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Skill is the built-in skill (source factory) whose runbooks the answers name.
const Skill = "factory-setup"

// Runbooks of the factory-setup skill (skills/factory-setup/references/<name>.md). Each ends with a proving check.
const (
	RunbookFirstSetup = "first-setup"
	RunbookPaperclip  = "moving-in-from-paperclip"
	RunbookAddMachine = "adding-a-machine"
	RunbookNodeLogins = "node-logins"
	RunbookDisk       = "disk"
	RunbookDNS        = "dns-and-proxy"
	RunbookNoAnswer   = "agent-does-not-answer"
	RunbookPauses     = "pauses"
	RunbookTokenUsage = "token-usage"
)

// Runbooks lists every runbook in the order of the skill's index.
var Runbooks = []string{RunbookFirstSetup, RunbookPaperclip, RunbookAddMachine, RunbookNodeLogins, RunbookDisk, RunbookDNS, RunbookNoAnswer, RunbookPauses, RunbookTokenUsage}

// RunbookRef is how an answer names a runbook: "factory-setup runbook dns-and-proxy".
func RunbookRef(name string) string {
	if name == "" {
		return ""
	}
	return Skill + " runbook " + name
}

// Answer is a cause in words, its fix and the runbook for it.
type Answer struct {
	Cause   string `json:"cause"`
	Fix     string `json:"fix,omitempty"`
	Runbook string `json:"runbook,omitempty"`
}

// Waiting explains a task's verdict (GET /tasks/{id}/why) and, when its run is queued, the queued reason.
func Waiting(v tasks.Verdict, queued *QueuedWhy) Answer {
	a := Answer{Cause: verdictCause(v), Fix: v.Clearer.Action, Runbook: reasonRunbook(v.Reason, v.Detail)}
	if queued != nil && queued.Reason != "" {
		q := Queued(*queued)
		a.Cause += "; its run waits: " + q.Cause
		if q.Fix != "" {
			a.Fix = q.Fix
		}
		if q.Runbook != "" {
			a.Runbook = q.Runbook
		}
	}
	return a
}

// QueuedWhy is a queued run's reason as Core reports it (storage.QueuedReason without the per-node list).
type QueuedWhy struct {
	Reason   string `json:"reason"`
	Detail   string `json:"detail"`
	Holder   string `json:"holder,omitempty"`
	Inactive string `json:"inactive,omitempty"`
}

// Queued explains why a queued run is not claimed.
func Queued(q QueuedWhy) Answer {
	a := Answer{Cause: strings.TrimSpace(queuedLabels[q.Reason] + ": " + q.Detail)}
	if queuedLabels[q.Reason] == "" {
		a.Cause = q.Reason + ": " + q.Detail
	}
	switch q.Reason {
	case "paused":
		a.Fix, a.Runbook = "resume the paused scope (factory pauses lists them; factory resume --project P)", RunbookPauses
	case "node_paused":
		a.Fix, a.Runbook = "resume the node (factory resume --node N) or release its sessions so another node takes the run", RunbookPauses
	case "affinity":
		a.Fix, a.Runbook = "the run waits for the node holding the agent's session; resume that node, or release the session (Nodes page: Release now) so another node starts a fresh one", RunbookNoAnswer
		if q.Inactive != "" {
			a.Cause += " (holder " + q.Holder + " is " + q.Inactive + ")"
		}
	case "pinned":
		a.Fix, a.Runbook = "the task's unsaved work is on that node: bring the node back, or reset the task's workspace", RunbookNoAnswer
	case "node_disk_critical":
		a.Fix, a.Runbook = "free disk on the node; it resumes by itself above its critical watermark", RunbookDisk
	case "driver_disabled":
		a.Fix, a.Runbook = "fix the driver's failing check on the node (factory-node doctor)", cmpOr(driverRunbook(q.Detail), RunbookNodeLogins)
	case "node_offline", "no_node":
		a.Fix, a.Runbook = "start the node (factory-node run, or its launchd/systemd service) or add one", RunbookAddMachine
	case "not_ready", "no_ready_node":
		a.Fix, a.Runbook = "fix the node's readiness fault for the project (the detail names it), then Check again", cmpOr(driverRunbook(q.Detail), RunbookAddMachine)
	case "labels":
		a.Fix, a.Runbook = "give a node the agent's labels (node.yaml labels) or change the agent's labels", RunbookAddMachine
	case "agent_busy", "no_slot", "resource_busy":
		a.Fix = "nothing to fix: it starts when the current run ends"
	case "unclaimed":
		a.Fix = "nothing to fix: a node can take it and its claim is on its way"
	}
	return a
}

var queuedLabels = map[string]string{
	"paused": "paused", "agent_busy": "agent busy", "pinned": "tied to the node with its unsaved work", "affinity": "tied to the node with the agent's session",
	"no_slot": "no free slot", "resource_busy": "resource busy", "node_paused": "node paused", "node_disk_critical": "node disk critical",
	"driver_disabled": "driver disabled", "node_offline": "node offline", "labels": "no node with the labels", "not_ready": "node not ready",
	"no_ready_node": "no ready node", "no_node": "no node", "unclaimed": "claim on its way",
}

func verdictCause(v tasks.Verdict) string {
	r := v.Reason
	label := reasonLabels[r]
	switch {
	case strings.HasPrefix(r, tasks.ReasonPaused):
		label = "paused (" + strings.TrimPrefix(r, tasks.ReasonPaused) + ")"
	case strings.HasPrefix(r, tasks.ReasonParked):
		label = "parked (" + strings.TrimPrefix(r, tasks.ReasonParked) + ")"
	case label == "":
		label = r
	}
	if v.Detail != "" {
		return label + ": " + v.Detail
	}
	return label
}

var reasonLabels = map[string]string{
	tasks.ReasonTerminal: "finished", tasks.ReasonBacklog: "in the backlog", tasks.ReasonUnassigned: "not assigned",
	tasks.ReasonLiveRun: "running now", tasks.ReasonDependencies: "waits for its dependencies", tasks.ReasonArtifacts: "waits for artifacts",
	tasks.ReasonBackoff: "waits for its automatic retry", tasks.ReasonSecretMissing: "a secret is missing", tasks.ReasonIdle: "idle",
	tasks.ReasonPinnedNode: "its node is unavailable", tasks.ReasonNoCapableNode: "no node can run it",
	tasks.ReasonNodeDiskCritical: "the node's disk is critical", tasks.ReasonNoReadyNode: "no node is ready for its project",
	tasks.ReasonCapacity: "waits for capacity", tasks.ReasonProjectSetup: "waits for the project setup", tasks.ReasonRunnable: "runnable now",
}

func reasonRunbook(reason, detail string) string {
	switch {
	case strings.HasPrefix(reason, tasks.ReasonPaused):
		return RunbookPauses
	case reason == tasks.ReasonNodeDiskCritical:
		return RunbookDisk
	case reason == tasks.ReasonNoCapableNode:
		if strings.Contains(detail, "paused") {
			return RunbookPauses
		}
		return cmpOr(driverRunbook(detail), RunbookAddMachine)
	case reason == tasks.ReasonNoReadyNode, reason == tasks.ReasonPinnedNode:
		return cmpOr(driverRunbook(detail), RunbookAddMachine)
	case reason == tasks.ReasonBackoff:
		return RunbookNoAnswer
	case reason == tasks.ReasonSecretMissing:
		return RunbookFirstSetup
	}
	return ""
}

// driverRunbook picks the runbook from a node's failing check: network checks and errors go to dns-and-proxy,
// logins to node-logins, disk to disk.
func driverRunbook(detail string) string {
	d := strings.ToLower(detail)
	switch {
	case strings.Contains(d, "net.dns") || strings.Contains(d, "net.api") || strings.Contains(d, "enotfound") || strings.Contains(d, "resolve"):
		return RunbookDNS
	case strings.Contains(d, "disk"):
		return RunbookDisk
	case strings.Contains(d, "auth") || strings.Contains(d, "login") || strings.Contains(d, "account"):
		return RunbookNodeLogins
	case strings.Contains(d, "paused"):
		return RunbookPauses
	case strings.Contains(d, "offline") || strings.Contains(d, "repo_access") || strings.Contains(d, "sandbox") || strings.Contains(d, "runtime"):
		return RunbookAddMachine
	}
	return ""
}

// NodeDriver is a node's driver as Core reports it (storage.NodeDriver).
type NodeDriver struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Check  string `json:"check,omitempty"`
	Detail string `json:"detail,omitempty"`
	Until  int64  `json:"until,omitempty"`
}

// Driver explains a disabled driver ("" Cause when it is usable).
func Driver(node string, d NodeDriver) Answer {
	if d.OK {
		return Answer{}
	}
	a := Answer{Cause: fmt.Sprintf("%s on %s is disabled: %s %s", d.Name, node, d.Check, d.Detail), Runbook: cmpOr(driverRunbook(d.Check+" "+d.Detail), RunbookNodeLogins)}
	switch a.Runbook {
	case RunbookDNS:
		a.Fix = "fix the node's DNS, VPN or proxy so it resolves and reaches the API host; factory-node doctor must pass net.dns and net.api"
	case RunbookNodeLogins:
		a.Fix = "log the node in again: factory-node login " + d.Name + "; then factory-node doctor"
	default:
		a.Fix = "run factory-node doctor on " + node + " and fix the failing " + d.Name + " check"
	}
	return a
}

// Outcome explains how a run ended: outcome, node fault and its result text.
func Outcome(outcome, nodeFault, result string) Answer {
	short := excerpt(result, 300)
	switch {
	case nodeFault == protocol.FaultNetwork:
		return Answer{Cause: "the node could not reach the runtime's API host (" + short + "); not counted against the task", Fix: "fix the node's DNS, VPN or proxy", Runbook: RunbookDNS}
	case nodeFault != "":
		return Answer{Cause: "the node's environment failed the run (" + nodeFault + ": " + short + "); not counted against the task", Fix: "fix the node (factory-node doctor), then Check again on the Nodes page", Runbook: cmpOr(driverRunbook(nodeFault+" "+result), RunbookAddMachine)}
	}
	switch outcome {
	case "succeeded", "":
		return Answer{Cause: outcome}
	case "paused_out":
		return Answer{Cause: "the runtime's login failed or a subscription limit was reached: " + short, Fix: "log the node in again (factory-node login claude|codex) or wait for the limit", Runbook: RunbookNodeLogins}
	case "no_exit":
		return Answer{Cause: "the agent ended its turn without a factory_exit_* call: " + short, Fix: "nothing on the node; the task retries or escalates", Runbook: RunbookNoAnswer}
	case "idle_timeout", "timed_out":
		return Answer{Cause: "the run was stopped (" + outcome + "): " + short, Fix: "look at the run's log (factory runs events ID); a hung tool or a too small timeout", Runbook: RunbookNoAnswer}
	case "lost":
		return Answer{Cause: "the node stopped reporting the run (restart, sleep or crash): " + short, Fix: "check the node log (factory-node logs) and that the node stays awake", Runbook: RunbookAddMachine}
	case "session_lost":
		return Answer{Cause: "the agent's session is gone from its node: " + short, Fix: "answer the owner question: new session, retry or cancel", Runbook: RunbookNoAnswer}
	}
	return Answer{Cause: outcome + ": " + short, Runbook: driverRunbook(result)}
}

// Usage explains a run's token usage in words. Cache reads are the session's context read again on every turn: they
// make most of a long session's tokens and cost about a tenth of new input.
func Usage(us []protocol.Usage) string {
	if len(us) == 0 {
		return "no token usage reported"
	}
	var in, out, cr, cw int64
	var cost float64
	models := []string{}
	for _, u := range us {
		in, out, cr, cw, cost = in+u.Input, out+u.Output, cr+u.CacheRead, cw+u.CacheWrite, cost+u.CostUSD
		if u.Model != "" {
			models = append(models, u.Model)
		}
	}
	sort.Strings(models)
	total := in + out + cr + cw
	s := fmt.Sprintf("%s tokens", count(total))
	parts := []string{}
	if cr > 0 {
		parts = append(parts, fmt.Sprintf("%s cache reads (%d%%: the session's context read again each turn, billed at about a tenth of new input)", count(cr), pct(cr, total)))
	}
	if cw > 0 {
		parts = append(parts, count(cw)+" cache writes (new context stored for later turns)")
	}
	parts = append(parts, count(in)+" new input", count(out)+" output")
	s += ": " + strings.Join(parts, ", ")
	if len(models) > 0 {
		s += "; model " + strings.Join(models, ", ")
	}
	if cost > 0 {
		s += fmt.Sprintf("; $%.2f as the runtime reports it", cost)
	}
	return s
}

func pct(a, b int64) int64 {
	if b == 0 {
		return 0
	}
	return a * 100 / b
}

// count formats a token count: 352140 → "352k", 1234567 → "1.2M", 950 → "950".
func count(n int64) string {
	switch {
	case n >= 1_000_000:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(n)/1e6), ".0") + "M"
	case n >= 1000:
		return fmt.Sprintf("%dk", (n+500)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
