package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/storage"
)

// txState is the session drill-down (t on a run): the run's full runtime transcript, every tool call with its
// full input and result (collapsible), errors highlighted, live-tailed while the run is running.
type txState struct {
	id      string
	back    tab
	run     *storage.Run
	runErr  error
	events  []RunEvent
	lastSeq int64
	calls   []storage.ToolCall
	ended   bool
	cancel  context.CancelFunc
	// core are the entries built from Core's events; local those of the local session file.
	core      []TxEntry
	local     []TxEntry
	localPath string
	localErr  error
	localTry  bool
	useLocal  bool
	dirty     bool
	// stale: the events or calls changed since core was built.
	stale bool
	// open marks expanded entries (by their index in the source's entries); all expands every entry.
	open       map[int]bool
	all        bool
	errorsOnly bool
	cursor     int // index into the visible entries
	offset     int // first line shown
	follow     bool
	// rendered lines and the first line of each visible entry, for the width they were built for.
	lines  []string
	starts []int
	vis    []int
	width  int
}

type (
	txLoadMsg struct {
		id     string
		run    storage.Run
		calls  []storage.ToolCall
		events []RunEvent
		err    error
	}
	txLocalMsg struct {
		id      string
		path    string
		entries []TxEntry
		err     error
	}
	txTickMsg struct{ id string }
)

// openTranscript shows a run's full transcript; esc goes back to the view it was opened from.
func (m *Model) openTranscript(id string) tea.Cmd {
	t := &m.tx
	if t.cancel != nil {
		t.cancel()
	}
	back := m.tab
	if back == tabTranscript {
		back = t.back
	}
	*t = txState{id: id, back: back, open: map[int]bool{}, follow: true, dirty: true, stale: true}
	m.tab = tabTranscript
	cmds := []tea.Cmd{m.loadTx(id, m.opts.NoStreams)}
	if !m.opts.NoStreams {
		ctx, cancel := context.WithCancel(m.ctx)
		t.cancel = cancel
		cmds = append(cmds, m.stream(func(ctx context.Context, out func(StreamEvent)) {
			m.c.StreamRun(ctx, id, 0, 0, func(e StreamEvent) {
				e.Stream = "tx:" + id
				out(e)
			})
		}, ctx), tea.Tick(runPoll, func(time.Time) tea.Msg { return txTickMsg{id} }))
	}
	return tea.Batch(cmds...)
}

// loadTx reads the run and Core's tool call records (and, without streams, the stored events).
func (m *Model) loadTx(id string, events bool) tea.Cmd {
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		r, err := c.Run(rctx, id)
		if err != nil {
			return txLoadMsg{id: id, err: err}
		}
		calls, _ := c.ToolCalls(rctx, id)
		msg := txLoadMsg{id: id, run: r, calls: calls}
		if events {
			msg.events, msg.err = c.RunEvents(rctx, id)
		}
		return msg
	}
}

// loadLocal reads the run's local session file (Claude or Codex) when this machine has it.
func (m *Model) loadLocal(id string, run storage.Run) tea.Cmd {
	lt := m.opts.Local
	return func() tea.Msg {
		path, provider, err := lt.Find(string(run.Spec.Provider), run.SessionID)
		if err != nil {
			return txLocalMsg{id: id, err: err}
		}
		entries, err := lt.Read(path, provider)
		return txLocalMsg{id: id, path: path, entries: entries, err: err}
	}
}

func (m *Model) onTxLoad(msg txLoadMsg) tea.Cmd {
	t := &m.tx
	if msg.id != t.id {
		return nil
	}
	if msg.err != nil {
		t.runErr = msg.err
		return nil
	}
	run := msg.run
	t.run, t.runErr, t.calls, t.dirty, t.stale = &run, nil, msg.calls, true, true
	if run.Status == "ENDED" && t.cancel == nil {
		t.ended = true
	}
	for _, e := range msg.events {
		if e.Seq > t.lastSeq {
			t.lastSeq = e.Seq
			t.events = append(t.events, e)
		}
	}
	if !t.localTry || !t.ended {
		t.localTry = true
		return m.loadLocal(t.id, run)
	}
	return nil
}

