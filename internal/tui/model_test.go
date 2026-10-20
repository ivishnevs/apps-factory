package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// run executes a command and feeds its message back, as the program would (batches flattened; quit and
// nil messages dropped). It returns the messages it delivered.
func run(m *Model, cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch msg := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		out := []tea.Msg{}
		for _, c := range msg {
			out = append(out, run(m, c)...)
		}
		return out
	case tea.QuitMsg:
		return []tea.Msg{msg}
	}
	_, next := m.Update(msg)
	_ = next // follow-up commands (ticks, listens) are not run in tests
	return []tea.Msg{msg}
}

func key(m *Model, k string) tea.Cmd {
	_, cmd := m.Update(keyMsg(k))
	return cmd
}

func lastSent(t *testing.T, c *FixtureClient) Action {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.Sent) == 0 {
		t.Fatal("nothing sent")
	}
	return c.Sent[len(c.Sent)-1]
}

func TestTaskActionsConfirmFirst(t *testing.T) {
	m, c := demoModel(t, 80, 24)
	press(m, "2")
	// The cursor is on CAL-3 (in progress first). p asks; n cancels; nothing is sent.
	key(m, "p")
	if m.confirm == nil || m.confirm.Kind != "pause" || m.confirm.TaskID != "CAL-3" || m.confirm.Version != 3 {
		t.Fatalf("%+v", m.confirm)
	}
	key(m, "n")
	if m.confirm != nil || len(c.Sent) != 0 {
		t.Fatal("cancel sent something")
	}
	for _, tc := range []struct{ key, kind string }{{"p", "pause"}, {"u", "resume"}, {"r", "retry"}, {"n", "run"}} {
		key(m, tc.key)
		run(m, key(m, "y"))
		if a := lastSent(t, c); a.Kind != tc.kind || a.TaskID != "CAL-3" {
			t.Fatalf("%+v", a)
		}
		if !strings.Contains(m.toast, "fixture: not sent") {
			t.Fatal(m.toast)
		}
	}
	// Cancelling a task with open children cascades (and says so).
	m.tasks.cursor = indexOf(m.visibleTasks(), "CAL-1")
	key(m, "x")
	if !m.confirm.Cascade || !strings.Contains(m.confirm.Describe(), "open children") {
		t.Fatalf("%+v", m.confirm)
	}
	run(m, key(m, "enter"))
	if a := lastSent(t, c); a.Kind != "cancel" || !a.Cascade || a.TaskID != "CAL-1" {
		t.Fatalf("%+v", a)
	}
}

func TestTaskInputs(t *testing.T) {
	m, c := demoModel(t, 80, 24)
	press(m, "2")
	// Reassign: an unknown agent keeps the input open with an error.
	key(m, "a")
	typeText(m, "nobody")
	key(m, "enter")
	if m.input == nil || !m.toastEr || !strings.Contains(m.toast, "unknown agent") {
		t.Fatal(m.input, m.toast)
	}
	for range "nobody" {
		key(m, "backspace")
	}
	typeText(m, "calorie-qa")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "reassign" || a.Assignee != "calorie-qa" || a.Version != 3 {
		t.Fatalf("%+v", a)
	}
	// Reprioritize starts from the current priority.
	key(m, "P")
	if m.input.ti.Value() != "7" {
		t.Fatal(m.input.ti.Value())
	}
	key(m, "backspace")
	typeText(m, "9")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "reprioritize" || a.Priority != 9 {
		t.Fatalf("%+v", a)
	}
	// A directive starting with ! is urgent; esc on the input sends nothing.
	key(m, "d")
	typeText(m, "!ship it")
	key(m, "enter")
	if !strings.Contains(m.confirm.Describe(), "(urgent)") {
		t.Fatal(m.confirm.Describe())
	}
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "direct" || !a.Urgent || a.Text != "ship it" {
		t.Fatalf("%+v", a)
	}
	n := len(c.Sent)
	key(m, "c")
	typeText(m, "a comment")
	key(m, "esc")
	if m.input != nil || len(c.Sent) != n {
		t.Fatal("esc sent")
	}
	key(m, "c")
	typeText(m, "a comment")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "comment" || a.Text != "a comment" {
		t.Fatalf("%+v", a)
	}
	// While typing, keys go to the input: q does not quit.
	key(m, "c")
	if cmd := key(m, "q"); cmd != nil {
		if _, quit := cmd().(tea.QuitMsg); quit {
			t.Fatal("q quit while typing")
		}
	}
	if m.input.ti.Value() != "q" {
		t.Fatal(m.input.ti.Value())
	}
}

