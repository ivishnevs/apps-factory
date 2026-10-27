package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/messaging"
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
	m.Update(msg)
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

func onBoard(t *testing.T, m *Model, id string) {
	t.Helper()
	press(m, "2")
	m.focusTask(id)
	if tk, ok := m.board.selected(m); !ok || tk.ID != id {
		t.Fatalf("focus %s: %+v", id, tk)
	}
}

func TestBoardColumnsAndNavigation(t *testing.T) {
	m, _ := demoModel(t, 160, 40)
	press(m, "2")
	got := map[string][]string{}
	for _, c := range m.columns() {
		for _, tk := range c.tasks {
			got[c.key] = append(got[c.key], tk.ID)
		}
	}
	want := map[string]string{"backlog": "P2-4 P3-4 P1-6", "running": "P3-2 P1-3", "review": "P2-1 P3-6", "done": "P1-2 P1-8 P2-6 P3-1"}
	for k, v := range want {
		if strings.Join(got[k], " ") != v {
			t.Errorf("%s: %v, want %s", k, got[k], v)
		}
	}
	if len(got["waiting"]) != 5 || got["waiting"][0] != "P2-3" || got["cancelled"] != nil {
		t.Fatal(got["waiting"], got["cancelled"])
	}
	// Owner-waiting cards first, cancelled hidden until z.
	key(m, "z")
	if len(m.columns()) != 7 {
		t.Fatal("cancelled column not shown")
	}
	// Arrows move between columns and cards.
	press(m, "right", "right", "down")
	if tk, _ := m.board.selected(m); tk.ID != "P1-3" {
		t.Fatal(tk.ID)
	}
	// The project chip narrows the board.
	press(m, "]")
	if m.project != "project-1" {
		t.Fatal(m.project)
	}
	for _, tk := range m.boardTasks() {
		if tk.ProjectID != "project-1" {
			t.Fatal(tk.ID)
		}
	}
	press(m, "[")
	if m.project != "" {
		t.Fatal(m.project)
	}
	// Filter: words, agent, needs-you.
	press(m, "/")
	typeText(m, "a:developer-2")
	press(m, "enter")
	for _, tk := range m.boardTasks() {
		if tk.Assignee != "developer-2" {
			t.Fatal(tk.ID)
		}
	}
	press(m, "esc")
	key(m, "!")
	if n := len(m.boardTasks()); n != 4 { // P1-1 has pm's project approval
		t.Fatal(n)
	}
}

func TestTaskActionsConfirmFirst(t *testing.T) {
	m, c := demoModel(t, 120, 40)
	onBoard(t, m, "P1-3")
	key(m, "p")
	if m.confirm == nil || m.confirm.Kind != "pause" || m.confirm.TaskID != "P1-3" || m.confirm.Version != 3 {
		t.Fatalf("%+v", m.confirm)
	}
	key(m, "n")
	if m.confirm != nil || len(c.Sent) != 0 {
		t.Fatal("cancel sent something")
	}
	for _, tc := range []struct{ key, kind string }{{"p", "pause"}, {"u", "resume"}, {"r", "retry"}} {
		key(m, tc.key)
		run(m, key(m, "y"))
		if a := lastSent(t, c); a.Kind != tc.kind || a.TaskID != "P1-3" {
			t.Fatalf("%+v", a)
		}
		if !strings.Contains(m.toast, "fixture: not sent") {
			t.Fatal(m.toast)
		}
	}
	// Cancelling a task with open children cascades (and says so).
	m.focusTask("P1-1")
	key(m, "x")
	if !m.confirm.Cascade || !strings.Contains(m.confirm.Describe(), "open children") {
		t.Fatalf("%+v", m.confirm)
	}
	run(m, key(m, "enter"))
	if a := lastSent(t, c); a.Kind != "cancel" || !a.Cascade || a.TaskID != "P1-1" {
		t.Fatalf("%+v", a)
	}
}

