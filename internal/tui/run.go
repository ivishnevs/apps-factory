package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"

	"appfactory/internal/storage"
)

// runState is the live run view: one run's activity, streamed, with follow mode.
type runState struct {
	id      string
	run     *storage.Run
	runErr  error
	events  []RunEvent
	lastSeq int64
	calls   []storage.ToolCall
	lines   []LogLine
	dirty   bool
	ended   bool
	outcome string
	follow  bool
	st      streamState
	cancel  context.CancelFunc
	vp      viewport.Model
	ready   bool
	// pick is the cursor of the live run picker shown without a run.
	pick int
}

// runPoll is how often the live run view reloads the run and its tool calls.
const runPoll = 2 * time.Second

// openRun shows a run in the live run view and starts streaming its events.
func (m *Model) openRun(id string) tea.Cmd {
	r := &m.run
	if r.cancel != nil {
		r.cancel()
	}
	follow := r.follow
	*r = runState{id: id, follow: follow || r.id == "", dirty: true}
	r.follow = true
	m.switchTab(tabRun)
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
		if prev == tabRun {
			prev = tabDashboard
		}
		return m.switchTab(prev)
	case "t":
		if r.run != nil && r.run.Spec.TaskID != "" {
			id := r.run.Spec.TaskID
			m.tasks.detailID, m.tasks.loading, m.tasks.scroll, m.tasks.open = id, true, 0, !m.wideTasks()
			m.tasks.detail = nil
			m.switchTab(tabTasks)
			return m.loadDetail(id)
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

func (m *Model) runLines(w, h int) []string {
	r := &m.run
	now := m.now()
	if r.id == "" {
		lines := []string{stTitle.Render("Live runs") + stFaint.Render("  enter: follow a run · from Tasks press L on a task"), ""}
		runs := m.liveRuns()
		if len(runs) == 0 {
			return append(lines, stFaint.Render("  no live runs"))
		}
		for i, lr := range runs {
			line := fmt.Sprintf("  %s %s %s %s %s  started %s ago", fit(lr.ID, 12), fit(lr.Agent, 18), fit(lr.TaskID, 10), statusStyle(lr.Status).Render(fit(lr.Status, 10)), fit(lr.Project, 12), ago(now, time.Unix(lr.StartedAt, 0)))
			if i == r.pick {
				line = stSelected.Render(fit(stripStyles(line), w))
			}
			lines = append(lines, line)
		}
		return lines
	}
	head := stTitle.Render("Run "+r.id) + " "
	if run := r.run; run != nil {
		status := run.Status
		if run.Outcome != "" {
			status += "/" + run.Outcome
		}
		head += fmt.Sprintf("%s · task %s · %s", run.Spec.Agent, dash(run.Spec.TaskID), statusStyle(run.Status).Render(status))
		if run.StartedAt > 0 {
			head += " · started " + ago(now, time.Unix(run.StartedAt, 0)) + " ago"
		}
		if c := usageCost(run.Usage); c > 0 {
			head += fmt.Sprintf(" · ~$%.2f", c)
		}
	} else if r.runErr != nil {
		head += stRed.Render(r.runErr.Error())
	}
	flags := []string{}
	if r.follow {
		flags = append(flags, stGreen.Render("follow on"))
	} else {
		flags = append(flags, stYellow.Render("follow off (f)"))
	}
	switch {
	case r.ended:
		flags = append(flags, "ended "+r.outcome)
	case r.st.connected:
		flags = append(flags, stGreen.Render("live"))
	case r.st.seen:
		flags = append(flags, stRed.Render("reconnecting"))
	}
	flags = append(flags, fmt.Sprintf("%d events", len(r.events)))
	lines := []string{cut(head, w), cut(stFaint.Render(strings.Join(flags, " · ")+" · j/k scroll · G bottom · t task · esc back"), w)}
	vh := max(h-len(lines), 1)
	if !r.ready {
		r.vp = viewport.New(viewport.WithWidth(w), viewport.WithHeight(vh))
		r.ready = true
	}
	if r.vp.Width() != w || r.vp.Height() != vh {
		r.vp.SetWidth(w)
		r.vp.SetHeight(vh)
		r.dirty = true
	}
	if r.dirty {
		r.lines = runLog(r.events, r.calls)
		content := make([]string, 0, len(r.lines))
		for _, l := range r.lines {
			label := lineKindStyle(l.Kind).Render(fit(l.Kind, 8))
			text := l.Text
			if l.Kind == lineSay || l.Kind == lineRefusal || l.Kind == lineError || l.Kind == lineExit {
				text = lineKindStyle(l.Kind).Render(text)
			}
			content = append(content, cut(stFaint.Render(m.clock(l.At))+" "+label+text, w))
		}
		if len(content) == 0 {
			content = []string{stFaint.Render("  waiting for activity…")}
		}
		r.vp.SetContentLines(content)
		r.dirty = false
		if r.follow {
			r.vp.GotoBottom()
		}
	}
	return append(lines, strings.Split(r.vp.View(), "\n")...)
}
