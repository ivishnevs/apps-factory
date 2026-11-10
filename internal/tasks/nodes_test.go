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
		{"capable node paused", func(s *Snapshot) { s.Nodes[0].Paused = true }, ReasonNoCapableNode, "mini-1 (n1) paused"},
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