func TestTaskFilterAndDetail(t *testing.T) {
	m, _ := demoModel(t, 80, 24)
	press(m, "2", "/")
	typeText(m, "a:calorie-manager")
	key(m, "enter")
	list := m.visibleTasks()
	if len(list) != 2 || list[0].ID != "CAL-1" {
		t.Fatalf("%+v", list)
	}
	key(m, "esc") // clears the filter
	if m.tasks.filter != "" || len(m.visibleTasks()) != 9 {
		t.Fatal(m.tasks.filter)
	}
	key(m, "!")
	for _, tk := range m.visibleTasks() {
		if !m.needsOwner(tk) {
			t.Fatal(tk.ID)
		}
	}
	if len(m.visibleTasks()) != 2 {
		t.Fatal(len(m.visibleTasks()))
	}
	key(m, "!")
	// conv shows the conversation tasks.
	m.tasks.setFilter(m, "conv p:notes")
	if len(m.visibleTasks()) != 4 {
		t.Fatal(len(m.visibleTasks()))
	}
	m.tasks.setFilter(m, "")
	// enter loads the detail; esc returns to the list.
	m.tasks.cursor = indexOf(m.visibleTasks(), "CAL-3")
	run(m, key(m, "enter"))
	if !m.tasks.open || m.tasks.detail == nil || m.tasks.detail.Task.ID != "CAL-3" {
		t.Fatalf("%+v", m.tasks)
	}
	// L opens the task's live run.
	key(m, "L")
	if m.tab != tabRun || m.run.id != "r-ios-7" {
		t.Fatal(m.tab, m.run.id)
	}
	key(m, "esc")
	if m.tab != tabTasks {
		t.Fatal(m.tab)
	}
	key(m, "esc")
	if m.tasks.open {
		t.Fatal("detail still open")
	}
}

func TestInboxAnswersAndApprovals(t *testing.T) {
	m, c := demoModel(t, 80, 24)
	press(m, "6")
	// The question has options: → picks the second, enter confirms, y sends a structured answer.
	key(m, "right")
	key(m, "enter")
	run(m, key(m, "y"))
	a := lastSent(t, c)
	if a.Kind != "answer" || a.MessageID != "m-ask-1" || a.Text != "nutritionix-paid" || a.Answers["answer"] != "nutritionix-paid" || a.Version != 1 {
		t.Fatalf("%+v", a)
	}
	// Free text.
	key(m, "a")
	typeText(m, "USDA for now")
	key(m, "enter")
	run(m, key(m, "y"))
	if a = lastSent(t, c); a.Text != "USDA for now" || a.Answers != nil {
		t.Fatalf("%+v", a)
	}
	// The approval: a is refused, y approves with a reason, n rejects and needs one.
	key(m, "j")
	key(m, "a")
	if m.input != nil || !m.toastEr {
		t.Fatal("answered an approval")
	}
	key(m, "y")
	key(m, "enter")
	run(m, key(m, "y"))
	if a = lastSent(t, c); a.Kind != "approve" || a.ApprovalID != "ap-1" || a.Text != "approved by owner" || a.Version != 1 {
		t.Fatalf("%+v", a)
	}
	key(m, "n")
	key(m, "enter")
	if m.input == nil || !strings.Contains(m.toast, "reason is required") {
		t.Fatal("empty rejection accepted")
	}
	typeText(m, "not yet")
	key(m, "enter")
	run(m, key(m, "y"))
	if a = lastSent(t, c); a.Kind != "reject" || a.Text != "not yet" {
		t.Fatalf("%+v", a)
	}
}

func TestKillSwitch(t *testing.T) {
	m, c := demoModel(t, 80, 24)
	key(m, "K")
	typeText(m, "incident")
	key(m, "enter")
	if !strings.Contains(m.confirm.Describe(), "STOP the company") {
		t.Fatal(m.confirm.Describe())
	}
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "stop" || a.Text != "incident" {
		t.Fatalf("%+v", a)
	}
	// Stopped: the status bar says so, K offers start.
	ov := m.ov
	ov.Pauses = append(ov.Pauses, storage.Pause{Scope: "company", Reason: "stop: incident", Actor: "owner"})
	m.Update(overviewMsg{ov: ov})
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "■ STOPPED stop: incident") {
		t.Fatal(s)
	}
	key(m, "K")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "start" {
		t.Fatalf("%+v", a)
	}
}

