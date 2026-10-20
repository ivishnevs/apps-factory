package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/storage"
)

// runState is the live run view: one run's transcript, streamed, with follow mode.
type runState struct {
	id      string
	run     *storage.Run
	runErr  error
	events  []RunEvent
	lastSeq int64
	calls   []storage.ToolCall
	lines   []LogLine
	ctx     ctxUsage // the last model call's context, from events
	dirty   bool
	ended   bool
	outcome string
	follow  bool
	st      streamState
	cancel  context.CancelFunc
	vp      viewport.Model
	ready   bool
	width   int
	// pick is the cursor of the live run picker shown without a run.
	pick int
	// errorsOnly shows only tool errors and refusals.
	errorsOnly bool
}

// runPoll is how often the live run view reloads the run and its tool calls.
const runPoll = 2 * time.Second

// openRun shows a run in the live run view and starts streaming its events.
func (m *Model) openRun(id string) tea.Cmd {
	r := &m.run
	if r.cancel != nil {
		r.cancel()
	}
	*r = runState{id: id, follow: true, dirty: true}
	if m.tab != tabRun {
		m.prevTab, m.tab = m.tab, tabRun
	}
	cmds := []tea.Cmd{m.loadRun(id)}
	if !m.opts.NoStreams {
		ctx, cancel := context.WithCancel(m.ctx)
		r.cancel = cancel
		cmds = append(cmds, m.stream(func(ctx context.Context, out func(StreamEvent)) {
			m.c.StreamRun(ctx, id, 0, 0, out)
		}, ctx), tea.Tick(runPoll, func(time.Time) tea.Msg { return runTickMsg{id} }))
	}
	return tea.Batch(cmds...)
}

func (m *Model) loadRun(id string) tea.Cmd {
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		r, err := c.Run(rctx, id)
		if err != nil {
			return runMsg{id: id, err: err}
		}
		calls, _ := c.ToolCalls(rctx, id)
		return runMsg{id: id, run: r, calls: calls}
	}
}

func (m *Model) onRun(msg runMsg) tea.Cmd {
	r := &m.run
	if msg.id != r.id {
		return nil
	}
	if msg.err != nil {
		r.runErr = msg.err
		return nil
	}
	run := msg.run
	r.run, r.runErr = &run, nil
	if len(msg.calls) != len(r.calls) {
		r.calls, r.dirty = msg.calls, true
	}
	if run.Status == "ENDED" && r.cancel == nil {
		r.ended, r.outcome = true, run.Outcome
	}
	return nil
}

func (m *Model) onRunTick(msg runTickMsg) tea.Cmd {
	r := &m.run
	if msg.id != r.id || r.ended {
		return nil
	}
	return tea.Batch(m.loadRun(r.id), tea.Tick(runPoll, func(time.Time) tea.Msg { return runTickMsg{msg.id} }))
}

func (r *runState) onStream(m *Model, e StreamEvent) tea.Cmd {
	if e.Stream != "run:"+r.id {
		return nil
	}
	switch {
	case e.State:
		r.st = streamState{connected: e.Connected, err: e.Err, seen: true}
	case e.Run != nil:
		if e.Run.Seq > r.lastSeq {
			r.lastSeq = e.Run.Seq
			r.events = append(r.events, *e.Run)
			r.dirty = true
		}
	case e.End:
		r.ended, r.outcome, r.dirty = true, e.Outcome, true
		r.st = streamState{seen: true}
		return m.loadRun(r.id)
	}
	return nil
}

func (r *runState) key(m *Model, key string) tea.Cmd {
	if r.id == "" {
		runs := m.liveRuns()
		switch key {
		case "j", "down":
			r.pick = min(r.pick+1, max(len(runs)-1, 0))
		case "k", "up":
			r.pick = max(r.pick-1, 0)
		case "enter":
			if r.pick < len(runs) {
				return m.openRun(runs[r.pick].ID)
			}
		case "t":
			if r.pick < len(runs) {
				return m.openTranscript(runs[r.pick].ID)
			}
		case "esc":
			return m.switchTab(m.prevTab)
		}
		return nil
	}
	switch key {
	case "f":
		r.follow = !r.follow
		if r.follow {
			r.vp.GotoBottom()
		}
	case "j", "down":
		r.vp.ScrollDown(1)
		r.follow = r.vp.AtBottom()
	case "k", "up":
		r.vp.ScrollUp(1)
		r.follow = false
	case "pgdown", "space":
		r.vp.PageDown()
		r.follow = r.vp.AtBottom()
	case "pgup":
		r.vp.PageUp()
		r.follow = false
	case "g", "home":
		r.vp.GotoTop()
		r.follow = false
	case "G", "end":
		r.vp.GotoBottom()
		r.follow = true
	case "esc":
		if r.cancel != nil {
			r.cancel()
		}
		prev := m.prevTab
		*r = runState{follow: true}
		m.tab = prev
		return nil
	case "t":
		return m.openTranscript(r.id)
	case "e":
		r.errorsOnly, r.dirty = !r.errorsOnly, true
	case "T":
		if r.run != nil && r.run.Spec.TaskID != "" {
			return m.openDetail(r.run.Spec.TaskID)
		}
	}
	return nil
}