func TestTaskInputs(t *testing.T) {
	m, c := demoModel(t, 120, 40)
	onBoard(t, m, "P1-3")
	key(m, "a")
	typeText(m, "nobody")
	key(m, "enter")
	if m.input == nil || !m.toastEr || !strings.Contains(m.toast, "unknown agent") {
		t.Fatal(m.input, m.toast)
	}
	for range "nobody" {
		key(m, "backspace")
	}
	typeText(m, "qa")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "reassign" || a.Assignee != "qa" || a.Version != 3 {
		t.Fatalf("%+v", a)
	}
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

func TestCardDetailModal(t *testing.T) {
	m, c := demoModel(t, 120, 40)
	onBoard(t, m, "P1-3")
	run(m, key(m, "enter"))
	if !m.board.open || m.board.detail == nil || m.board.detail.Task.ID != "P1-3" {
		t.Fatalf("%+v", m.board)
	}
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "P1-3 · Task 3") || !strings.Contains(s, "Runs (2)") {
		t.Fatal(s)
	}
	// Actions work from the modal; q closes it instead of quitting.
	key(m, "r")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "retry" || a.TaskID != "P1-3" {
		t.Fatalf("%+v", a)
	}
	if cmd := key(m, "q"); cmd != nil || m.board.open {
		t.Fatal("q did not close the modal")
	}
	// L opens the live run; esc returns.
	run(m, key(m, "L"))
	if m.tab != tabRun || m.run.id != "r-d1-7" {
		t.Fatal(m.tab, m.run.id)
	}
	key(m, "esc")
	if m.tab != tabBoard {
		t.Fatal(m.tab)
	}
}

func TestPromptBar(t *testing.T) {
	m, c := demoModel(t, 140, 40)
	s := ansi.Strip(m.Render())
	if !strings.Contains(s, "→ 12:55:00 pm asks: Which option for Task 5") || !strings.Contains(s, "+5 in inbox") {
		t.Fatal(s)
	}
	// The oldest item is a question with options: A opens the chooser; 2 picks the second option.
	key(m, "A")
	if m.chooser == nil || len(m.chooser.options) != 3 {
		t.Fatalf("%+v", m.chooser)
	}
	key(m, "2")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "answer" || a.MessageID != "m-ask-1" || a.Answers["answer"] != "option-b" {
		t.Fatalf("%+v", a)
	}
	// The last option is free text.
	key(m, "A")
	press(m, "down", "down", "enter")
	if m.input == nil {
		t.Fatal("no free text input")
	}
	typeText(m, "option A for now")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Text != "option A for now" || a.Answers != nil {
		t.Fatalf("%+v", a)
	}
	// Y/N do nothing for a question; with the approval first, Y approves.
	ov := m.ov
	ov.Inbox = []messaging.Message{ov.Inbox[1]}
	m.Update(overviewMsg{ov: ov})
	key(m, "Y")
	key(m, "enter")
	run(m, key(m, "y"))
	if a := lastSent(t, c); a.Kind != "approve" || a.ApprovalID != "ap-1" || a.Text != "approved by owner" {
		t.Fatalf("%+v", a)
	}
	key(m, "N")
	key(m, "enter")
	if m.input == nil || !strings.Contains(m.toast, "reason is required") {
		t.Fatal("empty rejection accepted")
	}
}

func TestInboxAnswersAndApprovals(t *testing.T) {
	m, c := demoModel(t, 120, 30)
	press(m, "5")
	key(m, "right")
	key(m, "enter")
	run(m, key(m, "y"))
	a := lastSent(t, c)
	if a.Kind != "answer" || a.MessageID != "m-ask-1" || a.Text != "option-b" || a.Answers["answer"] != "option-b" || a.Version != 1 {
		t.Fatalf("%+v", a)
	}
	key(m, "a")
	typeText(m, "option A for now")
	key(m, "enter")
	run(m, key(m, "y"))
	if a = lastSent(t, c); a.Text != "option A for now" || a.Answers != nil {
		t.Fatalf("%+v", a)
	}
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
	typeText(m, "not yet")
	key(m, "enter")
	run(m, key(m, "y"))
	if a = lastSent(t, c); a.Kind != "reject" || a.Text != "not yet" {
		t.Fatalf("%+v", a)
	}
}