func (m *Model) onTxLocal(msg txLocalMsg) {
	t := &m.tx
	if msg.id != t.id {
		return
	}
	t.localErr = msg.err
	if msg.err == nil {
		t.local, t.localPath, t.dirty = msg.entries, msg.path, true
		// Core keeps no events of this run (an old run, a purged log): the local file is the source.
		if len(t.events) == 0 && t.run != nil {
			t.useLocal = true
		}
	}
}

func (m *Model) onTxTick(msg txTickMsg) tea.Cmd {
	t := &m.tx
	if msg.id != t.id || t.ended {
		return nil
	}
	return tea.Batch(m.loadTx(t.id, false), tea.Tick(runPoll, func(time.Time) tea.Msg { return txTickMsg{msg.id} }))
}

func (t *txState) onStream(m *Model, e StreamEvent) tea.Cmd {
	if e.Stream != "tx:"+t.id {
		return nil
	}
	switch {
	case e.Run != nil:
		if e.Run.Seq > t.lastSeq {
			t.lastSeq = e.Run.Seq
			t.events = append(t.events, *e.Run)
			t.dirty, t.stale = true, true
		}
	case e.End:
		t.ended, t.dirty = true, true
		return m.loadTx(t.id, false)
	}
	return nil
}

// entries are the entries of the current source.
func (t *txState) entries() []TxEntry {
	if t.useLocal {
		return t.local
	}
	return t.core
}

func (t *txState) visible() []int {
	out := []int{}
	for i, e := range t.entries() {
		if t.errorsOnly && !txIsProblem(e) {
			continue
		}
		out = append(out, i)
	}
	return out
}

// txIsProblem: an entry the errors-only filter keeps.
func txIsProblem(e TxEntry) bool {
	return e.Kind == txRefusal || e.Kind == txError || e.Kind == txTool && e.IsError
}

// sync rebuilds the Core entries after new events or calls.
func (t *txState) sync(m *Model) {
	if !t.stale {
		return
	}
	t.core, t.stale, t.dirty = transcriptFromEvents(t.events, t.calls), false, true
	if t.id != "" {
		m.noteErrors(t.id, tallyErrors(t.core))
	}
}

func (t *txState) key(m *Model, key string) tea.Cmd {
	t.sync(m)
	vis := t.visible()
	if t.follow {
		t.cursor = max(len(vis)-1, 0)
	}
	page := max(m.height-8, 3)
	move := func(to int) {
		t.cursor = min(max(to, 0), max(len(vis)-1, 0))
		t.follow = t.cursor == len(vis)-1
	}
	switch key {
	case "esc", "backspace":
		if t.cancel != nil {
			t.cancel()
		}
		back := t.back
		*t = txState{}
		m.tab = back
		return nil
	case "j", "down":
		move(t.cursor + 1)
	case "k", "up":
		move(t.cursor - 1)
	case "pgdown", "space", "ctrl+d":
		t.offset += page
		t.follow = false
		t.cursorAtOffset()
	case "pgup", "ctrl+u":
		t.offset = max(t.offset-page, 0)
		t.follow = false
		t.cursorAtOffset()
	case "g", "home":
		move(0)
	case "G", "end":
		move(len(vis) - 1)
		t.follow = true
	case "enter", "o":
		if t.cursor < len(vis) {
			i := vis[t.cursor]
			t.open[i] = !t.isOpen(i)
			t.dirty = true
		}
	case "E":
		t.all = !t.all
		t.open = map[int]bool{}
		t.dirty = true
	case "e":
		cur := -1
		if t.cursor < len(vis) {
			cur = vis[t.cursor]
		}
		t.errorsOnly = !t.errorsOnly
		t.dirty = true
		nv := t.visible()
		t.cursor = max(len(nv)-1, 0)
		for j, i := range nv {
			if i >= cur {
				t.cursor = j
				break
			}
		}
	case "f":
		t.follow = !t.follow
		if t.follow {
			t.cursor = max(len(vis)-1, 0)
		}
	case "l":
		if len(t.local) == 0 {
			m.setToast("no local transcript of this run on this machine", true)
			return nil
		}
		t.useLocal = !t.useLocal
		t.open, t.cursor, t.offset, t.dirty = map[int]bool{}, 0, 0, true
	case "T":
		if t.run != nil && t.run.Spec.TaskID != "" {
			return m.openDetail(t.run.Spec.TaskID)
		}
	}
	return nil
}