// TestRunFollow: new events keep the view at the bottom while following; scrolling up stops following and
// new events then leave the view where it is; G follows again.
func TestRunFollow(t *testing.T) {
	m, _ := demoModel(t, 80, 12)
	openDemoRun(m, "r-ios-7")
	m.Render()
	if !m.run.follow || !m.run.vp.AtBottom() {
		t.Fatal("not at bottom")
	}
	key(m, "k")
	key(m, "k")
	if m.run.follow {
		t.Fatal("still following")
	}
	m.Render()
	offset := m.run.vp.YOffset()
	ev := m.c.(*FixtureClient).F.RunEvents["r-ios-7"][1]
	ev.Seq = 100
	m.Update(streamMsg{Stream: "run:r-ios-7", Run: &ev})
	m.Render()
	if m.run.vp.YOffset() != offset || len(m.run.events) != 16 {
		t.Fatal(m.run.vp.YOffset(), offset, len(m.run.events))
	}
	// A replayed event (seq already seen) is dropped.
	m.Update(streamMsg{Stream: "run:r-ios-7", Run: &ev})
	if len(m.run.events) != 16 {
		t.Fatal(len(m.run.events))
	}
	key(m, "G")
	m.Render()
	if !m.run.follow || !m.run.vp.AtBottom() {
		t.Fatal("G did not follow")
	}
	// The end of the run.
	cmd, _ := m.Update(streamMsg{Stream: "run:r-ios-7", End: true, Outcome: "succeeded"})
	_ = cmd
	if !m.run.ended || !strings.Contains(ansi.Strip(m.Render()), "ended succeeded") {
		t.Fatal("end not shown")
	}
	// Events of another run's stream are ignored.
	m.Update(streamMsg{Stream: "run:other", Run: &ev})
	if len(m.run.events) != 16 {
		t.Fatal("foreign event")
	}
}

func TestFeedDedupAndState(t *testing.T) {
	m, _ := demoModel(t, 80, 24)
	n := len(m.feed)
	old := m.feed[0]
	m.Update(streamMsg{Stream: "feed", Feed: &old})
	if len(m.feed) != n {
		t.Fatal("duplicate kept")
	}
	m.Update(streamMsg{Stream: "feed", State: true, Connected: false, Err: "EOF"})
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "feed reconnecting") {
		t.Fatal(s)
	}
	press(m, "5")
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "reconnecting: EOF") {
		t.Fatal(s)
	}
	// Scrolling up pauses the feed view; a new row does not move it; G returns to the newest.
	key(m, "k")
	if m.feedv.follow {
		t.Fatal("follow")
	}
	item := storage.FeedItem{Sequence: 999, Type: "task.created", ProjectID: "notes", TaskID: "N-9", Agent: "demo", Actor: "manager", Summary: "brand new", CreatedAt: DemoAt}
	m.Update(streamMsg{Stream: "feed", Feed: &item})
	if strings.Contains(ansi.Strip(m.Render()), "brand new") {
		t.Fatal("paused view moved")
	}
	key(m, "G")
	if !strings.Contains(ansi.Strip(m.Render()), "brand new") {
		t.Fatal("newest not shown")
	}
	// The new row is also an interaction in the flow.
	ins := interactions(m.feed, FlowFilter{Task: "N-9"})
	if len(ins) != 1 || ins[0].From != "manager" || ins[0].To != "demo" || ins[0].Verb != "create" {
		t.Fatalf("%+v", ins)
	}
}

func TestResizeAndSmallTerminal(t *testing.T) {
	m, _ := demoModel(t, 80, 24)
	for _, tb := range []string{"1", "2", "3", "4", "5", "6"} {
		press(m, tb)
		for _, size := range [][2]int{{60, 20}, {200, 50}, {80, 24}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			screen(t, m) // exact size, every line
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 10, Height: 3})
	if m.Render() != "terminal too small" {
		t.Fatal(m.Render())
	}
	v := m.View()
	if !v.AltScreen {
		t.Fatal("not full screen")
	}
}