func TestKillSwitch(t *testing.T) {
	m, c := demoModel(t, 120, 30)
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
	ov := m.ov
	ov.Pauses = append(ov.Pauses, storage.Pause{Scope: "company", Reason: "stop: incident", Actor: "owner"})
	m.Update(overviewMsg{ov: ov})
	m.toast = ""
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
	m, _ := demoModel(t, 100, 20)
	openDemoRun(m, "r-d1-7")
	base := len(m.run.events)
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
	ev := m.c.(*FixtureClient).F.RunEvents["r-d1-7"][1]
	ev.Seq = 100
	m.Update(streamMsg{Stream: "run:r-d1-7", Run: &ev})
	m.Render()
	if m.run.vp.YOffset() != offset || len(m.run.events) != base+1 {
		t.Fatal(m.run.vp.YOffset(), offset, len(m.run.events))
	}
	m.Update(streamMsg{Stream: "run:r-d1-7", Run: &ev})
	if len(m.run.events) != base+1 {
		t.Fatal(len(m.run.events))
	}
	key(m, "G")
	m.Render()
	if !m.run.follow || !m.run.vp.AtBottom() {
		t.Fatal("G did not follow")
	}
	m.Update(streamMsg{Stream: "run:r-d1-7", End: true, Outcome: "succeeded"})
	if !m.run.ended || !strings.Contains(ansi.Strip(m.Render()), "ended succeeded") {
		t.Fatal("end not shown")
	}
	m.Update(streamMsg{Stream: "run:other", Run: &ev})
	if len(m.run.events) != base+1 {
		t.Fatal("foreign event")
	}
}

func TestFeedDedupActivityAndToasts(t *testing.T) {
	m, _ := demoModel(t, 120, 30)
	n, acts := len(m.feed), len(m.activity)
	old := m.feed[0]
	m.Update(streamMsg{Stream: "feed", Feed: &old})
	if len(m.feed) != n || len(m.activity) != acts {
		t.Fatal("duplicate kept")
	}
	// A replayed run step is not added twice.
	ev := m.c.(*FixtureClient).F.RunEvents["r-d2-4"][0]
	m.Update(streamMsg{Stream: "tail:r-d2-4", Run: &ev})
	if len(m.activity) != acts {
		t.Fatal("duplicate step")
	}
	m.Update(streamMsg{Stream: "feed", State: true, Connected: false, Err: "EOF"})
	press(m, "4")
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "reconnecting EOF") {
		t.Fatal(s)
	}
	key(m, "k")
	if m.feedv.follow {
		t.Fatal("follow")
	}
	// A fresh attention event slides in as a toast (the model has been up for a while).
	m.startAt = DemoAt.Add(-time.Minute)
	item := storage.FeedItem{Sequence: 999, Type: "approval.requested", ProjectID: "project-2", TaskID: "P2-9", Agent: "developer-2", Actor: "developer-2", Summary: "brand new approval", CreatedAt: DemoAt, Attention: true}
	m.Update(streamMsg{Stream: "feed", Feed: &item})
	if len(m.toasts) != 1 {
		t.Fatal(m.toasts)
	}
	if s := ansi.Strip(m.Render()); strings.Contains(s, "brand new approval") {
		t.Fatal("the toast is shown before it slides in")
	}
	m.toasts[0].at = DemoAt.Add(-time.Second) // slid in
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "Attention") || !strings.Contains(s, "brand new approval") {
		t.Fatal(s)
	}
	key(m, "G")
	if !strings.Contains(ansi.Strip(m.Render()), "P2-9") {
		t.Fatal("newest not shown")
	}
}

