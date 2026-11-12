package tasks

import (
	"strings"
	"testing"
	"time"
)

// TestEvaluateNodeReasons: with registered nodes, a runnable task reports pinned_node_unavailable (its pinned
// node removed, offline past the pin timeout, or unable to run the assignee), no_capable_node (no online,
// unpaused node has the driver, labels and resources) or capacity (capable nodes are full), in that order.
func TestEvaluateNodeReasons(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	mac := func() Node {
		return Node{ID: "n1", Name: "mini-1", Online: true, LastSeen: now.Unix(), Slots: 2, Drivers: []NodeDriver{{Name: "codex", OK: true}},
			Labels: []string{"macos", "xcode"}, Resources: map[string]int{"simulator": 1}, Used: map[string]int{}}
	}
	linux := Node{ID: "n2", Name: "box", Online: true, LastSeen: now.Unix(), Slots: 4, Drivers: []NodeDriver{{Name: "codex", OK: true}, {Name: "claude", OK: true}},
		Labels: []string{"linux"}, Used: map[string]int{}}
	base := func() Snapshot {
		s := ready()
		s.Now, s.PinTimeout = now, 30*time.Minute
		s.Nodes = []Node{mac(), linux}
		s.Needs = NodeNeeds{Provider: "codex", Labels: []string{"macos"}, Resources: map[string]int{"simulator": 1}}
		return s
	}
	cases := []struct {
		name   string
		change func(*Snapshot)
		reason string
		detail string
	}{
		{"capable node free", func(*Snapshot) {}, ReasonRunnable, ""},
		{"no registered nodes", func(s *Snapshot) { s.Nodes = nil }, ReasonRunnable, ""},
		{"needs unknown", func(s *Snapshot) { s.Needs = NodeNeeds{} }, ReasonRunnable, ""},
		{"no node has the labels", func(s *Snapshot) { s.Needs.Labels = []string{"windows"} }, ReasonNoCapableNode, "mini-1 (n1): lacks labels windows"},
		{"driver disabled", func(s *Snapshot) { s.Nodes[0].Drivers[0].OK = false }, ReasonNoCapableNode, "mini-1 (n1): no usable codex driver"},
		{"no driver", func(s *Snapshot) { s.Needs.Provider = "claude" }, ReasonNoCapableNode, "no usable claude driver"},
		{"too few of a class", func(s *Snapshot) { s.Needs.Resources["simulator"] = 2 }, ReasonNoCapableNode, "has 1 simulator, the run needs 2"},
		{"capable node offline", func(s *Snapshot) { s.Nodes[0].Online = false }, ReasonNoCapableNode, "capable nodes are unavailable: mini-1 (n1) offline"},
		{"capable node paused by the owner", func(s *Snapshot) { s.Nodes[0].OwnerPaused, s.Nodes[0].PauseReason = true, "maintenance" }, ReasonNoCapableNode, "mini-1 (n1) paused by the owner: maintenance"},
		{"capable node disk critical", func(s *Snapshot) { s.Nodes[0].Disk = &NodeDisk{Free: 36 << 30 / 10, Critical: 5 << 30, Floor: 1 << 30} }, ReasonNodeDiskCritical, "mini-1: disk critical, 3.6 GiB free (critical 5 GiB)"},
		{"class held", func(s *Snapshot) { s.Nodes[0].Held, s.Nodes[0].Used["simulator"] = 1, 1 }, ReasonCapacity, "resource simulator: 1 of 1 held"},
		{"slots held", func(s *Snapshot) { s.Nodes[0].Held = 2 }, ReasonCapacity, "all 2 slots held"},
		{"agent busy first", func(s *Snapshot) {
			s.Nodes[0].Held = 2
			s.AgentRun, s.AgentLive = &Run{ID: "r9", Status: "RUNNING", TaskID: "t9"}, 1
		}, ReasonCapacity, "agent dev is busy"},
		{"queued run nobody can claim", func(s *Snapshot) {
			s.Needs.Labels, s.CurrentRun = []string{"windows"}, &Run{ID: "r1", Status: "QUEUED"}
		}, ReasonNoCapableNode, "(run r1 is QUEUED)"},
		{"queued run on full nodes", func(s *Snapshot) { s.Nodes[0].Held, s.CurrentRun = 2, &Run{ID: "r1", Status: "QUEUED"} }, ReasonCapacity, "all 2 slots held"},
		{"queued run, a node is free", func(s *Snapshot) { s.CurrentRun = &Run{ID: "r1", Status: "QUEUED"} }, ReasonLiveRun, "QUEUED"},
		{"running run", func(s *Snapshot) {
			s.Needs.Labels, s.CurrentRun = []string{"windows"}, &Run{ID: "r1", Status: "RUNNING"}
		}, ReasonLiveRun, "RUNNING"},
		{"paused before nodes", func(s *Snapshot) { s.Needs.Labels = []string{"windows"}; s.Paused = []string{"company"} }, ReasonPaused + "company", ""},
		{"pinned node free", func(s *Snapshot) { s.PinnedNode = "n1" }, ReasonRunnable, ""},
		{"pinned node full", func(s *Snapshot) { s.PinnedNode = "n1"; s.Nodes[0].Held = 2 }, ReasonCapacity, "tied to mini-1 (n1): its unsaved work is there; the node is full (mini-1 (n1): all 2 slots held)"},
		{"pinned, another node full does not matter", func(s *Snapshot) { s.PinnedNode = "n1"; s.Nodes[1].Held = 4 }, ReasonRunnable, ""},
		{"pinned node offline briefly", func(s *Snapshot) {
			s.PinnedNode, s.Nodes[0].Online, s.Nodes[0].LastSeen = "n1", false, now.Add(-10*time.Minute).Unix()
		}, ReasonCapacity, "tied to mini-1 (n1): its unsaved work is there; the node is offline since"},
		{"pinned node offline past the timeout", func(s *Snapshot) {
			s.PinnedNode, s.Nodes[0].Online, s.Nodes[0].LastSeen = "n1", false, now.Add(-31*time.Minute).Unix()
		}, ReasonPinnedNode, "offline since"},
		{"pinned node removed", func(s *Snapshot) { s.PinnedNode = "gone" }, ReasonPinnedNode, "tied to gone: its unsaved work is there; node gone is no longer registered"},
		{"pinned node cannot run the assignee", func(s *Snapshot) { s.PinnedNode = "n2" }, ReasonPinnedNode, "tied to box (n2): its unsaved work is there; the node cannot run dev: lacks labels macos"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := base()
			c.change(&s)
			v := Evaluate(s)
			if v.Reason != c.reason || !strings.Contains(v.Detail, c.detail) {
				t.Fatalf("%s: %q (%s) — %+v", c.reason, v.Reason, v.Detail, v.Clearer)
			}
			if v.Reason == ReasonPinnedNode && !strings.Contains(v.Clearer.Action, "reset_workspace") || v.Reason == ReasonNoCapableNode && v.Clearer.Who != "owner" {
				t.Fatalf("%+v", v.Clearer)
			}
		})
	}
	// sticky-work-first 2.1: a pinned task on a full online node waits for that node, not for all nodes.
	s := base()
	s.PinnedNode, s.Nodes[0].Held = "n1", 2
	if v := Evaluate(s); v.Clearer.Who != "node:n1" || !strings.Contains(v.Clearer.Action, "takes this task first") {
		t.Fatalf("pinned to a full node: %+v", v.Clearer)
	}
	if w := s.nodeReason(true).Wait; w == nil || w.Kind != WaitPinnedNode || w.Node != "mini-1 (n1)" {
		t.Fatalf("pinned to a full node: wait %+v", w)
	}
	// disk-pause-conversations 2.1: a pinned node paused for its disk names the fix.
	s = base()
	s.PinnedNode, s.Nodes[0].Disk = "n1", &NodeDisk{Free: 4 << 30, Critical: 5 << 30, Floor: 1 << 30}
	if v := Evaluate(s); v.Reason != ReasonCapacity || !strings.Contains(v.Detail, "the node is paused: disk critical, 4 GiB free (critical 5 GiB)") || v.Clearer.Action != "free disk on mini-1; it resumes by itself above 5 GiB" {
		t.Fatalf("pinned to a disk-paused node: %+v", v)
	}

	// The escalation check ignores parks and needs: an escalated task is still pinned_node_unavailable.
	s = base()
	s.PinnedNode, s.Needs, s.Parks = "gone", NodeNeeds{}, []Park{{Kind: ParkEscalation, RefID: "m1", Reason: "pinned"}}
	if why := PinnedUnavailable(s); why == "" || Evaluate(s).Reason != ReasonParked+ParkEscalation {
		t.Fatal(why, Evaluate(s))
	}
}

