package tui

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"appfactory/internal/storage"
)

type tasksState struct {
	cursor   int
	filter   string
	editing  bool
	ti       textinput.Model
	detailID string
	detail   *TaskDetail
	err      error
	loading  bool
	// open shows the detail in place of the list (narrow terminals).
	open   bool
	scroll int
}

// statusRank orders the task list: what moves first, closed tasks last.
var statusRank = map[string]int{"IN_PROGRESS": 0, "IN_REVIEW": 1, "READY": 2, "BLOCKED": 3, "BACKLOG": 4, "DONE": 5, "CANCELLED": 6}

func terminal(status string) bool { return status == "DONE" || status == "CANCELLED" }

// needsOwner: the task waits on the owner (its clearer, or an inbox item about it).
func (m *Model) needsOwner(t Task) bool {
	if terminal(t.Status) {
		return false
	}
	if t.Clearer.Who == "owner" && t.Reason != "backlog" {
		return true
	}
	for _, msg := range m.ov.Inbox {
		if msg.TaskID == t.ID {
			return true
		}
	}
	return false
}

// taskFilter is the parsed filter: p:PROJECT s:STATUS a:ASSIGNEE ! (attention) open conv, words match id,
// title and reason.
type taskFilter struct {
	project, status, assignee string
	attention, open, conv     bool
	words                     []string
}

func parseTaskFilter(s string) taskFilter {
	var f taskFilter
	for _, tok := range strings.Fields(s) {
		k, v, ok := strings.Cut(tok, ":")
		switch {
		case ok && (k == "p" || k == "project"):
			f.project = v
		case ok && (k == "s" || k == "status"):
			f.status = strings.ToUpper(v)
		case ok && (k == "a" || k == "assignee"):
			f.assignee = v
		case tok == "!" || tok == "attention":
			f.attention = true
		case tok == "open":
			f.open = true
		case tok == "conv":
			f.conv = true
		default:
			f.words = append(f.words, strings.ToLower(tok))
		}
	}
	return f
}

func (m *Model) visibleTasks() []Task {
	f := parseTaskFilter(m.tasks.filter)
	out := []Task{}
	for _, t := range m.ov.Tasks {
		if t.Kind == "conversation" && !f.conv || f.project != "" && t.ProjectID != f.project || f.assignee != "" && t.Assignee != f.assignee ||
			f.status != "" && !strings.Contains(t.Status, f.status) || f.open && terminal(t.Status) || f.attention && !m.needsOwner(t) {
			continue
		}
		text := strings.ToLower(t.ID + " " + t.Title + " " + t.Reason + " " + t.Assignee)
		ok := true
		for _, w := range f.words {
			if !strings.Contains(text, w) {
				ok = false
			}
		}
		if ok {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i].Status), rank(out[j].Status)
		if ri != rj {
			return ri < rj
		}
		return out[i].Priority > out[j].Priority
	})
	return out
}

func rank(s string) int {
	if r, ok := statusRank[s]; ok {
		return r
	}
	return 3
}

func (s *tasksState) filtering() bool { return s.editing }

func (s *tasksState) clamp(m *Model) {
	n := len(m.visibleTasks())
	if s.cursor >= n {
		s.cursor = max(n-1, 0)
	}
}

func (s *tasksState) setFilter(m *Model, f string) {
	s.filter, s.cursor, s.open = f, 0, false
	s.clamp(m)
}

// selected is the task the actions apply to: the open detail's, else the cursor's.
func (s *tasksState) selected(m *Model) (Task, bool) {
	if s.open && s.detail != nil {
		if t, ok := m.taskByID(s.detail.Task.ID); ok {
			return t, true
		}
		return s.detail.Task, true
	}
	list := m.visibleTasks()
	if s.cursor < len(list) {
		return list[s.cursor], true
	}
	return Task{}, false
}

func (m *Model) loadDetail(id string) tea.Cmd {
	c, ctx, all := m.c, m.ctx, m.ov.Tasks
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d, err := c.TaskDetail(rctx, id, all)
		return detailMsg{id, d, err}
	}
}