func TestDashboardPanels(t *testing.T) {
	m, _ := demoModel(t, 140, 40)
	// The owner row: tasks that need the owner, company stats.
	if list := m.panelTasks(); len(list) != 6 || list[0].ID != "P2-1" {
		t.Fatalf("%+v", list)
	}
	// Select developer-1 (rows: owner, lead, developer-2, reviewer, pm, designer, developer-1).
	press(m, "j", "j", "j", "j", "j", "j")
	a, ok := m.selectedAgent()
	if !ok || a.Name != "developer-1" || a.run != "r-d1-7" {
		t.Fatalf("%+v", a)
	}
	s := ansi.Strip(m.Render())
	for _, want := range []string{"Stats · developer-1", "45,230", "12,891", "Context", "Tasks · developer-1", "[→] Task 3"} {
		if !strings.Contains(s, want) {
			t.Fatal(want, s)
		}
	}
	// enter on a running agent opens its run.
	run(m, key(m, "enter"))
	if m.tab != tabRun || m.run.id != "r-d1-7" {
		t.Fatal(m.tab)
	}
	key(m, "esc")
	// Focus moves between panels; z zooms the focused one.
	key(m, "l")
	if m.dash.focus != pTasks {
		t.Fatal(m.dash.focus)
	}
	key(m, "z")
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "Tasks · developer-1  ⤢") || strings.Contains(s, "Agent Tree") {
		t.Fatal(s)
	}
	key(m, "esc")
	// enter on a task opens its detail on the board.
	run(m, key(m, "enter"))
	if m.tab != tabBoard || !m.board.open || m.board.detailID != "P1-3" {
		t.Fatal(m.tab, m.board.detailID)
	}
}

func TestPalette(t *testing.T) {
	m, _ := demoModel(t, 120, 30)
	key(m, "ctrl+k")
	if m.palette == nil {
		t.Fatal("no palette")
	}
	typeText(m, "P3-2")
	list := m.palette.matches()
	if len(list) == 0 || list[0].label != "P3-2 Task 2" {
		t.Fatalf("%+v", list)
	}
	run(m, key(m, "enter"))
	if m.palette != nil || m.tab != tabBoard || m.board.detailID != "P3-2" {
		t.Fatal(m.tab, m.board.detailID)
	}
	key(m, "esc")
	key(m, "ctrl+k")
	typeText(m, "r-d2-4")
	run(m, key(m, "enter"))
	if m.tab != tabRun || m.run.id != "r-d2-4" {
		t.Fatal(m.tab, m.run.id)
	}
	if fuzzy("dv", "developer-1") == 0 || fuzzy("xyz", "project-1") != 0 || fuzzy("dev", "developer-1") <= fuzzy("dev", "undevised") {
		t.Fatal("fuzzy")
	}
}