// TestEvaluateTie (node-ties 1.1): a task tied to a node carries the tie in its verdict whatever the reason, with
// the node's name, the reason, how it ends and why its work could not be saved; terminal tasks carry none.
func TestEvaluateTie(t *testing.T) {
	now := time.Date(2027, 1, 15, 8, 0, 0, 0, time.UTC)
	s := Snapshot{Task: Task{ID: "t1", Status: "READY", Assignee: "dev"}, Now: now, AgentConfigured: true, WakeSeq: 1,
		Nodes: []Node{{ID: "n1", Name: "hetzner-hel1", Online: true, Slots: 1, Held: 1}}, Needs: NodeNeeds{Provider: "codex"}}
	s.Nodes[0].Drivers = []NodeDriver{{Name: "codex", OK: true}}
	s.PinnedNode = "n1"
	s.Tie = &Tie{Node: "n1", Reason: TieWork, Since: now.Unix(), Release: WorkRelease, Unsaved: "secret finding: aws key in config.env"}
	v := Evaluate(s)
	if v.Tie == nil || v.Tie.NodeName != "hetzner-hel1" || v.Tie.Reason != TieWork || v.Tie.Release != WorkRelease || v.Tie.Since != now.Unix() {
		t.Fatalf("%+v", v.Tie)
	}
	if want := "tied to hetzner-hel1 (n1): its unsaved work is there (it could not be saved: secret finding: aws key in config.env)"; !strings.HasPrefix(v.Detail, want) {
		t.Fatalf("detail %q", v.Detail)
	}
	s.Tie = &Tie{Node: "n1", Reason: TieSession, Release: SessionRelease(90 * time.Minute)}
	s.PinnedNode = ""
	if v = Evaluate(s); v.Tie == nil || v.Tie.Say() != "tied to hetzner-hel1 (n1): its agent session is there" || v.Tie.Release != "90 min after the node goes inactive" {
		t.Fatalf("%+v", v.Tie)
	}
	s.Task.Status = "DONE"
	if v = Evaluate(s); v.Tie != nil {
		t.Fatal("terminal task tied", v.Tie)
	}
}

