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
// live runs' steps known. It starts no streams: tests drive it with messages.
func demoModel(t *testing.T, w, h int) (*Model, *FixtureClient) {
	t.Helper()
	applyTheme(true)
	f := DemoFixture()
	c := &FixtureClient{F: f}
	m := New(Options{Client: c, Now: func() time.Time { return DemoAt }, Location: time.UTC, NoStreams: true, Local: LocalTranscripts{Off: true}, Notify: NotifyOff})
	t.Cleanup(m.Close)
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	// The overview's follow-up reads (tool error counts, the owner conversations) run at once.
	_, cmd := m.Update(overviewMsg{ov: f.Overview})
	run(m, cmd)
	m.Update(streamMsg{Stream: "feed", State: true, Connected: true})
	for i := range f.Feed {
		m.Update(streamMsg{Stream: "feed", Feed: &f.Feed[i]})
	}
	for _, id := range []string{"r-d1-7", "r-rev-2", "r-d2-4", "r-lead-c1"} {
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
	case "ctrl+k":
		return tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
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
		{name: "dashboard_140x40", w: 140, h: 40},
		{name: "dashboard_100x30", w: 100, h: 30},
		{name: "dashboard_80x24", w: 80, h: 24},
		{name: "dashboard_agent_140x40", w: 140, h: 40, keys: []string{"j", "j", "j", "j", "j", "j"}},
		{name: "dashboard_zoom_tree_100x30", w: 100, h: 30, keys: []string{"z"}},
		{name: "board_160x40", w: 160, h: 40, keys: []string{"2"}},
		{name: "board_100x30", w: 100, h: 30, keys: []string{"2", "right", "right"}},
		{name: "board_80x24", w: 80, h: 24, keys: []string{"2"}},
		{name: "board_project_100x30", w: 100, h: 30, keys: []string{"2", "]"}},
		{name: "task_detail_120x40", w: 120, h: 40, prep: func(m *Model) { m.Update(m.openDetail("P1-3")()) }},
		{name: "confirm_pause_100x30", w: 100, h: 30, keys: []string{"2", "right", "right", "p"}},
		{name: "input_directive_100x30", w: 100, h: 30, keys: []string{"2", "right", "right", "d"}, prep: func(m *Model) { typeText(m, "!ship the grams-only picker today") }},
		{name: "run_140x40", w: 140, h: 40, prep: func(m *Model) { openDemoRun(m, "r-d1-7") }},
		{name: "run_codex_100x30", w: 100, h: 30, prep: func(m *Model) { openDemoRun(m, "r-d2-4") }},
		{name: "flow_lanes_140x40", w: 140, h: 40, keys: []string{"3"}},
		{name: "flow_lanes_project_100x30", w: 100, h: 30, keys: []string{"3", "]"}},
		{name: "flow_pairs_100x30", w: 100, h: 30, keys: []string{"3", "g"}},
		{name: "flow_timeline_80x24", w: 80, h: 24, keys: []string{"3"}},
		{name: "feed_120x30", w: 120, h: 30, keys: []string{"4"}},
		{name: "feed_attention_80x24", w: 80, h: 24, keys: []string{"4", "!"}},
		{name: "inbox_120x30", w: 120, h: 30, keys: []string{"5"}},
		{name: "inbox_approval_80x24", w: 80, h: 24, keys: []string{"5", "j"}},
		{name: "prompt_chooser_100x30", w: 100, h: 30, keys: []string{"A"}},
		{name: "palette_100x30", w: 100, h: 30, keys: []string{"ctrl+k"}, prep: func(m *Model) { typeText(m, "cal3") }},
		{name: "help_100x30", w: 100, h: 30, keys: []string{"?"}},
		// Chat: the direct reports and a conversation; lead is replying in project-2; typing a message.
		{name: "chat_100x30", w: 100, h: 30, keys: []string{"6"}},
		{name: "chat_80x24", w: 80, h: 24, keys: []string{"6"}},
		{name: "chat_replying_160x40", w: 160, h: 40, keys: []string{"6", "]", "]", "j"}},
		{name: "chat_typing_100x30", w: 100, h: 30, keys: []string{"6", "enter"}, prep: func(m *Model) { typeText(m, "@lead cut P2-2 from 1.2 if the rebase fails") }},
		// The session drill-down: Claude and Codex transcripts, everything expanded, errors only.
		{name: "transcript_140x40", w: 140, h: 40, prep: func(m *Model) { openDemoTranscript(m, "r-d1-7") }},
		{name: "transcript_expanded_100x30", w: 100, h: 30, prep: func(m *Model) { openDemoTranscript(m, "r-d1-7"); press(m, "E", "g", "j", "j") }},
		{name: "transcript_errors_80x24", w: 80, h: 24, prep: func(m *Model) { openDemoTranscript(m, "r-d1-7"); press(m, "e") }},
		{name: "transcript_codex_160x40", w: 160, h: 40, prep: func(m *Model) { openDemoTranscript(m, "r-d2-4"); press(m, "k", "k", "enter") }},
		{name: "run_errors_100x30", w: 100, h: 30, prep: func(m *Model) { openDemoRun(m, "r-d1-7"); press(m, "e") }},
		// External links: a task's pull request; an artifact approval and its preview.
		{name: "task_detail_pr_120x40", w: 120, h: 40, prep: func(m *Model) { run(m, m.openDetail("P2-1")) }},
		{name: "inbox_artifact_120x30", w: 120, h: 30, keys: []string{"5", "j", "j"}},
		{name: "preview_100x30", w: 100, h: 30, keys: []string{"5", "j", "j"}, prep: func(m *Model) { run(m, key(m, "v")) }},
		{name: "preview_80x24", w: 80, h: 24, keys: []string{"5", "j", "j"}, prep: func(m *Model) { run(m, key(m, "v")) }},
		// The create task form: empty, refused fields, filled in.
		{name: "form_100x30", w: 100, h: 30, keys: []string{"2", "n"}},
		{name: "form_errors_80x24", w: 80, h: 24, keys: []string{"2", "n", "ctrl+s"}},
		{name: "form_filled_160x40", w: 160, h: 40, keys: []string{"2", "]", "n"}, prep: fillDemoForm},
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

// openDemoTranscript opens a run's transcript and delivers its load as the stream would.
func openDemoTranscript(m *Model, id string) {
	run(m, m.openTranscript(id))
	if m.tx.run != nil {
		run(m, m.loadLocal(id, *m.tx.run)) // the follow-up of the load
	}
}

// fillDemoForm types a task into the open form: title, description, developer-1, priority 7, P1-3.
func fillDemoForm(m *Model) {
	typeText(m, "Export items as CSV")
	press(m, "tab")
	typeText(m, "One row per item.")
	press(m, "tab")
	for m.form.assigneeName() != "developer-1" {
		press(m, "right")
	}
	press(m, "tab")
	press(m, "backspace")
	typeText(m, "7")
	press(m, "tab")
	for m.form.depAt < len(m.depCandidates(m.form)) && m.depCandidates(m.form)[m.form.depAt].ID != "P1-3" {
		press(m, "right")
	}
	press(m, "space", "tab")
}
