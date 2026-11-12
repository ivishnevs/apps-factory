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
	// OwnerPaused: the owner paused the node (PauseReason is the owner's reason); it takes no run.
	OwnerPaused bool
	PauseReason string
	// Disk: Core paused the node for a critical disk (nil when it is not): it takes no work or review run, and no
	// conversation either below its conversation floor (disk-pause-conversations).
	Disk      *NodeDisk
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

// NodeDisk is a node's critical disk: free space and the critical watermark in bytes; Full when it is below the
// conversation floor too (Floor, 0 when the node does not report one).
type NodeDisk struct {
	Free, Critical, Floor uint64
	Full                  bool
}

// GiB formats bytes as "3.6 GiB" ("5 GiB" for a whole number).
func GiB(b uint64) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(b)/(1<<30)), ".0") + " GiB"
}

// What says the disk pause in words: "disk critical, 3.6 GiB free (critical 5 GiB)".
func (d *NodeDisk) What() string {
	if d == nil {
		return ""
	}
	if d.Critical == 0 {
		return "disk below its critical watermark"
	}
	s := "disk critical, " + GiB(d.Free) + " free (critical " + GiB(d.Critical) + ")"
	if d.Full && d.Floor > 0 {
		s += ", below the conversation floor " + GiB(d.Floor)
	}
	return s
}

// Resumes says when the node claims again by itself: "above 5 GiB" (its critical watermark), or above its
// conversation floor for a conversation on a full disk.
func (d *NodeDisk) Resumes(conversation bool) string {
	switch {
	case conversation && d.Full && d.Floor > 0:
		return "above " + GiB(d.Floor)
	case d.Critical > 0:
		return "above " + GiB(d.Critical)
	}
	return "above its critical watermark"
}

// PausedFor tells whether the node takes no run of the kind now: the owner paused it, or its disk is critical (for
// a conversation only below the conversation floor).
func (n Node) PausedFor(conversation bool) bool {
	return n.OwnerPaused || n.Disk != nil && (!conversation || n.Disk.Full)
}

// pausedWhy is the pause in words: "paused by the owner: REASON" or "paused: disk critical, ...".
func (n Node) pausedWhy() string {
	if n.OwnerPaused {
		if n.PauseReason != "" {
			return "paused by the owner: " + n.PauseReason
		}
		return "paused by the owner"
	}
	return "paused: " + n.Disk.What()
}