func (m *Model) onDetail(msg detailMsg) {
	if msg.id != m.tasks.detailID {
		return
	}
	m.tasks.loading = false
	if msg.err != nil {
		m.tasks.err = msg.err
		return
	}
	d := msg.d
	m.tasks.detail, m.tasks.err = &d, nil
}

func (m *Model) wideTasks() bool { return m.width >= 140 }

func (s *tasksState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if s.editing {
		switch key {
		case "enter", "esc":
			s.editing = false
			s.ti.Blur()
			return nil
		}
		var cmd tea.Cmd
		s.ti, cmd = s.ti.Update(k)
		s.filter = s.ti.Value()
		s.cursor = 0
		return cmd
	}
	list := m.visibleTasks()
	if s.open {
		switch key {
		case "esc", "backspace", "h", "left":
			s.open = false
			return nil
		case "j", "down":
			s.scroll++
			return nil
		case "k", "up":
			s.scroll = max(s.scroll-1, 0)
			return nil
		case "pgdown", "space":
			s.scroll += m.height / 2
			return nil
		case "pgup":
			s.scroll = max(s.scroll-m.height/2, 0)
			return nil
		case "g":
			s.scroll = 0
			return nil
		}
		return s.action(m, key)
	}
	switch key {
	case "j", "down":
		s.cursor = min(s.cursor+1, max(len(list)-1, 0))
	case "k", "up":
		s.cursor = max(s.cursor-1, 0)
	case "pgdown":
		s.cursor = min(s.cursor+m.height/2, max(len(list)-1, 0))
	case "pgup":
		s.cursor = max(s.cursor-m.height/2, 0)
	case "g", "home":
		s.cursor = 0
	case "G", "end":
		s.cursor = max(len(list)-1, 0)
	case "/":
		s.ti = textinput.New()
		s.ti.Prompt = "filter › "
		s.ti.Placeholder = "p:PROJECT s:STATUS a:AGENT ! open conv words"
		s.ti.SetValue(s.filter)
		s.ti.CursorEnd()
		s.ti.SetWidth(max(m.width-12, 10))
		s.editing = true
		return s.ti.Focus()
	case "esc":
		if s.filter != "" {
			s.setFilter(m, "")
		}
	case "!":
		if strings.Contains(s.filter, "!") {
			s.setFilter(m, strings.TrimSpace(strings.ReplaceAll(s.filter, "!", "")))
		} else {
			s.setFilter(m, strings.TrimSpace(s.filter+" !"))
		}
	case "enter", "l", "right":
		if s.cursor < len(list) {
			id := list[s.cursor].ID
			s.detailID, s.loading, s.scroll, s.err = id, true, 0, nil
			if s.detail != nil && s.detail.Task.ID != id {
				s.detail = nil
			}
			s.open = !m.wideTasks()
			return m.loadDetail(id)
		}
	default:
		return s.action(m, key)
	}
	return nil
}

