package tasks

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// Node reasons of Evaluate (node-agent D3, task-worktrees D8), judged over the company's registered
// (runtime protocol v2) nodes.

// Node is a registered node as Evaluate sees it.
type Node struct {
	ID       string
	Name     string
	Online   bool
	LastSeen int64
	// Paused: the node does not claim (health.disk critical).
	Paused    bool
	Drivers   []NodeDriver
	Labels    []string
	Resources map[string]int
	Slots     int
	// Held counts the runs holding a work slot (from claim until ended and sealed); Used their resource classes.
	Held int
	Used map[string]int
	// NotReady maps a project to why the node is not ready for it (node-readiness: repository access, a missing
	// tool, a run that failed there before its agent started, with the fix); the node takes none of its runs.
	NotReady map[string]string
	// ConversationSlots take only conversation runs, on top of Slots; ConversationHeld counts the runs in them
	// (work-and-conversation-runs).
	ConversationSlots, ConversationHeld int
}

type NodeDriver struct {
	Name string
	OK   bool
}

// NodeNeeds is what the assignee's runs need from a node: its runtime provider, spec.labels and
// spec.resources. Core fills it from the agent catalog.
type NodeNeeds struct {
	Provider  string
	Labels    []string
	Resources map[string]int
}

func (n Node) label() string {
	if n.Name == "" || n.Name == n.ID {
		return n.ID
	}
	return n.Name + " (" + n.ID + ")"
}

// Incapable says why the node can never run a run with these needs ("" when it can): no usable driver for
// the provider, missing labels, or a resource class it does not have enough of.
func (n Node) Incapable(need NodeNeeds) string {
	if !slices.ContainsFunc(n.Drivers, func(d NodeDriver) bool { return d.Name == need.Provider && d.OK }) {
		return "no usable " + need.Provider + " driver"
	}
	if missing := MissingLabels(n.Labels, need.Labels); len(missing) > 0 {
		return "lacks labels " + strings.Join(missing, ", ")
	}
	for _, k := range slices.Sorted(maps.Keys(need.Resources)) {
		if n.Resources[k] < need.Resources[k] {
			return fmt.Sprintf("has %d %s, the run needs %d", n.Resources[k], k, need.Resources[k])
		}
	}
	return ""
}

// Short says what the node lacks right now for one more run of the kind ("" when it fits): a conversation run fits
// a free conversation slot, then a free work slot (work-and-conversation-runs).
func (n Node) Short(conversation bool, need map[string]int) string {
	if conversation && n.ConversationHeld < n.ConversationSlots {
		return ""
	}
	short := NodeShort(n.Held, n.Slots, n.Used, n.Resources, need)
	if short != "" && conversation && n.ConversationSlots > 0 {
		short = fmt.Sprintf("all %d conversation slots held; %s", n.ConversationSlots, short)
	}
	return short
}

// NodeShort says what a node with slots and capacity, whose held runs use used, lacks for one more run
// needing need ("" when it fits). A class the node does not declare has capacity 0.
func NodeShort(held, slots int, used, capacity, need map[string]int) string {
	if held >= slots {
		return fmt.Sprintf("all %d slots held", slots)
	}
	for _, k := range slices.Sorted(maps.Keys(need)) {
		if used[k]+need[k] > capacity[k] {
			return fmt.Sprintf("resource %s: %d of %d held, the run needs %d", k, used[k], capacity[k], need[k])
		}
	}
	return ""
}

// MissingLabels lists the labels of need that have lacks.
func MissingLabels(have, need []string) []string {
	var missing []string
	for _, l := range need {
		if !slices.Contains(have, l) {
			missing = append(missing, l)
		}
	}
	return missing
}

// nodeAware: the company has registered nodes and Core filled the assignee's needs.
func (s Snapshot) nodeAware() bool { return len(s.Nodes) > 0 && s.Needs.Provider != "" }

func (s Snapshot) node(id string) *Node {
	for i := range s.Nodes {
		if s.Nodes[i].ID == id {
			return &s.Nodes[i]
		}
	}
	return nil
}

// PinnedUnavailable says why the node holding the task's unpushed work cannot continue it ("" when it can
// or the task is not pinned): the node was removed, is offline beyond the pin timeout, or cannot run the
// current assignee. Without needs (Provider empty) only removal and the offline timeout count.
func PinnedUnavailable(s Snapshot) string {
	if s.PinnedNode == "" {
		return ""
	}
	n := s.node(s.PinnedNode)
	if n == nil {
		return "the task's unpushed work is on node " + s.PinnedNode + ", which is no longer registered"
	}
	if s.Needs.Provider != "" {
		if why := n.Incapable(s.Needs); why != "" {
			return fmt.Sprintf("the task's unpushed work is on node %s, which cannot run %s: %s", n.label(), s.Task.Assignee, why)
		}
	}
	if !n.Online && s.PinTimeout > 0 && !s.Now.IsZero() && s.Now.Sub(time.Unix(n.LastSeen, 0)) > s.PinTimeout {
		return fmt.Sprintf("the task's unpushed work is on node %s, offline since %s (more than %s)", n.label(), time.Unix(n.LastSeen, 0).UTC().Format(time.RFC3339), s.PinTimeout)
	}
	return ""
}