// short is the node's name, else its ID.
func (n Node) short() string {
	if n.Name != "" {
		return n.Name
	}
	return n.ID
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

// tie is the task's tie with its node's name (node-ties): the loaded one, or a work tie from PinnedNode.
func (s Snapshot) tie() *Tie {
	var t Tie
	switch {
	case s.Tie != nil:
		t = *s.Tie
	case s.PinnedNode != "":
		t = Tie{Node: s.PinnedNode, Reason: TieWork, Release: WorkRelease}
	default:
		return nil
	}
	if n := s.node(t.Node); n != nil {
		t.NodeName = n.Name
	}
	return &t
}

// workSay is "tied to <node>: its unsaved work is there" (with why it could not be saved).
func (s Snapshot) workSay() string {
	if t := s.tie(); t != nil && t.Reason == TieWork {
		return t.Say()
	}
	return (&Tie{Node: s.PinnedNode, Reason: TieWork}).Say()
}

// PinnedUnavailable says why the node the task is tied to by its work cannot continue it ("" when it can or the
// task has no work tie): the node was removed, is offline beyond the pin timeout, or cannot run the current
// assignee. Without needs (Provider empty) only removal and the offline timeout count.
func PinnedUnavailable(s Snapshot) string {
	if s.PinnedNode == "" {
		return ""
	}
	n := s.node(s.PinnedNode)
	if n == nil {
		return s.workSay() + "; node " + s.PinnedNode + " is no longer registered"
	}
	if s.Needs.Provider != "" {
		if why := n.Incapable(s.Needs); why != "" {
			return fmt.Sprintf("%s; the node cannot run %s: %s", s.workSay(), s.Task.Assignee, why)
		}
	}
	if !n.Online && s.PinTimeout > 0 && !s.Now.IsZero() && s.Now.Sub(time.Unix(n.LastSeen, 0)) > s.PinTimeout {
		return fmt.Sprintf("%s; the node is offline since %s (more than %s)", s.workSay(), time.Unix(n.LastSeen, 0).UTC().Format(time.RFC3339), s.PinTimeout)
	}
	return ""
}

// noCapableNode says why no online, unpaused node can take the task's runs, and how the owner clears it (reason ""
// when one can): node_disk_critical when a capable node is paused for its disk (it resumes by itself once space is
// freed), no_capable_node naming the owner's pause when one paused a capable node, no_capable_node with "start or
// fix a node" when capable nodes are offline or no node can run the assignee, no_ready_node when capable nodes exist
// but none is ready for the task's project (disk-pause-conversations 2.1).
func (s Snapshot) noCapableNode() (reason, why, clear string) {
	conversation := s.Task.Kind == KindConversation
	var incapable, down, unready []string
	var disk, owner []Node
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
		case n.OwnerPaused:
			owner = append(owner, n)
			down = append(down, n.label()+" "+n.pausedWhy())
		case n.PausedFor(conversation):
			disk = append(disk, n)
		default:
			return "", "", ""
		}
	}
	names := func(ns []Node) string {
		out := make([]string, len(ns))
		for i, n := range ns {
			out[i] = n.short()
		}
		return strings.Join(out, " or ")
	}
	if len(disk) > 0 {
		parts := make([]string, 0, len(disk)+len(down))
		for _, n := range disk {
			parts = append(parts, n.short()+": "+n.Disk.What())
		}
		clear = "free disk on " + names(disk) + "; it resumes by itself " + disk[0].Disk.Resumes(conversation)
		if len(disk) > 1 {
			clear = "free disk on " + names(disk) + "; each resumes by itself above its critical watermark"
		}
		return ReasonNodeDiskCritical, strings.Join(append(parts, down...), "; "), clear
	}
	if len(owner) > 0 {
		clear = "resume node " + names(owner)
		if len(owner) == 1 {
			clear += " (" + owner[0].pausedWhy() + ")"
		} else {
			clear += " (paused by the owner)"
		}
		return ReasonNoCapableNode, "capable nodes are unavailable: " + strings.Join(down, "; "), clear
	}
	start := "start or fix a node with " + s.Needs.describe() + " (node.yaml, factory-node doctor)"
	if len(down) > 0 {
		return ReasonNoCapableNode, "capable nodes are unavailable: " + strings.Join(down, "; "), start
	}
	if len(unready) > 0 {
		return ReasonNoReadyNode, "no node is ready for project " + s.Task.ProjectID + " — " + strings.Join(unready, "; "), NoReadyClearer
	}
	return ReasonNoCapableNode, "no registered node can run " + s.Task.Assignee + " (" + s.Needs.describe() + "): " + strings.Join(incapable, "; "), start
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
		if reason, why, clear := s.noCapableNode(); reason != "" {
			return hold(reason, why, "owner", clear)
		}
	} else if n := s.node(s.PinnedNode); n != nil && n.NotReady[s.Task.ProjectID] != "" {
		// Only the pinned node can continue the task (task-worktrees D8): it waits for that node's fix.
		return hold(ReasonNoReadyNode, fmt.Sprintf("the task's unpushed work is on node %s, which is not ready for project %s: %s", n.label(), s.Task.ProjectID, n.NotReady[s.Task.ProjectID]), "owner", NoReadyClearer)
	}
	if !capacity {
		return Verdict{}
	}
	// The pinned node offline or paused for its disk: the task waits for it (an owner pause of the pinned node leaves
	// the run queued, its claim refused, as before; the queued reason names the pause).
	conversation := s.Task.Kind == KindConversation
	if n := s.node(s.PinnedNode); n != nil && (!n.Online || n.Disk != nil && (!conversation || n.Disk.Full)) {
		state := "paused: " + n.Disk.What()
		action := fmt.Sprintf("wait for the node (after %s offline reset_workspace is offered)", s.PinTimeout)
		if !n.Online {
			state = "offline since " + time.Unix(n.LastSeen, 0).UTC().Format(time.RFC3339)
		} else {
			action = "free disk on " + n.short() + "; it resumes by itself " + n.Disk.Resumes(conversation)
		}
		return wait(hold(ReasonCapacity, fmt.Sprintf("%s; the node is %s", s.workSay(), state), "node:"+n.ID, action),
			Wait{Kind: WaitPinnedNode, Node: n.label()})
	}
	if why := s.nodesFull(); why != "" && s.PinnedNode != "" {
		// Online but full (sticky-work-first 2.1): the task waits for that node, not for "all nodes"; it is offered
		// first there when a slot frees.
		label := s.PinnedNode
		if n := s.node(s.PinnedNode); n != nil {
			label = n.label()
		}
		return wait(hold(ReasonCapacity, s.workSay()+"; the node is full ("+why+")", "node:"+s.PinnedNode,
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
		if s.PinnedNode != "" && n.ID != s.PinnedNode || n.Incapable(s.Needs) != "" || n.NotReady[s.Task.ProjectID] != "" || !n.Online || n.PausedFor(s.Task.Kind == KindConversation) {
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