// cursorAtOffset puts the cursor on the first entry starting at or after the first line shown.
func (t *txState) cursorAtOffset() {
	for j, s := range t.starts {
		if s >= t.offset {
			t.cursor = j
			return
		}
	}
}

func (t *txState) isOpen(i int) bool {
	if v, ok := t.open[i]; ok {
		return v
	}
	return t.all
}

// txLines renders the transcript screen: a header with the source and the error count, the entries, and the
// last error at the bottom.
func (m *Model) txLines(w, h int) []string {
	t := &m.tx
	t.sync(m)
	agent, task := "", ""
	if t.run != nil {
		agent, task = t.run.Spec.Agent, t.run.Spec.TaskID
	}
	entries := t.entries()
	inner := w - 4
	if t.dirty || t.width != inner {
		t.render(m, agent, entries, inner)
		t.dirty, t.width = false, inner
	}
	vis := t.vis
	if t.cursor >= len(vis) {
		t.cursor = max(len(vis)-1, 0)
	}
	if t.follow {
		t.cursor = max(len(vis)-1, 0)
	}
	bodyH := max(h-4, 1)
	// Keep the cursor's entry in view: its first line, and as much of it as fits.
	if len(t.starts) > 0 {
		s := t.starts[t.cursor]
		end := len(t.lines)
		if t.cursor+1 < len(t.starts) {
			end = t.starts[t.cursor+1]
		}
		if t.follow {
			t.offset = max(len(t.lines)-bodyH, 0)
		} else {
			if s < t.offset {
				t.offset = s
			}
			if end > t.offset+bodyH {
				t.offset = min(s, end-bodyH)
			}
		}
	}
	t.offset = min(max(t.offset, 0), max(len(t.lines)-bodyH, 0))
	body := []string{}
	cur := -1
	if len(t.starts) > 0 {
		cur = t.cursor
	}
	for i := t.offset; i < len(t.lines) && len(body) < bodyH; i++ {
		mark := "  "
		if cur >= 0 && i >= t.starts[cur] && (cur+1 >= len(t.starts) || i < t.starts[cur+1]) {
			mark = stAccent.Render("▌ ")
		}
		body = append(body, mark+t.lines[i])
	}
	if len(entries) == 0 {
		switch {
		case t.runErr != nil:
			body = []string{stRed.Render("  " + t.runErr.Error())}
		case t.run == nil:
			body = []string{"  " + m.spin.View() + " loading the transcript…"}
		default:
			body = []string{stFaint.Render("  no transcript entries yet")}
		}
	} else if len(vis) == 0 {
		body = []string{stGreen.Render("  ✓ no tool errors or refusals in this run")}
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	// Header: run, source badge, error count, filter.
	state := stGreen.Render("● live")
	if t.ended || t.run != nil && t.run.Status == "ENDED" {
		out := ""
		if t.run != nil {
			out = t.run.Outcome
		}
		state = stYellow.Render("■ ended " + out)
	}
	src := lipgloss.NewStyle().Background(theme.surfaceHi).Foreground(theme.cyan).Render(" core events ")
	if t.useLocal {
		src = lipgloss.NewStyle().Background(theme.cyan).Foreground(theme.onAccent).Bold(true).Render(" local transcript ")
	}
	local := ""
	switch {
	case len(t.local) > 0 && !t.useLocal:
		local = stFaint.Render(" · l local transcript")
	case len(t.local) > 0:
		local = stFaint.Render(" · l core events")
	case t.localTry && t.localErr != nil:
		local = stDim.Render(" · local: not available here")
	}
	tally := tallyErrors(entries)
	title := lipgloss.NewStyle().Foreground(agentColor(agent)).Bold(true).Render(dash(agent)) + stFaint.Render(" · "+t.id+" · "+dash(task)+" · ") + state
	info := src + local
	if tally.N > 0 {
		info += "  " + errBadge(tally.N)
	}
	if t.errorsOnly {
		info += "  " + lipgloss.NewStyle().Background(theme.red).Foreground(theme.onAccent).Bold(true).Render(" errors only ")
	}
	follow := stGreen.Render("follow")
	if !t.follow {
		follow = stYellow.Render("paused · G")
	}
	head := rightAlign(" "+info, follow+" ", inner)
	last := stFaint.Render(fmt.Sprintf(" %d entries", len(entries)))
	if tally.N > 0 {
		last = stRedBold.Render(" ✗ last error ") + stFaint.Render(m.clock(tally.At)+" ") + stRed.Render(cut(tally.Last, inner-26))
	}
	lines := append([]string{head, rule(inner)}, body...)
	lines = append(lines[:min(len(lines), h-3)], last)
	return frame(title, lines, w, h, theme.accent, true, false)
}

// render builds the lines of the visible entries for width w.
func (t *txState) render(m *Model, agent string, entries []TxEntry, w int) {
	t.vis = t.visible()
	t.lines, t.starts = []string{}, []int{}
	ac := agentColor(agent)
	cw := w - 2 // the cursor mark
	for _, i := range t.vis {
		e := entries[i]
		t.starts = append(t.starts, len(t.lines))
		open := t.isOpen(i)
		clock := stDim.Render(m.clock(e.At))
		switch e.Kind {
		case txSay:
			bw := min(cw, 90)
			body := wrap(e.Text, bw-4)
			for _, l := range frame(lipgloss.NewStyle().Foreground(ac).Bold(true).Render(dash(agent))+" "+clock, body, bw, len(body)+2, ac, false, false) {
				t.lines = append(t.lines, l)
			}
		case txThink:
			text := wrap(e.Text, cw-4)
			more := ""
			if !open && len(text) > 2 {
				more = stDim.Render(fmt.Sprintf("  … %d more lines · enter", len(text)-2))
				text = text[:2]
			}
			t.lines = append(t.lines, rightAlign(stMagenta.Render("∴ reasoning")+more, clock, cw))
			for _, l := range text {
				t.lines = append(t.lines, stFaint.Italic(true).Render("  "+l))
			}
		case txTool:
			icon, st := "✎", stFaint
			switch {
			case isFactoryTool(e.Tool):
				icon, st = "⇄", stMagenta
			case e.Tool == "Bash":
				icon, st = "❯", stCyan
			}
			status := stGreen.Render("✓")
			switch {
			case e.IsError:
				status, st = stRedBold.Render("✗ error"), stRedBold
			case !e.Done:
				status = stYellow.Render(m.spin.View() + " running")
			}
			fold := stDim.Render("▸")
			if open {
				fold = stDim.Render("▾")
			}
			brief := e.Brief
			if brief == "" {
				brief = oneLine(e.Input)
			}
			left := fold + " " + st.Render(icon+" "+e.Tool) + "  " + stFaint.Render(cut(brief, max(cw-40, 10)))
			t.lines = append(t.lines, rightAlign(left, status+" "+clock, cw))
			if !open {
				if e.IsError && e.Result != "" {
					t.lines = append(t.lines, "    "+stRed.Render(cut(firstLine(e.Result), cw-6)))
				}
				continue
			}
			if e.Input != "" {
				t.lines = append(t.lines, "    "+stDim.Render("input"))
				for _, l := range wrap(e.Input, cw-8) {
					t.lines = append(t.lines, "    "+stDim.Render("│ ")+stCode.Render(l))
				}
			}
			if e.Done {
				label, ls := "result", stDim
				if e.IsError {
					label, ls = "error", stRedBold
				}
				lines := wrap(e.Result, cw-8)
				t.lines = append(t.lines, "    "+ls.Render(fmt.Sprintf("%s · %s", label, plural(len(lines), "line"))))
				for _, l := range lines {
					style := lipgloss.NewStyle().Foreground(theme.text)
					if e.IsError {
						style = stRed
					}
					t.lines = append(t.lines, "    "+stDim.Render("│ ")+style.Render(l))
				}
			}
		case txRefusal:
			t.lines = append(t.lines, rightAlign(stRedBold.Render("✗ "+e.Text), clock, cw))
		case txError:
			t.lines = append(t.lines, rightAlign(stRedBold.Render("⚠ "+e.Text), clock, cw))
		case txEnd:
			t.lines = append(t.lines, lipgloss.NewStyle().Background(theme.yellow).Foreground(theme.onAccent).Bold(true).Render(" ■ "+e.Text+" ")+" "+clock)
		default:
			t.lines = append(t.lines, rightAlign(stDim.Render("── "+e.Text+" ──"), clock, cw))
		}
	}
}

// errBadge is the tool error count: ✗3.
func errBadge(n int) string {
	if n <= 0 {
		return ""
	}
	return stRedBold.Render(fmt.Sprintf("✗%d", n))
}

// plural is "1 line", "3 lines".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}
