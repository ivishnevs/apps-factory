package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"appfactory/internal/core"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// TestHTTPClientQueuedParksCosts (owner-ui 6.1, 6.4, 6.5): the task detail carries Core's queued reason and
// parks (none from an older Core), and the Costs view reads the ledger once per grouping.
func TestHTTPClientQueuedParksCosts(t *testing.T) {
	fc, _, c := newFakeCore(t)
	ctx := context.Background()
	d, err := c.TaskDetail(ctx, "P1-9", nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Queued == nil || d.Queued.Reason != storage.QueuedDriverDisabled || len(d.Queued.Nodes) != 3 || d.Why.Reason != "no_capable_node" {
		t.Fatalf("%+v %+v", d.Queued, d.Why)
	}
	if d, err = c.TaskDetail(ctx, "P1-5", nil); err != nil || len(d.Parks) != 1 || d.Parks[0].Kind != "ask" || d.Parks[0].RefID != "m-ask-1" {
		t.Fatalf("%+v %v", d.Parks, err)
	}
	if d, err = c.TaskDetail(ctx, "P1-3", nil); err != nil || d.Parks != nil {
		t.Fatalf("an older Core's parks: %+v %v", d.Parks, err)
	}
	since := DemoAt.AddDate(0, 0, -6)
	v, err := c.Costs(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	for _, by := range costGroupings {
		if fc.count("GET /costs?by="+by+"&since=") != 1 {
			t.Errorf("no ledger read by %s: %v", by, fc.requests)
		}
	}
	if len(v.Projects.Groups) != 3 || len(v.Models.Groups) != 2 || v.Days.Total.Runs == 0 || v.Days.Total.CostUSD != v.Agents.Total.CostUSD {
		t.Fatalf("%+v", v)
	}
}

func (fc *fakeCore) count(prefix string) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	n := 0
	for _, r := range fc.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// TestExplainConflict: Core's bare 409 becomes what changed, per action; other errors stay as they are.
func TestExplainConflict(t *testing.T) {
	fc, _, c := newFakeCore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		a    Action
		path string
		want string
	}{
		{Action{Kind: "answer", MessageID: "m-1", Version: 1, Text: "x"}, "/inbox/m-1/answer", "message m-1 was answered, withdrawn or changed meanwhile"},
		{Action{Kind: "read", MessageID: "m-2", Version: 1}, "/inbox/m-2/read", "already read"},
		{Action{Kind: "pause", TaskID: "T-1", Version: 3}, "/tasks/T-1/pause", "task T-1 changed meanwhile (version 3 is stale); refreshed"},
		{Action{Kind: "approve", ApprovalID: "ap-1", Version: 1, Text: "ok"}, "/approvals/ap-1/decision", "approval ap-1 was decided"},
	} {
		fc.fail[tc.path] = 409
		_, err := c.Do(ctx, tc.a)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "state or version conflict") {
			t.Errorf("%s: %v", tc.a.Kind, err)
		}
	}
	fc.fail["/tasks/T-2/pause"] = 500
	if _, err := c.Do(ctx, Action{Kind: "pause", TaskID: "T-2"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal(err)
	}
}

// TestQueuedCause (owner-ui 6.1): a QUEUED run shows Core's cause on its card (in Ready, not Running), in its
// task's why, on its agent's row and in the run view, with the hold-off end in local time; each cause has a
// label, and without Core's reason the run waits for a node.
func TestQueuedCause(t *testing.T) {
	m, _ := demoModel(t, 140, 40)
	if col := m.columnOf(mustTask(t, m, "P1-9")); col != "ready" {
		t.Fatal(col)
	}
	why := m.whyLine(mustTask(t, m, "P1-9"))
	if !strings.HasPrefix(why, "↳ queued: driver disabled · mac-studio (n1): driver claude disabled: auth check failed: 401 invalid credentials") ||
		!strings.HasSuffix(why, "· until 14:40:00") || strings.Contains(why, "2026-") {
		t.Fatal(why)
	}
	var qa agentRow
	for _, r := range m.agentRows() {
		if r.Name == "qa" {
			qa = r
		}
	}
	if qa.state != "queued" || !strings.Contains(qa.queued, "driver disabled") {
		t.Fatalf("%+v", qa)
	}
	press(m, "2")
	if s := screen(t, m); !strings.Contains(s, "◷ queued: driver disable") {
		t.Fatal(s)
	}
	openDemoRun(m, "r-qa-1")
	if s := screen(t, m); !strings.Contains(s, "◷ queued") || !strings.Contains(s, "Why queued") || !strings.Contains(s, "linux-1: has no claude driver") {
		t.Fatal(s)
	}
	for reason, label := range map[string]string{storage.QueuedPaused: "paused", storage.QueuedAgentBusy: "agent busy", storage.QueuedNoSlot: "no free slot",
		storage.QueuedResourceBusy: "resource busy", storage.QueuedNodePaused: "node paused", storage.QueuedNodeOffline: "node offline", storage.QueuedNoNode: "no node"} {
		if got := m.queuedText(&storage.QueuedReason{Reason: reason, Detail: "d"}); got != label+" · d" {
			t.Errorf("%s: %q", reason, got)
		}
	}
	if got := m.queuedText(nil); got != "waits for a node to claim it" {
		t.Fatal(got)
	}
}

func mustTask(t *testing.T, m *Model, id string) Task {
	t.Helper()
	tk, ok := m.taskByID(id)
	if !ok {
		t.Fatal(id)
	}
	return tk
}

// TestNodesScreen (owner-ui 6.2, 6.3): the nodes needing care count on the tab; p pauses the selected node
// after a reason and a confirmation; u on a pause resumes it after a confirmation.
func TestNodesScreen(t *testing.T) {
	m, c := demoModel(t, 160, 40)
	if n := m.nodesNeedingCare(); n != 2 {
		t.Fatal(n)
	}
	press(m, "7")
	s := screen(t, m)
	for _, want := range []string{"7 Nodes ⚠2", "claude disabled until 14:40:00: auth check failed", "1 transient failure", "below low (low 10.0 GiB, critical 2.0 GiB)", "silent 7m",
		"⏸ quota key codex-team", "driver_unsupported", "node.low_disk"} {
		if !strings.Contains(s, want) {
			t.Fatalf("%q missing:\n%s", want, s)
		}
	}
	press(m, "p")
	if m.input == nil {
		t.Fatal("no reason input")
	}
	typeText(m, "maintenance")
	press(m, "enter")
	if m.confirm == nil || m.confirm.Kind != "hold" || m.confirm.Scope != "node" || m.confirm.Target != "n1" {
		t.Fatalf("%+v", m.confirm)
	}
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "hold" || a.Target != "n1" || a.Text != "maintenance" {
		t.Fatalf("%+v", a)
	}
	press(m, "l", "j", "u")
	if m.confirm == nil || m.confirm.Kind != "release" || m.confirm.Scope != "quota_key" || m.confirm.Target != "codex-team" {
		t.Fatalf("%+v", m.confirm)
	}
	press(m, "n")
	if m.confirm != nil {
		t.Fatal("not cancelled")
	}
	// A paused node: u on it resumes it.
	m.ov.Pauses = append(m.ov.Pauses, storage.Pause{Scope: "node", Target: "n1", Reason: "maintenance", Actor: "owner"})
	press(m, "h", "u")
	if m.confirm == nil || m.confirm.Kind != "release" || m.confirm.Target != "n1" {
		t.Fatalf("%+v", m.confirm)
	}
}

// TestPauseChooser (owner-ui 6.3): pause picks a scope, then a target, then a reason; a quota key is typed.
func TestPauseChooser(t *testing.T) {
	m, c := demoModel(t, 120, 30)
	m.openPauseChooser("", "")
	if m.chooser == nil || len(m.chooser.options) != 4 {
		t.Fatalf("%+v", m.chooser)
	}
	press(m, "2") // an agent
	if m.chooser == nil || m.chooser.options[0] != "designer" {
		t.Fatalf("%+v", m.chooser)
	}
	press(m, "j", "enter") // developer-1
	typeText(m, "runaway costs")
	press(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "hold" || a.Scope != "agent" || a.Target != "developer-1" || a.Text != "runaway costs" {
		t.Fatalf("%+v", a)
	}
	m.openPauseChooser("", "")
	press(m, "3") // a quota key: typed
	typeText(m, "claude-max")
	press(m, "enter")
	typeText(m, "limit")
	press(m, "enter")
	if m.confirm == nil || m.confirm.Scope != "quota_key" || m.confirm.Target != "claude-max" || m.confirm.Text != "limit" {
		t.Fatalf("%+v", m.confirm)
	}
}

// TestCostsScreen (owner-ui 6.4): the ledger grouped by day, agent, model and project; r cycles the range
// and rereads it.
func TestCostsScreen(t *testing.T) {
	f := DemoFixture()
	v := groupCosts(f.Costs, startOfDay(DemoAt), time.UTC)
	if v.Days.Total.Runs == 0 || len(v.Days.Groups) != 1 || v.Days.Groups[0].Key != "2026-10-20" {
		t.Fatalf("%+v", v.Days)
	}
	all := groupCosts(f.Costs, DemoAt.AddDate(0, 0, -30), time.UTC)
	if all.Days.Total.Sources["none"] != 1 || all.Projects.Total.Runs != len(f.Costs) {
		t.Fatalf("%+v", all.Projects.Total)
	}
	m, _ := demoModel(t, 120, 30)
	cmd := m.switchTab(tabCosts)
	if cmd == nil || !m.costs.loading {
		t.Fatal("the Costs tab does not read the ledger")
	}
	run(m, cmd)
	if m.costs.view == nil || !m.costs.view.Since.Equal(startOfDay(DemoAt).AddDate(0, 0, -6)) {
		t.Fatalf("%+v", m.costs.view)
	}
	s := screen(t, m)
	for _, want := range []string{"Costs · last 7 days", "By day", "By agent", "By model", "By project", "1 without a price", "Tue Oct 20"} {
		if !strings.Contains(s, want) {
			t.Fatalf("%q missing:\n%s", want, s)
		}
	}
	run(m, key(m, "r"))
	if !m.costs.view.Since.Equal(startOfDay(DemoAt).AddDate(0, 0, -29)) || !strings.Contains(screen(t, m), "last 30 days") {
		t.Fatal(m.costs.view.Since)
	}
}

// TestNotificationItems (owner fix): a notification takes no answer: the prompt bar says "says" with R reply
// and M mark read; R opens the chat with its sender, M marks it read, A only explains; in the inbox r and m
// do the same, and a notification from an agent who is not a direct report replies with a directive.
func TestNotificationItems(t *testing.T) {
	m, c := demoModel(t, 140, 40)
	note := m.ov.Inbox[3]
	m.ov.Inbox = []messaging.Message{note}
	s := ansiStrip(m.Render())
	if !strings.Contains(s, "pm says: Module 1 offline store is merged") || !strings.Contains(s, "[R reply · M read]") || strings.Contains(s, "pm asks") {
		t.Fatal(s)
	}
	press(m, "A")
	if m.input != nil || m.chooser != nil || len(c.Sent) != 0 || !strings.Contains(m.toast, "takes no answer") {
		t.Fatalf("A on a notification: %q %+v", m.toast, c.Sent)
	}
	cmd := key(m, "R")
	if m.tab != tabChat || !m.chat.typing || m.chatAgent() != "pm" || m.chatProject() != "project-1" || cmd == nil {
		t.Fatal(m.tab, m.chat.typing, m.chatAgent(), m.chatProject())
	}
	press(m, "esc", "1")
	run(m, key(m, "M"))
	if a := lastSent(t, c); a.Kind != "read" || a.MessageID != "m-note-1" || a.Version != 1 {
		t.Fatalf("%+v", a)
	}
	// In the inbox: m marks read, a refuses; a non-report's notification is answered with a directive.
	press(m, "5")
	run(m, key(m, "m"))
	if len(c.Sent) != 2 {
		t.Fatalf("%+v", c.Sent)
	}
	m.ov.Inbox[0].Sender, m.ov.Inbox[0].TaskID = "developer-1", "P1-3"
	press(m, "r")
	if m.input == nil || m.input.action.Kind != "direct" || m.input.action.TaskID != "P1-3" {
		t.Fatalf("%+v", m.input)
	}
}

// TestRenderOnlyOnChange (owner-ui 6.6): with nothing live on the screen the model asks for no animation
// frames (a pending spinner tick is dropped and none starts); a live run on a screen that shows it animates.
func TestRenderOnlyOnChange(t *testing.T) {
	f := DemoFixture()
	idle := f.Overview
	idle.Status = nil
	for _, p := range f.Overview.Status {
		p.Runs = []core.RunStatus{}
		idle.Status = append(idle.Status, p)
	}
	m := New(Options{Client: &FixtureClient{F: f}, Now: func() time.Time { return DemoAt }, Location: time.UTC, Local: LocalTranscripts{Off: true}, Notify: NotifyOff})
	t.Cleanup(m.Close)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(overviewMsg{ov: idle})
	if m.wantsAnim() {
		t.Fatal("idle model wants animation")
	}
	// The loading spinner's pending tick arrives: no next frame.
	if _, cmd := m.Update(m.spin.Tick()); cmd != nil || m.spinning {
		t.Fatal("an idle model scheduled another frame")
	}
	for _, tb := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		press(m, tb)
		if m.wantsAnim() || m.spinning {
			t.Fatal("idle tab", tb, "animates")
		}
	}
	if _, cmd := m.Update(tickMsg(DemoAt)); cmd == nil {
		t.Fatal("no refresh") // the data refresh stays
	} else if m.spinning {
		t.Fatal("the refresh started the animation")
	}
	// A live run on the dashboard animates; on the Costs screen it does not.
	press(m, "1")
	_, cmd := m.Update(overviewMsg{ov: f.Overview})
	if !m.wantsAnim() || !m.spinning || cmd == nil {
		t.Fatal("a live run does not animate")
	}
	if _, cmd = m.Update(m.spin.Tick()); cmd == nil {
		t.Fatal("the animation stopped with a live run on screen")
	}
	press(m, "8")
	if m.wantsAnim() {
		t.Fatal("the Costs screen animates")
	}
	if _, cmd = m.Update(m.spin.Tick()); cmd != nil || m.spinning {
		t.Fatal("frames continue on a screen without live runs")
	}
}
