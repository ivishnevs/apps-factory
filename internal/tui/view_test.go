package tui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

var update = flag.Bool("update", false, "rewrite the golden screens")

// demoModel is the UI on the demo fixture at a fixed moment, sized w×h, with the feed received and the
// live runs' last activity known. It starts no streams: tests drive it with messages.
func demoModel(t *testing.T, w, h int) (*Model, *FixtureClient) {
	t.Helper()
	f := DemoFixture()
	c := &FixtureClient{F: f}
	m := New(Options{Client: c, Now: func() time.Time { return DemoAt }, Location: time.UTC, NoStreams: true})
	t.Cleanup(m.Close)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.Update(overviewMsg{ov: f.Overview})
	m.Update(streamMsg{Stream: "feed", State: true, Connected: true})
	for i := range f.Feed {
		m.Update(streamMsg{Stream: "feed", Feed: &f.Feed[i]})
	}
	for _, id := range []string{"r-ios-7", "r-rev-2"} {
		events := f.RunEvents[id]
		for i := range events {
			m.Update(streamMsg{Stream: "tail:" + id, Run: &events[i]})
		}
	}
	return m, c
}

// press sends keys; each key is a name as tea.KeyPressMsg.String reports it.
func press(m *Model, keys ...string) {
	for _, k := range keys {
		m.Update(keyMsg(k))
	}
}

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	r := []rune(k)
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

// typeText types text into the focused input.
func typeText(m *Model, s string) {
	for _, r := range s {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

// screen is the rendered screen without styles; every line is exactly the width.
func screen(t *testing.T, m *Model) string {
	t.Helper()
	out := ansi.Strip(m.Render())
	lines := strings.Split(out, "\n")
	if len(lines) != m.height {
		t.Fatalf("screen has %d lines, want %d", len(lines), m.height)
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != m.width {
			t.Fatalf("line %d is %d wide, want %d: %q", i, w, m.width, l)
		}
	}
	return out
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	// Trailing spaces are kept in the screen but trimmed in the file, so the goldens stay readable.
	trimmed := []string{}
	for _, l := range strings.Split(got, "\n") {
		trimmed = append(trimmed, strings.TrimRight(l, " "))
	}
	got = strings.Join(trimmed, "\n") + "\n"
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test ./internal/tui -update)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs (go test ./internal/tui -update to accept):\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func TestGoldenScreens(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
		keys []string
		prep func(m *Model)
	}{
		{name: "dashboard_80x24", w: 80, h: 24},
		{name: "dashboard_120x32", w: 120, h: 32},
		{name: "tasks_80x24", w: 80, h: 24, keys: []string{"2"}},
		{name: "tasks_filter_80x24", w: 80, h: 24, keys: []string{"2", "/"}, prep: func(m *Model) { typeText(m, "p:calorie open"); press(m, "enter") }},
		{name: "task_detail_80x40", w: 80, h: 40, keys: []string{"2"}, prep: func(m *Model) {
			m.tasks.cursor = indexOf(m.visibleTasks(), "CAL-3")
			cmd := m.tasks.key(m, keyMsg("enter"))
			m.Update(cmd())
		}},
		{name: "tasks_wide_160x30", w: 160, h: 30, keys: []string{"2"}, prep: func(m *Model) {
			m.tasks.cursor = indexOf(m.visibleTasks(), "CAL-3")
			cmd := m.tasks.key(m, keyMsg("enter"))
			m.Update(cmd())
		}},
		{name: "confirm_pause_80x24", w: 80, h: 24, keys: []string{"2", "p"}},
		{name: "input_directive_80x24", w: 80, h: 24, keys: []string{"2", "d"}, prep: func(m *Model) { typeText(m, "!ship the grams-only picker today") }},
		{name: "run_80x24", w: 80, h: 24, prep: func(m *Model) { openDemoRun(m, "r-ios-7") }},
		{name: "run_picker_80x24", w: 80, h: 24, keys: []string{"3"}},
		{name: "flow_80x24", w: 80, h: 24, keys: []string{"4"}},
		{name: "flow_task_80x24", w: 80, h: 24, keys: []string{"4"}, prep: func(m *Model) { m.flow.filter = FlowFilter{Task: "N-1"} }},
		{name: "flow_graph_100x30", w: 100, h: 30, keys: []string{"4", "g"}},
		{name: "feed_80x24", w: 80, h: 24, keys: []string{"5"}},
		{name: "feed_attention_80x24", w: 80, h: 24, keys: []string{"5", "!"}},
		{name: "inbox_80x24", w: 80, h: 24, keys: []string{"6"}},
		{name: "inbox_approval_80x24", w: 80, h: 24, keys: []string{"6", "j"}},
		{name: "help_80x24", w: 80, h: 24, keys: []string{"?"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := demoModel(t, tc.w, tc.h)
			press(m, tc.keys...)
			if tc.prep != nil {
				tc.prep(m)
			}
			golden(t, tc.name, screen(t, m))
		})
	}
}

func indexOf(list []Task, id string) int {
	for i, t := range list {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// openDemoRun opens a run and delivers its fixture events and run record as the stream and load would.
func openDemoRun(m *Model, id string) {
	m.openRun(id)
	f := m.c.(*FixtureClient).F
	m.Update(streamMsg{Stream: "run:" + id, State: true, Connected: true})
	for i := range f.RunEvents[id] {
		m.Update(streamMsg{Stream: "run:" + id, Run: &f.RunEvents[id][i]})
	}
	m.Update(runMsg{id: id, run: f.Runs[id], calls: f.ToolCalls[id]})
}