// liveRun is a live run of the status, with its project.
type liveRun struct {
	ID, Agent, TaskID, Status, Project string
	StartedAt                          int64
}

func (m *Model) liveRuns() []liveRun {
	out := []liveRun{}
	for _, p := range m.ov.Status {
		for _, r := range p.Runs {
			out = append(out, liveRun{ID: r.ID, Agent: r.Agent, TaskID: r.TaskID, Status: r.Status, Project: p.Project.ID, StartedAt: r.StartedAt})
		}
	}
	return out
}

// transcript renders the run's activity like a chat: the agent's messages as bubbles, tool calls as
// compact rows (shell commands as code), refusals in red, the exit highlighted.
func (m *Model) transcript(agent string, lines []LogLine, w int) []string {
	out := []string{}
	ac := agentColor(agent)
	for _, l := range lines {
		t := stDim.Render(l.At.In(m.opts.Location).Format("15:04:05"))
		switch l.Kind {
		case lineSay:
			bw := min(w-2, 76)
			body := wrap(l.Arg, bw-4)
			title := lipgloss.NewStyle().Foreground(ac).Bold(true).Render(agent) + " " + t
			out = append(out, "")
			for _, bl := range frame(title, body, bw, len(body)+2, ac, false, false) {
				out = append(out, " "+bl)
			}
		case lineShell:
			out = append(out, rightAlign("   "+stCyan.Render("▸ ")+stCode.Render(" "+cut(l.Arg, w-18)+" "), t, w))
		case lineMCP:
			out = append(out, rightAlign("   "+stMagenta.Render("⇄ "+l.Tool)+"  "+stFaint.Render(l.Arg), t, w))
		case lineExit:
			bar := lipgloss.NewStyle().Background(theme.green).Foreground(theme.onAccent).Bold(true)
			out = append(out, rightAlign("   "+bar.Render(" ⏏ EXIT "+strings.TrimPrefix(l.Tool, "factory_exit_")+" ")+" "+stGreen.Render(cut(l.Arg, w-30)), t, w))
		case lineRefusal:
			out = append(out, rightAlign("   "+stRedBold.Render("✗ "+l.Arg), t, w))
		case lineError:
			out = append(out, rightAlign("   "+stRedBold.Render("⚠ "+l.Arg), t, w))
		case lineCore:
			out = append(out, rightAlign("   "+stDim.Render("◇ core "+l.Arg), t, w))
		case lineEnd:
			out = append(out, "", " "+lipgloss.NewStyle().Background(theme.yellow).Foreground(theme.onAccent).Bold(true).Render(" ■ "+l.Arg+" ")+" "+t)
		case lineInfo:
			out = append(out, rightAlign(" "+stDim.Render("── "+l.Arg+" ──"), t, w))
		default:
			out = append(out, rightAlign("   "+stFaint.Render("✎ "+l.Tool)+"  "+l.Arg, t, w))
		}
	}
	return out
}