// TestDiskPauseVerdicts (disk-pause-conversations 1.2, 2.1): a node paused for a critical disk holds work with
// node_disk_critical and the fix (free disk; it resumes by itself), not "start or fix a node"; it still takes a
// conversation unless its disk is below the conversation floor; an owner pause says to resume the node with the
// owner's reason; "start or fix a node" stays for offline or incapable nodes.
func TestDiskPauseVerdicts(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	base := func(kind string) Snapshot {
		s := ready()
		s.Task.Kind, s.Task.ProjectID, s.Now = kind, "wrappie", now
		s.Nodes = []Node{{ID: "n1", Name: "tvorog-mac", Online: true, LastSeen: now.Unix(), Slots: 2, ConversationSlots: 1, Drivers: []NodeDriver{{Name: "claude", OK: true}}, Used: map[string]int{},
			Disk: &NodeDisk{Free: 36 << 30 / 10, Critical: 5 << 30, Floor: 1 << 30}}}
		s.Needs = NodeNeeds{Provider: "claude"}
		return s
	}
	work := Evaluate(base(""))
	if work.Reason != ReasonNodeDiskCritical || work.Detail != "tvorog-mac: disk critical, 3.6 GiB free (critical 5 GiB)" ||
		work.Clearer.Who != "owner" || work.Clearer.Action != "free disk on tvorog-mac; it resumes by itself above 5 GiB" {
		t.Fatalf("work task on a disk-paused node: %+v", work)
	}
	if v := Evaluate(base(KindConversation)); v.Reason != ReasonRunnable {
		t.Fatalf("conversation on a disk-paused node above the floor: %+v", v)
	}
	full := base(KindConversation)
	full.Nodes[0].Disk.Free, full.Nodes[0].Disk.Full = 1<<29, true
	if v := Evaluate(full); v.Reason != ReasonNodeDiskCritical || v.Clearer.Action != "free disk on tvorog-mac; it resumes by itself above 1 GiB" || !strings.Contains(v.Detail, "below the conversation floor 1 GiB") {
		t.Fatalf("conversation below the floor: %+v", v)
	}
	owner := base("")
	owner.Nodes[0].Disk, owner.Nodes[0].OwnerPaused, owner.Nodes[0].PauseReason = nil, true, "moving the Mac"
	if v := Evaluate(owner); v.Reason != ReasonNoCapableNode || v.Clearer.Action != "resume node tvorog-mac (paused by the owner: moving the Mac)" {
		t.Fatalf("owner pause: %+v", v)
	}
	if v := Evaluate(func() Snapshot { s := owner; s.Task.Kind = KindConversation; return s }()); v.Reason != ReasonNoCapableNode {
		t.Fatalf("an owner pause stops conversations too: %+v", v)
	}
	offline := base("")
	offline.Nodes[0].Disk, offline.Nodes[0].Online = nil, false
	if v := Evaluate(offline); v.Reason != ReasonNoCapableNode || !strings.HasPrefix(v.Clearer.Action, "start or fix a node with driver claude") {
		t.Fatalf("offline: %+v", v)
	}
	// Mixed: the disk pause comes first (it clears by itself once space is freed) and the detail lists the rest.
	mixed := base("")
	mixed.Nodes = append(mixed.Nodes, Node{ID: "n2", Name: "box", Online: false, Slots: 1, Drivers: []NodeDriver{{Name: "claude", OK: true}}})
	if v := Evaluate(mixed); v.Reason != ReasonNodeDiskCritical || !strings.Contains(v.Detail, "box (n2) offline") {
		t.Fatalf("mixed: %+v", v)
	}
}
