package support

import (
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// TestDoctorReplay (setup-assistant 3.1; tvorog 2026-11-11): every fault of the day is an item with its fix and runbook,
// problems first; self-clearing waits are not stuck.
func TestDoctorReplay(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	d := BuildDoctor(DoctorInput{Company: "tvorog", Now: now, Since: now.Add(-24 * time.Hour),
		Nodes: []DoctorNode{
			{ID: "n1", Name: "tvorog-mac", Online: true, Disk: "critical", FreeBytes: 3865470566, Low: 20 << 30, Critical: 5 << 30,
				Drivers: []NodeDriver{{Name: "claude", OK: true}}},
			{ID: "n2", Name: "mac2", Online: true, Paused: true, PauseReason: "WARP",
				Drivers: []NodeDriver{{Name: "claude", OK: false, Check: "probe", Detail: "net.api: cannot resolve api.anthropic.com"}},
				Checks:  []NodeCheck{{Check: "net.forge", Detail: "github.com not reached", Hint: "check its VPN"}}},
			{ID: "n3", Name: "air", Online: false, LastSeen: now.Add(-2 * time.Hour).Unix(), Drivers: []NodeDriver{{Name: "codex", OK: false, Check: "auth", Detail: "not logged in"}}},
		},
		Pauses: []DoctorPause{{Scope: "project", Target: "tracely", Reason: "moving in"}, {Scope: "node", Target: "n2"}},
		FailedRuns: []FailedRun{
			{ID: "r1", Outcome: "setup_failed", NodeFault: protocol.FaultNetwork, Result: "getaddrinfo ENOTFOUND api.anthropic.com"},
			{ID: "r2", Outcome: "setup_failed", NodeFault: protocol.FaultNetwork, Result: "getaddrinfo ENOTFOUND api.anthropic.com"},
			{ID: "r3", Outcome: "no_exit", Result: "done"},
		},
		Tasks: []TaskWait{
			{TaskID: "conv-ceo-tracely", Project: "tracely", Assignee: "ceo", Kind: tasks.KindConversation,
				Verdict: tasks.Verdict{Reason: tasks.ReasonPaused + "project:tracely", Detail: "paused by the owner", Clearer: tasks.Clearer{Who: "owner", Action: "factory resume --project tracely"}}},
			{TaskID: "w1", Project: "app", Verdict: tasks.Verdict{Reason: tasks.ReasonLiveRun}, Queued: &QueuedWhy{Reason: "affinity", Detail: "tied to mac2", Holder: "n2", Inactive: "paused"}},
			{TaskID: "w2", Project: "app", Verdict: tasks.Verdict{Reason: tasks.ReasonLiveRun}, Queued: &QueuedWhy{Reason: "agent_busy"}},
			{TaskID: "w3", Project: "app", Verdict: tasks.Verdict{Reason: tasks.ReasonBackoff}},
		},
		Pending: Pending{Inbox: 2, Total: 2},
	})
	if d.OK {
		t.Fatal("problems found but OK")
	}
	text := d.Text()
	for _, want := range []string{
		"tvorog-mac: disk critical: 3.6 GiB free (critical 5 GiB)", "runbook: factory-setup runbook disk",
		"mac2 claude: claude on mac2 is disabled", "runbook: factory-setup runbook dns-and-proxy",
		"air: offline, last seen 2h0m0s ago", "air codex", "factory-node login codex",
		"mac2: node paused by the owner: WARP", "factory resume --node n2", "project tracely: paused: moving in", "factory resume --project tracely",
		"2 failed run(s): node fault: network", "chat with ceo in tracely", "task w1", "agent-does-not-answer", "2 item(s) wait for you",
		"mac2 net.forge",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("%q missing in\n%s", want, text)
		}
	}
	for _, not := range []string{"task w2", "task w3"} {
		if strings.Contains(text, not) {
			t.Errorf("self-clearing wait listed: %s", not)
		}
	}
	if !strings.HasPrefix(strings.SplitN(text, "\n", 3)[1], "!") {
		t.Fatalf("problems not first:\n%s", text)
	}
	if s := d.Summary(2); !strings.Contains(s, "Needs fixing:") || !strings.Contains(s, "more (factory doctor --company)") {
		t.Fatalf("summary %q", s)
	}
	if ok := BuildDoctor(DoctorInput{Company: "c", Now: now, Nodes: []DoctorNode{{ID: "n", Online: true, Drivers: []NodeDriver{{Name: "claude", OK: true}}}}}); !ok.OK || ok.Summary(5) != "" || !strings.Contains(ok.Text(), "all good") {
		t.Fatalf("healthy company: %+v", ok)
	}
}

func TestUsage(t *testing.T) {
	got := Usage([]protocol.Usage{{Model: "claude-sonnet", Input: 4000, Output: 6000, CacheRead: 330000, CacheWrite: 12000}})
	for _, want := range []string{"352k tokens", "330k cache reads (93%", "12k cache writes", "4k new input", "6k output", "claude-sonnet"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing in %q", want, got)
		}
	}
	if Usage(nil) != "no token usage reported" || count(1_250_000) != "1.2M" {
		t.Fatal("edge cases")
	}
}