func (m *Model) runLines(w, h int) []string {
	r := &m.run
	now := m.now()
	if r.id == "" {
		lines := []string{}
		runs := m.liveRuns()
		if len(runs) == 0 {
			lines = append(lines, stFaint.Render("no live runs"))
		}
		for i, lr := range runs {
			line := pulse(m.frameN, theme.green) + " " + fit(lr.ID, 10) + lipgloss.NewStyle().Foreground(agentColor(lr.Agent)).Render(fit(lr.Agent, 18)) + fit(lr.TaskID, 8) + projectChip(lr.Project) +
				stFaint.Render("  "+elapsed(now.Sub(time.Unix(lr.StartedAt, 0))))
			if i == r.pick {
				line = lipgloss.NewStyle().Background(theme.surfaceHi).Render(fit(line, w-4))
			}
			lines = append(lines, line)
		}
		return panel("Live runs", lines, w, h, true)
	}
	agent, task := "", ""
	if r.run != nil {
		agent, task = r.run.Spec.Agent, r.run.Spec.TaskID
	}
	sideW := 0
	if w >= 110 {
		sideW = 36
	}
	tw := w - sideW
	inner := tw - 4
	live := !r.ended && (r.run == nil || r.run.Status != "ENDED")
	vh := max(h-2, 1)
	if live {
		vh = max(h-3, 1) // the last line animates the working agent
	}
	if !r.ready {
		r.vp = viewport.New(viewport.WithWidth(inner), viewport.WithHeight(vh))
		r.ready = true
	}
	if r.vp.Width() != inner || r.vp.Height() != vh {
		r.vp.SetWidth(inner)
		r.vp.SetHeight(vh)
		r.dirty = true
	}
	if r.dirty {
		r.lines = runLog(r.events, r.calls)
		r.ctx = contextOf(r.events)
		m.noteErrors(r.id, tallyErrors(transcriptFromEvents(r.events, r.calls)))
		shown := r.lines
		if r.errorsOnly {
			shown = []LogLine{}
			for _, l := range r.lines {
				if l.Kind == lineRefusal || l.Kind == lineError {
					shown = append(shown, l)
				}
			}
		}
		content := m.transcript(agent, shown, inner)
		if len(content) == 0 {
			content = []string{stFaint.Render(" waiting for activity…")}
			if r.errorsOnly {
				content = []string{stGreen.Render(" ✓ no tool errors or refusals") + stFaint.Render(" · e shows everything")}
			}
		}
		r.vp.SetContentLines(content)
		r.dirty = false
		if r.follow {
			r.vp.GotoBottom()
		}
	}
	state := stGreen.Render("● live")
	switch {
	case r.ended:
		state = stYellow.Render("■ ended " + r.outcome)
	case r.st.seen && !r.st.connected:
		state = stRed.Render("◐ reconnecting")
	}
	follow := stGreen.Render("follow")
	if !r.follow {
		follow = stYellow.Render("paused · G")
	}
	title := lipgloss.NewStyle().Foreground(agentColor(agent)).Bold(true).Render(agent) + stFaint.Render(" · "+r.id+" · "+dash(task)+" · ") + state + stFaint.Render(" · ") + follow
	if t, ok := m.runErrs(r.id); ok && t.N > 0 {
		title += stFaint.Render(" · ") + errBadge(t.N)
	}
	if r.errorsOnly {
		title += stFaint.Render(" · ") + stRedBold.Render("errors only")
	}
	body := strings.Split(r.vp.View(), "\n")
	if live {
		body = append(body, "   "+lipgloss.NewStyle().Foreground(agentColor(agent)).Render(m.spin.View()+" "+agent+" is working"+strings.Repeat(".", m.frameN%4)))
	}
	main := panel(title, body, tw, h, true)
	if sideW == 0 {
		return main
	}
	return hjoin(0, main, panel("Run stats", m.runStats(sideW-4), sideW, h, false))
}

func (m *Model) runStats(w int) []string {
	r := &m.run
	const kw = 11
	if r.run == nil {
		if r.runErr != nil {
			return []string{stRed.Render(r.runErr.Error())}
		}
		return []string{m.spin.View() + " loading"}
	}
	run := r.run
	var in, out, cr, cw int64
	var cost float64
	model := ""
	for _, u := range run.Usage {
		in, out, cr, cw, cost = in+u.Input, out+u.Output, cr+u.CacheRead, cw+u.CacheWrite, cost+u.CostUSD
		model = u.Model
	}
	status := run.Status
	if run.Outcome != "" {
		status += " · " + run.Outcome
	}
	end := m.now()
	if run.FinishedAt > 0 {
		end = time.Unix(run.FinishedAt, 0)
	}
	refusals, tools, shells := 0, 0, 0
	for _, l := range r.lines {
		switch l.Kind {
		case lineRefusal:
			refusals++
		case lineShell:
			shells++
		case lineMCP, lineTool, lineExit:
			tools++
		}
	}
	lines := []string{
		kv("Agent", lipgloss.NewStyle().Foreground(agentColor(run.Spec.Agent)).Bold(true).Render(run.Spec.Agent), kw),
		kv("Task", dash(run.Spec.TaskID), kw),
		kv("Status", statusStyle(run.Status).Render(status), kw),
		kv("Model", dash(model), kw),
		kv("Context", contextText(r.ctx, model, max(w-kw-1, 10)), kw),
		stFaint.Render("(run total)"),
		kv("Tokens", thousands(in)+stFaint.Render(" in ")+thousands(out)+stFaint.Render(" out"), kw),
		kv("Cache R/W", thousands(cr)+stFaint.Render(" / ")+thousands(cw), kw),
		kv("Cost", stBold.Render(money(cost)), kw),
		kv("Duration", elapsed(end.Sub(time.Unix(run.StartedAt, 0))), kw),
		kv("Steps", fmt.Sprintf("%d", len(r.lines)), kw),
		kv("Shell", fmt.Sprintf("%d", shells), kw),
		kv("Tools", fmt.Sprintf("%d", tools), kw),
	}
	if refusals > 0 {
		lines = append(lines, kv("Refusals", stRedBold.Render(fmt.Sprint(refusals)), kw))
	}
	if t, ok := m.runErrs(r.id); ok && t.N > 0 {
		lines = append(lines, kv("Errors", errBadge(t.N), kw), "", stRedBold.Render("Last error ")+stDim.Render(m.clock(t.At)))
		for _, l := range wrap(t.Last, w) {
			lines = append(lines, stRed.Render(l))
		}
		lines = append(lines, "", stFaint.Render("t transcript · e errors only"))
	}
	return lines
}