// action starts an owner action on the selected task: a confirmation, or an input then a confirmation.
func (s *tasksState) action(m *Model, key string) tea.Cmd {
	t, ok := s.selected(m)
	if !ok {
		return nil
	}
	base := Action{TaskID: t.ID, Version: t.Version}
	switch key {
	case "p":
		base.Kind, base.Text = "pause", "paused by owner"
		m.askConfirm(base)
	case "u":
		base.Kind, base.Text = "resume", "resumed by owner"
		m.askConfirm(base)
	case "r":
		base.Kind = "retry"
		m.askConfirm(base)
	case "n":
		base.Kind = "run"
		m.askConfirm(base)
	case "x":
		base.Kind = "cancel"
		for _, c := range childrenOf(t.ID, m.ov.Tasks) {
			if !terminal(c.Status) {
				base.Cascade = true
			}
		}
		m.askConfirm(base)
	case "a":
		base.Kind = "reassign"
		names := []string{}
		for _, a := range m.ov.Agents {
			names = append(names, a.Name)
		}
		return m.openInput("Reassign "+t.ID+" (now "+t.Assignee+") to", "", base, func(a *Action, text string) error {
			if !slices.Contains(names, text) {
				return fmt.Errorf("unknown agent %q; agents: %s", text, strings.Join(names, ", "))
			}
			a.Assignee = text
			return nil
		})
	case "P":
		base.Kind = "reprioritize"
		return m.openInput(fmt.Sprintf("Priority of %s (now %d)", t.ID, t.Priority), strconv.Itoa(t.Priority), base, func(a *Action, text string) error {
			n, err := strconv.Atoi(text)
			if err != nil {
				return fmt.Errorf("priority must be a number")
			}
			a.Priority = n
			return nil
		})
	case "c":
		base.Kind = "comment"
		return m.openInput("Comment on "+t.ID, "", base, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("empty comment")
			}
			a.Text = text
			return nil
		})
	case "d":
		base.Kind = "direct"
		return m.openInput("Directive to "+t.ID+" (start with ! for urgent: stops the live run)", "", base, func(a *Action, text string) error {
			if strings.HasPrefix(text, "!") {
				a.Urgent, text = true, strings.TrimSpace(strings.TrimPrefix(text, "!"))
			}
			if text == "" {
				return fmt.Errorf("empty directive")
			}
			a.Text = text
			return nil
		})
	case "L":
		run := t.CurrentRunID
		if run == "" && s.detail != nil && s.detail.Task.ID == t.ID && len(s.detail.Attempts) > 0 {
			run = s.detail.Attempts[len(s.detail.Attempts)-1].Spec.ID
		}
		if run == "" {
			m.setToast(t.ID+" has no run yet", true)
			return nil
		}
		return m.openRun(run)
	case "f":
		m.flow.filter = FlowFilter{Task: t.ID}
		return m.switchTab(tabFlow)
	}
	return nil
}

func (m *Model) tasksLines(w, h int) []string {
	if !m.loaded {
		return m.dashboardLines(w, h)
	}
	s := &m.tasks
	if s.open {
		return m.detailView(w, h)
	}
	if m.wideTasks() {
		lw := w * 11 / 20
		left := m.taskListLines(lw-1, h)
		right := m.detailView(w-lw, h)
		out := make([]string, h)
		for i := range out {
			l, r := "", ""
			if i < len(left) {
				l = left[i]
			}
			if i < len(right) {
				r = right[i]
			}
			out[i] = fit(l, lw-1) + stFaint.Render("│") + fit(r, w-lw)
		}
		return out
	}
	return m.taskListLines(w, h)
}

func (m *Model) taskListLines(w, h int) []string {
	s := &m.tasks
	list := m.visibleTasks()
	head := stTitle.Render("Tasks") + stFaint.Render(fmt.Sprintf("  %d shown", len(list)))
	if s.filter != "" {
		head += "  filter: " + stYellow.Render(s.filter)
	}
	head += stFaint.Render("  / filter · ! attention · enter detail")
	lines := []string{cut(head, w)}
	if s.editing {
		lines = append(lines, s.ti.View())
	}
	const idW, stW, asW, pW = 11, 12, 17, 4
	whyW := 20
	if w < 100 {
		whyW = 15
	}
	titleW := max(w-idW-stW-asW-pW-whyW-2, 8)
	lines = append(lines, stFaint.Render(cut("  "+col("ID", idW)+col("STATUS", stW)+col("ASSIGNEE", asW)+col("PRI", pW)+col("WHY", whyW)+"TITLE", w)))
	room := max(h-len(lines), 1)
	start := 0
	if s.cursor >= room {
		start = s.cursor - room + 1
	}
	for i := start; i < len(list) && i < start+room; i++ {
		t := list[i]
		mark := "  "
		if m.needsOwner(t) {
			mark = stRedBold.Render("! ")
		}
		why := t.Reason
		if terminal(t.Status) {
			why = ""
		}
		line := mark + col(t.ID, idW) + statusStyle(t.Status).Render(col(t.Status, stW)) + col(t.Assignee, asW) + col(strconv.Itoa(t.Priority), pW) + stFaint.Render(col(why, whyW)) + cut(t.Title, titleW)
		if i == s.cursor {
			line = stSelected.Render(fit(stripStyles(line), w))
		}
		lines = append(lines, line)
	}
	if len(list) == 0 {
		lines = append(lines, stFaint.Render("  no tasks match"))
	}
	return lines
}