func TestMouse(t *testing.T) {
	m, _ := demoModel(t, 140, 40)
	m.Render()
	// Click the Board tab (second line).
	for _, z := range m.zones {
		if z.y0 == 1 && z.x0 > 10 && z.x0 < 25 {
			m.Update(tea.MouseClickMsg{X: z.x0 + 1, Y: 1, Button: tea.MouseLeft})
			break
		}
	}
	if m.tab != tabBoard {
		t.Fatal(m.tab)
	}
	press(m, "1")
	m.Render()
	// Click the tree row of developer-1: body starts at y=2, the panel's first row at y=3.
	rows := m.treeRows()
	line := -1
	for l, r := range m.dash.treeMap {
		if rows[r].name == "developer-1" && line < 0 {
			line = l
		}
	}
	m.Update(tea.MouseClickMsg{X: 10, Y: 3 + line, Button: tea.MouseLeft})
	if a, ok := m.selectedAgent(); !ok || a.Name != "developer-1" || m.dash.focus != pTree {
		t.Fatalf("%+v %v", a, m.dash.focus)
	}
	// Wheel scrolls the focused list.
	next := ""
	for i, r := range rows {
		if r.name == "developer-1" {
			next = rows[i+1].name
		}
	}
	m.Update(tea.MouseWheelMsg{X: 10, Y: 6, Button: tea.MouseWheelDown})
	if a, _ := m.selectedAgent(); a.Name != next {
		t.Fatal(a.Name, next)
	}
	// A project chip.
	m.Render()
	chips := 0
	for _, z := range m.zones {
		if z.y0 == 0 {
			if chips++; chips == 2 { // all, then project-1
				m.Update(tea.MouseClickMsg{X: z.x0, Y: 0, Button: tea.MouseLeft})
				break
			}
		}
	}
	if m.project != "project-1" {
		t.Fatal(m.project)
	}
	// Board: a click focuses a card (second card of the Ready column), a second click opens it.
	m.project = ""
	press(m, "2")
	m.Render()
	var ready zone
	for _, z := range m.zones {
		if z.y0 > 2 && z.x0 > 0 && ready.x1 == 0 {
			ready = z
		}
	}
	m.Update(tea.MouseClickMsg{X: ready.x0 + 3, Y: ready.y0 + 7, Button: tea.MouseLeft})
	if tk, _ := m.board.selected(m); m.board.col != 1 || tk.ID != m.columns()[1].tasks[1].ID {
		t.Fatal(m.board.col, tk.ID)
	}
	m.Render()
	run(m, func() tea.Msg {
		_, cmd := m.Update(tea.MouseClickMsg{X: ready.x0 + 3, Y: ready.y0 + 7, Button: tea.MouseLeft})
		return cmd()
	})
	if !m.board.open {
		t.Fatal("second click did not open the card")
	}
	if !m.View().AltScreen || m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("view modes")
	}
}

func TestResizeThemeAndSmallTerminal(t *testing.T) {
	m, _ := demoModel(t, 80, 24)
	for _, tb := range []string{"1", "2", "3", "4", "5"} {
		press(m, tb)
		for _, size := range [][2]int{{60, 16}, {200, 50}, {80, 24}, {100, 30}} {
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			screen(t, m)
		}
	}
	openDemoRun(m, "r-rev-2")
	for _, size := range [][2]int{{60, 16}, {200, 50}} {
		m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		screen(t, m)
	}
	m.Update(tea.WindowSizeMsg{Width: 50, Height: 10})
	if !strings.HasPrefix(m.Render(), "terminal too small") {
		t.Fatal(m.Render())
	}
	// A light terminal switches the palette.
	dark := theme.text
	m.Update(tea.BackgroundColorMsg{Color: lightColor{}})
	if theme.text == dark || isDark {
		t.Fatal("theme not switched")
	}
	applyTheme(true)
}

type lightColor struct{}

func (lightColor) RGBA() (r, g, b, a uint32) { return 0xffff, 0xffff, 0xffff, 0xffff }

func TestQuitAndHelp(t *testing.T) {
	m, _ := demoModel(t, 100, 30)
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

// TestOverviewErrors: an unreachable Core shows in the header; the last good data stays.
func TestOverviewErrors(t *testing.T) {
	c := &HTTPClient{API: worker.API{URL: "http://127.0.0.1:1", Token: "secret-token-xyz"}}
	m := New(Options{Client: c, Now: func() time.Time { return DemoAt }, Location: time.UTC, NoStreams: true})
	defer m.Close()
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m.Update(overviewMsg{err: errors.New("dial tcp 127.0.0.1:1: connection refused")})
	s := ansi.Strip(m.Render())
	if !strings.Contains(s, "unreachable") || !strings.Contains(s, "connection refused") {
		t.Fatal(s)
	}
	m.Update(overviewMsg{ov: DemoFixture().Overview})
	m.Update(overviewMsg{err: errors.New("timeout")})
	s = ansi.Strip(m.Render())
	if !strings.Contains(s, "retrying") || !strings.Contains(s, "developer-1") {
		t.Fatal(s)
	}
	for _, tb := range []string{"1", "2", "3", "4", "5", "?"} {
		press(m, tb)
		if strings.Contains(m.Render(), "secret-token-xyz") {
			t.Fatal("token on screen")
		}
		press(m, "esc")
	}
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
	if got != 9 {
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