func TestQuitAndHelp(t *testing.T) {
	m, _ := demoModel(t, 80, 24)
	key(m, "?")
	if !m.help {
		t.Fatal("no help")
	}
	if cmd := key(m, "q"); cmd != nil || m.help {
		t.Fatal("q in help should close help only")
	}
	cmd := key(m, "q")
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q did not quit")
	}
	if m.ctx.Err() == nil {
		t.Fatal("streams not stopped")
	}
}

func TestTabsAndDashboardNavigation(t *testing.T) {
	m, _ := demoModel(t, 80, 24)
	key(m, "tab")
	if m.tab != tabTasks {
		t.Fatal(m.tab)
	}
	press(m, "1")
	// The cursor starts on calorie-manager; j moves to calorie-designer, then calorie-ios (running): enter opens
	// its run.
	press(m, "j", "j")
	key(m, "enter")
	if m.tab != tabRun || m.run.id != "r-ios-7" {
		t.Fatal(m.tab, m.run.id)
	}
	press(m, "1", "k")
	key(m, "enter") // calorie-designer is not running: its tasks
	if m.tab != tabTasks || m.tasks.filter != "a:calorie-designer" {
		t.Fatal(m.tab, m.tasks.filter)
	}
	press(m, "1", "f")
	if m.tab != tabFlow || m.flow.filter.Agent != "calorie-designer" {
		t.Fatal(m.flow.filter)
	}
	key(m, "x")
	key(m, "p")
	if m.flow.filter.Project != "calorie" {
		t.Fatal(m.flow.filter)
	}
	key(m, "a")
	typeText(m, "demo")
	key(m, "enter")
	if m.confirm != nil || m.flow.filter.Agent != "demo" {
		t.Fatal("a flow filter asked for confirmation")
	}
}

// TestOverviewErrors: an unreachable Core shows in the status bar; the last good data stays.
func TestOverviewErrors(t *testing.T) {
	c := &HTTPClient{API: worker.API{URL: "http://127.0.0.1:1", Token: "secret-token-xyz"}}
	m := New(Options{Client: c, Now: func() time.Time { return DemoAt }, Location: time.UTC, NoStreams: true})
	defer m.Close()
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.Update(overviewMsg{err: errors.New("dial tcp 127.0.0.1:1: connection refused")})
	s := ansi.Strip(m.Render())
	if !strings.Contains(s, "unreachable") || !strings.Contains(s, "connection refused") {
		t.Fatal(s)
	}
	m.Update(overviewMsg{ov: DemoFixture().Overview})
	m.Update(overviewMsg{err: errors.New("timeout")})
	s = ansi.Strip(m.Render())
	if !strings.Contains(s, "retrying") || !strings.Contains(s, "calorie-ios") {
		t.Fatal(s)
	}
	for _, tb := range []string{"1", "2", "3", "4", "5", "6", "?"} {
		press(m, tb)
		if strings.Contains(m.Render(), "secret-token-xyz") {
			t.Fatal("token on screen")
		}
		press(m, "esc")
	}
	// The real client against a closed port fails without the token in the error.
	_, err := c.Overview(context.Background())
	if err == nil || strings.Contains(err.Error(), "secret-token-xyz") {
		t.Fatal(err)
	}
}

// TestFixtureStreams: the fixture client replays the feed and a run, and ends an ended run.
func TestFixtureStreams(t *testing.T) {
	c := &FixtureClient{F: DemoFixture()}
	ctx, cancel := context.WithCancel(context.Background())
	got := 0
	done := make(chan struct{})
	go func() {
		c.StreamFeed(ctx, FeedQuery{Attention: true}, 0, func(e StreamEvent) {
			if e.Feed != nil {
				got++
			}
		})
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
	if got != 6 {
		t.Fatal(got)
	}
	events := []StreamEvent{}
	c.StreamRun(context.Background(), "r-des-3", 0, 0, func(e StreamEvent) { events = append(events, e) })
	if last := events[len(events)-1]; !last.End || last.Outcome != "succeeded" || len(events) != 5 {
		t.Fatalf("%+v", events)
	}
	events = nil
	c.StreamRun(context.Background(), "r-des-3", 0, 1, func(e StreamEvent) { events = append(events, e) })
	if len(events) != 3 {
		t.Fatalf("%+v", events)
	}
}