// noCapableNode says why no online, unpaused node can take the task's runs (reason "" when one can):
// no_capable_node, or no_ready_node when capable nodes exist but none is ready for the task's project.
func (s Snapshot) noCapableNode() (reason, why string) {
	var incapable, down, unready []string
	for _, n := range s.Nodes {
		if w := n.Incapable(s.Needs); w != "" {
			incapable = append(incapable, n.label()+": "+w)
			continue
		}
		if w := n.NotReady[s.Task.ProjectID]; w != "" {
			unready = append(unready, n.label()+": "+w)
			continue
		}
		switch {
		case !n.Online:
			down = append(down, n.label()+" offline")
		case n.Paused:
			down = append(down, n.label()+" paused")
		default:
			return "", ""
		}
	}
	if len(down) > 0 {
		return ReasonNoCapableNode, "capable nodes are unavailable: " + strings.Join(down, "; ")
	}
	if len(unready) > 0 {
		return ReasonNoReadyNode, "no node is ready for project " + s.Task.ProjectID + " — " + strings.Join(unready, "; ")
	}
	return ReasonNoCapableNode, "no registered node can run " + s.Task.Assignee + " (" + s.Needs.describe() + "): " + strings.Join(incapable, "; ")
}

// NoReadyClearer is how the owner clears no_ready_node: the node's fix, then its check passes.
const NoReadyClearer = "fix the node as its fault says (Nodes page), then Check again; the task runs on the first ready node, its retries untouched"

// nodeReason is the node reason of a task with registered nodes (empty Reason when none applies):
// pinned_node_unavailable, then no_capable_node, then, with capacity, node capacity (the pinned node offline or
// paused for less than the pin timeout, or every capable node full).
func (s Snapshot) nodeReason(capacity bool) Verdict {
	hold := func(reason, detail, who, action string) Verdict {
		return Verdict{Reason: reason, Detail: detail, Clearer: Clearer{Who: who, Action: action}}
	}
	if !s.nodeAware() {
		return Verdict{}
	}
	if why := PinnedUnavailable(s); why != "" {
		return hold(ReasonPinnedNode, why, "owner", "answer the pinned_node_unavailable escalation with reset_workspace (continues from the remote branch on any node), or bring the node back")
	}
	if s.PinnedNode == "" {
		if reason, why := s.noCapableNode(); reason == ReasonNoReadyNode {
			return hold(reason, why, "owner", NoReadyClearer)
		} else if reason != "" {
			return hold(reason, why, "owner", "start or fix a node with "+s.Needs.describe()+" (node.yaml, factory-node doctor)")
		}
	} else if n := s.node(s.PinnedNode); n != nil && n.NotReady[s.Task.ProjectID] != "" {
		// Only the pinned node can continue the task (task-worktrees D8): it waits for that node's fix.
		return hold(ReasonNoReadyNode, fmt.Sprintf("the task's unpushed work is on node %s, which is not ready for project %s: %s", n.label(), s.Task.ProjectID, n.NotReady[s.Task.ProjectID]), "owner", NoReadyClearer)
	}
	if !capacity {
		return Verdict{}
	}
	if n := s.node(s.PinnedNode); n != nil && (!n.Online || n.Paused) {
		state := "paused"
		if !n.Online {
			state = "offline since " + time.Unix(n.LastSeen, 0).UTC().Format(time.RFC3339)
		}
		return wait(hold(ReasonCapacity, fmt.Sprintf("the task's unpushed work is on node %s, %s", n.label(), state), "node:"+n.ID, fmt.Sprintf("wait for the node (after %s offline reset_workspace is offered)", s.PinTimeout)),
			Wait{Kind: WaitPinnedNode, Node: n.label()})
	}
	if why := s.nodesFull(); why != "" && s.PinnedNode != "" {
		// Online but full (sticky-work-first 2.1): the task waits for that node, not for "all nodes"; it is offered
		// first there when a slot frees.
		label := s.PinnedNode
		if n := s.node(s.PinnedNode); n != nil {
			label = n.label()
		}
		return wait(hold(ReasonCapacity, "the task's unpushed work is on node "+label+", which is full ("+why+")", "node:"+s.PinnedNode,
			"wait for a free slot on that node; it takes this task first"), Wait{Kind: WaitPinnedNode, Node: label})
	} else if why != "" {
		return wait(hold(ReasonCapacity, "capable nodes are full: "+why, "scheduler", "wait for a run on a capable node to end and be sealed"), Wait{Kind: WaitNodes})
	}
	return Verdict{}
}

// nodesFull says why every online capable node (or the pinned node) has no room now ("" when one has).
func (s Snapshot) nodesFull() string {
	var full []string
	for _, n := range s.Nodes {
		if s.PinnedNode != "" && n.ID != s.PinnedNode || n.Incapable(s.Needs) != "" || n.NotReady[s.Task.ProjectID] != "" || !n.Online || n.Paused {
			continue
		}
		short := n.Short(s.Task.Kind == KindConversation, s.Needs.Resources)
		if short == "" {
			return ""
		}
		full = append(full, n.label()+": "+short)
	}
	return strings.Join(full, "; ")
}

func (n NodeNeeds) describe() string {
	parts := []string{"driver " + n.Provider}
	if len(n.Labels) > 0 {
		parts = append(parts, "labels "+strings.Join(n.Labels, ", "))
	}
	for _, k := range slices.Sorted(maps.Keys(n.Resources)) {
		parts = append(parts, fmt.Sprintf("%s: %d", k, n.Resources[k]))
	}
	return strings.Join(parts, "; ")
}