// detailView is the selected task's detail, scrolled.
func (m *Model) detailView(w, h int) []string {
	s := &m.tasks
	var lines []string
	switch {
	case s.detailID == "":
		lines = []string{"", stFaint.Render(" Select a task and press enter.")}
	case s.err != nil:
		lines = []string{"", stRed.Render(" " + s.detailID + ": " + s.err.Error())}
	case s.detail == nil:
		lines = []string{"", stFaint.Render(" Loading " + s.detailID + "…")}
	default:
		lines = m.detailLines(*s.detail, w)
	}
	footer := stFaint.Render(cut(" p pause · u resume · r retry · n run · x cancel · a reassign · P priority · c comment · d direct · L live run · f flow · esc back", w))
	room := max(h-1, 1)
	if s.scroll > max(len(lines)-room, 0) {
		s.scroll = max(len(lines)-room, 0)
	}
	lines = lines[s.scroll:]
	if len(lines) > room {
		lines = lines[:room]
	}
	for len(lines) < room {
		lines = append(lines, "")
	}
	return append(lines, footer)
}

// detailLines renders a task's detail: header, origin, why, triggers, parks, dependencies, children, body,
// runs with outcomes, exits and costs, comments and recent events.
func (m *Model) detailLines(d TaskDetail, w int) []string {
	t := d.Task
	if cur, ok := m.taskByID(t.ID); ok {
		t = cur // the list is refreshed more often
	}
	now := m.now()
	iw := w - 2
	out := []string{}
	add := func(s string) { out = append(out, " "+cut(s, iw)) }
	section := func(s string) { out = append(out, "", " "+stBold.Render(s)) }
	add(stBold.Render(t.ID) + "  " + statusStyle(t.Status).Render("["+t.Status+"]") + "  " + t.Title)
	add(stFaint.Render(fmt.Sprintf("project %s · assignee %s · priority %d · v%d · attempt %d", t.ProjectID, dash(t.Assignee), t.Priority, t.Version, t.Attempt)))
	origin := "created by " + orOwner(t.CreatedBy)
	if t.OriginTask != "" {
		origin += " from task " + t.OriginTask
	}
	if t.OriginRun != "" {
		origin += " (run " + t.OriginRun + ")"
	}
	if t.ParentTaskID != "" {
		origin += " · parent " + t.ParentTaskID
	}
	if t.Implementer != "" {
		origin += " · implementer " + t.Implementer
	}
	if t.Reviewer != "" {
		origin += " · reviewer " + t.Reviewer
	}
	add("origin: " + origin + " · " + ago(now, time.Unix(t.CreatedAt, 0)) + " ago")
	why := d.Why
	if why.Reason == "" {
		why.Reason, why.Clearer = t.Reason, t.Clearer
	}
	reason := why.Reason
	if why.Detail != "" {
		reason += " — " + why.Detail
	}
	whoStyle := stFaint
	if why.Clearer.Who == "owner" {
		whoStyle = stRedBold
	}
	add("why: " + stYellow.Render(reason))
	add("  clears: " + whoStyle.Render(dash(why.Clearer.Who)) + " — " + why.Clearer.Action)
	if t.PausedBy != "" {
		add(stYellow.Render("paused by " + t.PausedBy))
	}
	if len(why.Pending) > 0 {
		parts := []string{}
		for _, g := range why.Pending {
			p := g.Kind + " by " + g.Actor
			if g.Authority {
				p += " (authority)"
			}
			parts = append(parts, p)
		}
		add("triggers: " + strings.Join(parts, ", "))
	}
	if parks := activeParks(d.Events); len(parks) > 0 {
		add("parks: " + stYellow.Render(strings.Join(parks, ", ")))
	}
	if len(t.Dependencies) > 0 {
		deps := []string{}
		for _, id := range t.Dependencies {
			st := "?"
			if dt, ok := m.taskByID(id); ok {
				st = dt.Status
			}
			deps = append(deps, id+" "+statusStyle(st).Render(st))
		}
		add("depends on: " + strings.Join(deps, ", "))
	}
	if len(d.Children) > 0 {
		section(fmt.Sprintf("Children (%d)", len(d.Children)))
		for _, c := range d.Children {
			add("  " + fit(c.ID, 10) + statusStyle(c.Status).Render(fit(c.Status, 12)) + fit(c.Assignee, 16) + c.Title)
		}
	}
	if strings.TrimSpace(t.Description) != "" {
		section("Body")
		for _, l := range wrap(t.Description, iw-2) {
			add("  " + l)
		}
	}
	if t.Submission != nil {
		add("submission: " + t.Submission.Summary)
	}
	if t.ReviewFeedback != "" {
		add("review feedback: " + t.ReviewFeedback)
	}
	if len(d.Attempts) > 0 {
		section(fmt.Sprintf("Runs (%d)", len(d.Attempts)))
		costs := map[string]storage.RunCost{}
		for _, c := range d.Costs.Runs {
			costs[c.RunID] = c
		}
		for i, r := range d.Attempts {
			st := r.Status
			if r.Outcome != "" {
				st = r.Outcome
			}
			exit := runExit(d.Events, r.Spec.ID)
			cost := "-"
			if c, ok := costs[r.Spec.ID]; ok {
				cost = storage.CostText(c.CostUSD, c.CostSource)
			} else if u := usageCost(r.Usage); u > 0 {
				cost = fmt.Sprintf("~$%.2f", u)
			}
			started := "-"
			if r.StartedAt > 0 {
				started = time.Unix(r.StartedAt, 0).In(m.opts.Location).Format("01-02 15:04")
			}
			add(fmt.Sprintf("  #%d %s %s %s %s %s", i+1, fit(r.Spec.ID, 10), statusStyle(r.Status).Render(fit(st, 10)), fit(started, 12), fit(dash(exit), 11), cost))
			if r.Result != "" {
				add(stFaint.Render("     " + oneLine(r.Result)))
			}
		}
		if tot := d.Costs.Total; tot.Runs > 0 {
			add(stFaint.Render(fmt.Sprintf("  ended runs cost $%.2f over %d runs", tot.CostUSD, tot.Runs)))
		}
	}
	if len(d.Comments) > 0 {
		section(fmt.Sprintf("Comments (%d)", len(d.Comments)))
		for _, c := range d.Comments {
			add("  " + stFaint.Render(time.Unix(c.CreatedAt, 0).In(m.opts.Location).Format("01-02 15:04")) + " " + stBold.Render(c.Author) + ": " + oneLine(c.Text))
		}
	}
	if len(d.Events) > 0 {
		section("Recent events")
		events := d.Events
		if len(events) > 12 {
			events = events[len(events)-12:]
		}
		for _, e := range events {
			mark := " "
			if e.Attention {
				mark = stRedBold.Render("!")
			}
			add("  " + stFaint.Render(m.clock(e.CreatedAt)) + " " + mark + " " + fit(e.Type, 26) + oneLine(e.Summary))
		}
	}
	return out
}

func usageCost(u storage.UsageList) float64 {
	var c float64
	for _, x := range u {
		c += x.CostUSD
	}
	return c
}

// runExit is the exit a run took, from the task's exit events.
func runExit(events []storage.FeedItem, run string) string {
	exit := ""
	for _, e := range events {
		if e.RunID == run && strings.HasPrefix(e.Type, "task.exit.") {
			exit = "exit " + strings.TrimPrefix(e.Type, "task.exit.")
		}
	}
	return exit
}

// activeParks are the park kinds whose last task.parked event has no later task.unparked of that kind.
func activeParks(events []storage.FeedItem) []string {
	active := map[string]bool{}
	order := []string{}
	for _, e := range events {
		switch {
		case strings.HasPrefix(e.Type, "task.parked."):
			k := strings.TrimPrefix(e.Type, "task.parked.")
			if !active[k] {
				order = append(order, k)
			}
			active[k] = true
		case e.Type == "task.unparked.all":
			for k := range active {
				active[k] = false
			}
		case strings.HasPrefix(e.Type, "task.unparked."):
			active[strings.TrimPrefix(e.Type, "task.unparked.")] = false
		}
	}
	out := []string{}
	for _, k := range order {
		if active[k] {
			out = append(out, k)
		}
	}
	return out
}
