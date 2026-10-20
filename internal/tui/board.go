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
	"charm.land/lipgloss/v2"

	"appfactory/internal/storage"
)

// boardState is the kanban board and the task detail modal it opens.
type boardState struct {
	col, offset   int
	rows          map[string]int
	filter        string
	editing       bool
	ti            textinput.Model
	showCancelled bool
	// open shows the task detail modal of detailID.
	open     bool
	detailID string
	detail   *TaskDetail
	err      error
	loading  bool
	scroll   int
}

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

// boardTasks are the tasks the filter and the project selection keep.
func (m *Model) boardTasks() []Task {
	f := parseTaskFilter(m.board.filter)
	out := []Task{}
	for _, t := range m.ov.Tasks {
		if t.Kind == "conversation" && !f.conv || m.project != "" && t.ProjectID != m.project || f.project != "" && t.ProjectID != f.project ||
			f.assignee != "" && t.Assignee != f.assignee || f.status != "" && !strings.Contains(t.Status, f.status) || f.open && terminal(t.Status) ||
			f.attention && !m.needsOwner(t) {
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
	return out
}

// columnOf is a task's board column.
func (m *Model) columnOf(t Task) string {
	switch {
	case t.Status == "DONE":
		return "done"
	case t.Status == "CANCELLED":
		return "cancelled"
	case t.Status == "BACKLOG":
		return "backlog"
	case t.Status == "IN_REVIEW":
		return "review"
	case m.isLive(t.ID) || t.Status == "IN_PROGRESS":
		return "running"
	case t.Status == "BLOCKED" || strings.HasPrefix(t.Reason, "parked:") || strings.HasPrefix(t.Reason, "paused:") || t.Reason == "dependencies" ||
		t.Reason == "artifacts" || t.Reason == "unassigned" || t.Reason == "pinned_node_unavailable":
		return "waiting"
	}
	return "ready"
}

// columns are the visible columns with their cards, in board order.
func (m *Model) columns() []struct {
	key, name string
	tasks     []Task
} {
	by := map[string][]Task{}
	for _, t := range m.boardTasks() {
		c := m.columnOf(t)
		by[c] = append(by[c], t)
	}
	out := []struct {
		key, name string
		tasks     []Task
	}{}
	for _, c := range boardColumns {
		if c.key == "cancelled" && !m.board.showCancelled {
			continue
		}
		list := by[c.key]
		sort.SliceStable(list, func(i, j int) bool {
			if c.key == "done" || c.key == "cancelled" {
				return list[i].UpdatedAt > list[j].UpdatedAt // most recently closed first
			}
			if m.needsOwner(list[i]) != m.needsOwner(list[j]) {
				return m.needsOwner(list[i])
			}
			if list[i].Priority != list[j].Priority {
				return list[i].Priority > list[j].Priority
			}
			return list[i].UpdatedAt > list[j].UpdatedAt
		})
		out = append(out, struct {
			key, name string
			tasks     []Task
		}{c.key, c.name, list})
	}
	return out
}

func (s *boardState) clamp(m *Model) {
	cols := m.columns()
	if s.col >= len(cols) {
		s.col = max(len(cols)-1, 0)
	}
	if s.rows == nil {
		s.rows = map[string]int{}
	}
	for _, c := range cols {
		if s.rows[c.key] >= len(c.tasks) {
			s.rows[c.key] = max(len(c.tasks)-1, 0)
		}
	}
}

func (s *boardState) setFilter(m *Model, f string) {
	s.filter, s.open = f, false
	s.clamp(m)
}

// selected is the focused card's task.
func (s *boardState) selected(m *Model) (Task, bool) {
	if s.open && s.detail != nil {
		if t, ok := m.taskByID(s.detail.Task.ID); ok {
			return t, true
		}
		return s.detail.Task, true
	}
	cols := m.columns()
	if s.col < len(cols) {
		c := cols[s.col]
		if r := s.rows[c.key]; r < len(c.tasks) {
			return c.tasks[r], true
		}
	}
	return Task{}, false
}

// focusTask moves the board focus to a task's card.
func (m *Model) focusTask(id string) {
	m.board.clamp(m)
	for i, c := range m.columns() {
		for j, t := range c.tasks {
			if t.ID == id {
				m.board.col, m.board.rows[c.key] = i, j
			}
		}
	}
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

func (m *Model) onDetail(msg detailMsg) tea.Cmd {
	if msg.id != m.board.detailID {
		return nil
	}
	m.board.loading = false
	if msg.err != nil {
		m.board.err = msg.err
		return nil
	}
	d := msg.d
	m.board.detail, m.board.err = &d, nil
	// The runs' tool error counts (ended runs are counted once).
	ids := []string{}
	for _, r := range d.Attempts {
		ids = append(ids, r.Spec.ID)
	}
	return m.ensureErrs(ids...)
}

// detailRun is the open task's live run, else its last attempt.
func (m *Model) detailRun() string {
	d := m.board.detail
	if d == nil {
		return ""
	}
	if id, ok := m.liveRunOf(d.Task.ID); ok {
		return id
	}
	if n := len(d.Attempts); n > 0 {
		return d.Attempts[n-1].Spec.ID
	}
	return ""
}

// openDetail opens a task's detail modal on the board.
func (m *Model) openDetail(id string) tea.Cmd {
	s := &m.board
	s.detailID, s.loading, s.scroll, s.err, s.open = id, true, 0, nil, true
	if s.detail != nil && s.detail.Task.ID != id {
		s.detail = nil
	}
	m.switchTab(tabBoard)
	m.focusTask(id)
	return m.loadDetail(id)
}

func (s *boardState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
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
		s.clamp(m)
		return cmd
	}
	if s.open {
		switch key {
		case "esc", "backspace", "q":
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
		case "o":
			return m.openTaskPR()
		case "t":
			if id := m.detailRun(); id != "" {
				return m.openTranscript(id)
			}
			m.setToast(s.detailID+" has no run yet", true)
			return nil
		}
		if t, ok := s.selected(m); ok {
			return m.taskAction(t, key)
		}
		return nil
	}
	s.clamp(m)
	cols := m.columns()
	cur := func() (string, int) {
		if s.col < len(cols) {
			return cols[s.col].key, len(cols[s.col].tasks)
		}
		return "", 0
	}
	switch key {
	case "h", "left":
		s.col = max(s.col-1, 0)
	case "l", "right":
		s.col = min(s.col+1, max(len(cols)-1, 0))
	case "j", "down":
		if c, n := cur(); n > 0 {
			s.rows[c] = min(s.rows[c]+1, n-1)
		}
	case "k", "up":
		if c, _ := cur(); c != "" {
			s.rows[c] = max(s.rows[c]-1, 0)
		}
	case "g", "home":
		if c, _ := cur(); c != "" {
			s.rows[c] = 0
		}
	case "G", "end":
		if c, n := cur(); n > 0 {
			s.rows[c] = n - 1
		}
	case "/":
		s.ti = textinput.New()
		s.ti.Prompt = "filter › "
		s.ti.Placeholder = "a:AGENT s:STATUS ! open words"
		s.ti.SetValue(s.filter)
		s.ti.CursorEnd()
		s.ti.SetWidth(max(m.width-14, 10))
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
	case "z":
		s.showCancelled = !s.showCancelled
		s.clamp(m)
	case "enter":
		if t, ok := s.selected(m); ok {
			return m.openDetail(t.ID)
		}
	case "n":
		return m.openForm()
	default:
		if t, ok := s.selected(m); ok {
			return m.taskAction(t, key)
		}
	}
	return nil
}

// taskAction starts an owner action on a task: a confirmation, or an input then a confirmation.
func (m *Model) taskAction(t Task, key string) tea.Cmd {
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
		if id, ok := m.liveRunOf(t.ID); ok {
			run = id
		}
		if run == "" && m.board.detail != nil && m.board.detail.Task.ID == t.ID && len(m.board.detail.Attempts) > 0 {
			run = m.board.detail.Attempts[len(m.board.detail.Attempts)-1].Spec.ID
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

// cardLines are the four lines of a task card; the selected card shows its run's last tool error.
func (m *Model) cardLines(t Task, w int, col string, selected bool) []string {
	now := m.now()
	dot := columnColor(col).Render("●")
	if col == "running" || m.isLive(t.ID) {
		dot = pulse(m.frameN, theme.green)
	}
	title := t.Title
	if m.needsOwner(t) {
		title = stRedBold.Render("⚠ ") + stBold.Render(title)
	} else {
		title = stBold.Render(title)
	}
	prio := stDim.Render("▽")
	switch {
	case t.Priority >= 8:
		prio = stRed.Render("▲")
	case t.Priority >= 5:
		prio = stYellow.Render("△")
	}
	errs, hasErrs := m.taskErrs(t)
	badge := ""
	if hasErrs && errs.N > 0 {
		badge = errBadge(errs.N) + " "
	}
	line2 := rightAlign(stFaint.Render(t.ID)+" "+projectChip(t.ProjectID), badge+prio+stFaint.Render(strconv.Itoa(t.Priority)), w)
	cost := ""
	for _, g := range m.ov.TaskCosts.Groups {
		if g.Key == t.ID {
			cost = money(g.CostUSD) + " · "
		}
	}
	line3 := rightAlign(agentChip(t.Assignee), stFaint.Render(cost+ago(now, time.Unix(t.UpdatedAt, 0))), w)
	var line4 string
	switch {
	case selected && hasErrs && errs.N > 0:
		line4 = stRedBold.Render("✗ ") + stRed.Render(errs.Last)
	case m.isLive(t.ID):
		act := "working…"
		if l, ok := m.agentLast[t.Assignee]; ok {
			act = subText(l)
			line4 = stGreen.Render(m.spin.View()+" ") + lineKindStyle(l.Kind).Render(kindIcon(l.Kind)) + " " + stFaint.Render(act)
		} else {
			line4 = stGreen.Render(m.spin.View()+" ") + stFaint.Render(act)
		}
	case m.needsOwner(t):
		line4 = stRedBold.Render("↳ you: ") + stFaint.Render(t.Clearer.Action)
	case col == "done":
		line4 = stGreen.Render("✓ ") + stDim.Render("done "+ago(now, time.Unix(t.UpdatedAt, 0))+" ago")
	case col == "cancelled":
		line4 = stDim.Render("✗ cancelled")
	case col == "backlog":
		line4 = stDim.Render("○ not released")
	case col == "review":
		line4 = stCyan.Render("◎ ") + stFaint.Render("review by "+dash(t.Reviewer))
	default:
		if why := m.whyLine(t); why != "" {
			line4 = stYellow.Render(cut(why, w))
		} else {
			line4 = stBlue.Render("◌ ") + stFaint.Render("ready · next free slot")
		}
	}
	return []string{dot + " " + title, line2, line3, cut(line4, w)}
}

func (m *Model) boardLines(w, h int) []string {
	if !m.loaded {
		return m.notLoaded()
	}
	s := &m.board
	s.clamp(m)
	cols := m.columns()
	head := []string{}
	if s.editing {
		head = append(head, " "+s.ti.View())
	} else {
		info := stFaint.Render(fmt.Sprintf(" %d tasks", len(m.boardTasks())))
		if s.filter != "" {
			info += "  filter " + stYellow.Render(s.filter)
		}
		if !s.showCancelled {
			info += stDim.Render("  · z shows cancelled")
		}
		head = append(head, info)
	}
	colH := h - len(head)
	n := len(cols)
	visible := max(min(n, w/30), 1)
	if s.col < s.offset {
		s.offset = s.col
	}
	if s.col >= s.offset+visible {
		s.offset = s.col - visible + 1
	}
	s.offset = min(s.offset, max(n-visible, 0))
	cw := w / visible
	blocks := [][]string{}
	for i := s.offset; i < s.offset+visible && i < n; i++ {
		c := cols[i]
		width := cw
		if i == s.offset+visible-1 {
			width = w - cw*(visible-1)
		}
		focused := i == s.col
		inner := width - 4
		cardW := inner
		const cardH = 6
		lines := []string{}
		per := max((colH-2)/cardH, 1)
		sel := s.rows[c.key]
		first := 0
		if sel >= per {
			first = sel - per + 1
		}
		for j := first; j < len(c.tasks) && j < first+per; j++ {
			lines = append(lines, card(m.cardLines(c.tasks[j], cardW-4, c.key, focused && j == sel), cardW, cardH, columnColor(c.key).GetForeground(), focused && j == sel)...)
		}
		if len(c.tasks) == 0 {
			lines = append(lines, stDim.Render("  —"))
		}
		more := ""
		if hidden := len(c.tasks) - per; hidden > 0 {
			more = stFaint.Render(fmt.Sprintf(" +%d", hidden))
		}
		// A click on a card focuses it; a second click on the focused card opens it.
		col, key, x0 := i, c.key, (i-s.offset)*cw
		y0 := 2 + len(head) + 1
		m.zones = append(m.zones, zone{x0: x0, y0: y0, x1: x0 + width, y1: y0 + colH - 2, click: func(_, y int) tea.Cmd {
			j := first + y/cardH
			if j >= len(c.tasks) {
				s.col = col
				return nil
			}
			if s.col == col && s.rows[key] == j {
				return m.openDetail(c.tasks[j].ID)
			}
			s.col, s.rows[key] = col, j
			return nil
		}})
		title := columnColor(c.key).Render("● ") + lipgloss.NewStyle().Foreground(theme.text).Bold(focused).Render(c.name) + stFaint.Render(fmt.Sprintf(" %d", len(c.tasks))) + more
		blocks = append(blocks, frame(title, lines, width, colH, theme.faint, focused, false))
	}
	if s.offset > 0 {
		head[0] = rightAlign(head[0], stFaint.Render("◀ more"), w)
	}
	if s.offset+visible < n {
		head[0] = rightAlign(head[0], stFaint.Render("more ▶"), w)
	}
	return append(head, hjoin(0, blocks...)...)
}

// detailModal is the open task's detail as a modal panel.
func (m *Model) detailModal(w, h int) []string {
	s := &m.board
	mw, mh := min(w-4, 110), h-1
	inner := mw - 4
	var lines []string
	title := s.detailID
	switch {
	case s.err != nil:
		lines = []string{"", stRed.Render(s.err.Error())}
	case s.detail == nil:
		lines = []string{"", m.spin.View() + " loading " + s.detailID + "…"}
	default:
		lines = m.detailLines(*s.detail, inner)
		title = s.detail.Task.ID + " · " + s.detail.Task.Title
	}
	room := mh - 4
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
	keys := [][2]string{{"p/u", "pause/resume"}, {"r", "retry"}, {"n", "run"}, {"x", "cancel"}, {"a", "reassign"}, {"P", "priority"}, {"c", "comment"},
		{"d", "direct"}, {"L", "live"}, {"t", "transcript"}, {"o", "PR"}, {"esc", "close"}}
	parts := []string{}
	for _, k := range keys {
		parts = append(parts, stAccent.Render(k[0])+stFaint.Render(" "+k[1]))
	}
	hint := strings.Join(parts, " ")
	if ansiWidth(hint) > inner {
		// Narrow: the keys only, the words for the most used.
		parts = parts[:0]
		for _, k := range keys {
			parts = append(parts, stAccent.Render(k[0]))
		}
		hint = strings.Join(parts, " ") + stFaint.Render("  ? names them")
	}
	lines = append(lines, rule(inner), hint)
	return frame(title, lines, mw, mh, theme.accent, true, false)
}

// detailLines renders a task's detail: header, origin, why, triggers, parks, dependencies, children, body,
// runs with outcomes, exits and costs, comments and recent events.
func (m *Model) detailLines(d TaskDetail, w int) []string {
	t := d.Task
	if cur, ok := m.taskByID(t.ID); ok {
		t = cur // the list is refreshed more often
	}
	now := m.now()
	out := []string{}
	add := func(s string) { out = append(out, cut(s, w)) }
	section := func(s string) { out = append(out, "", stTitle.Render("▌ "+s)) }
	col := m.columnOf(t)
	add(columnColor(col).Render("● "+strings.ToUpper(col)) + "  " + statusStyle(t.Status).Render(t.Status) + "  " + projectChip(t.ProjectID) + "  " + agentChip(t.Assignee) +
		stFaint.Render(fmt.Sprintf("  priority %d · v%d · attempt %d", t.Priority, t.Version, t.Attempt)))
	origin := "created by " + orOwner(t.CreatedBy)
	if t.OriginTask != "" {
		origin += " from " + t.OriginTask
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
	add(stFaint.Render("origin  ") + origin + stFaint.Render(" · "+ago(now, time.Unix(t.CreatedAt, 0))+" ago"))
	if pr := taskPR(d); pr != nil {
		st := stGreen
		switch pr.State {
		case "merged":
			st = stMagenta
		case "closed":
			st = stRed
		}
		line := stFaint.Render("PR      ") + stBold.Render(fmt.Sprintf("#%d", pr.Number)) + " " + st.Render(dash(pr.State))
		if pr.HeadSHA != "" {
			line += stFaint.Render(" · head " + clip(pr.HeadSHA, 8))
		}
		if u := m.prURL(t.ProjectID, pr); u != "" {
			line += stFaint.Render(" · ") + stBlue.Underline(true).Render(u) + stFaint.Render("  o opens")
		}
		add(line)
	}
	if t.Submission != nil && len(t.Submission.Checks) > 0 {
		add(stFaint.Render("checks  ") + stGreen.Render("✓ ") + strings.Join(t.Submission.Checks, stFaint.Render(" · ")+stGreen.Render("✓ ")))
	}
	why := d.Why
	if why.Reason == "" {
		why.Reason, why.Clearer = t.Reason, t.Clearer
	}
	reason := why.Reason
	if why.Detail != "" {
		reason += " — " + why.Detail
	}
	who := stFaint.Render(dash(why.Clearer.Who))
	if why.Clearer.Who == "owner" {
		who = stRedBold.Render("you")
	}
	add(stFaint.Render("why     ") + stYellow.Render(reason))
	add(stFaint.Render("clears  ") + who + stFaint.Render(" — ") + why.Clearer.Action)
	if t.PausedBy != "" {
		add(stYellow.Render("⏸ paused by " + t.PausedBy))
	}
	if len(why.Pending) > 0 {
		parts := []string{}
		for _, g := range why.Pending {
			p := g.Kind + " by " + g.Actor
			if g.Authority {
				p += " ★"
			}
			parts = append(parts, p)
		}
		add(stFaint.Render("triggers ") + strings.Join(parts, ", "))
	}
	if parks := activeParks(d.Events); len(parks) > 0 {
		add(stFaint.Render("parks   ") + stYellow.Render(strings.Join(parks, ", ")))
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
		add(stFaint.Render("needs   ") + strings.Join(deps, ", "))
	}
	if len(d.Children) > 0 {
		section(fmt.Sprintf("Children (%d)", len(d.Children)))
		for _, c := range d.Children {
			add("  " + columnColor(m.columnOf(c)).Render("●") + " " + fit(c.ID, 8) + statusStyle(c.Status).Render(fit(c.Status, 12)) + fit(agentChip(c.Assignee), 20) + c.Title)
		}
	}
	if strings.TrimSpace(t.Description) != "" {
		section("Body")
		for _, l := range wrap(t.Description, w-2) {
			add("  " + l)
		}
	}
	if t.Submission != nil {
		add(stFaint.Render("submission ") + t.Submission.Summary)
	}
	if t.ReviewFeedback != "" {
		add(stFaint.Render("review feedback ") + t.ReviewFeedback)
	}
	if len(d.Attempts) > 0 {
		section(fmt.Sprintf("Runs (%d)", len(d.Attempts)))
		costs := map[string]storage.RunCost{}
		for _, c := range d.Costs.Runs {
			costs[c.RunID] = c
		}
		for i, r := range d.Attempts {
			st := r.Status
			icon := pulse(m.frameN, theme.green)
			if r.Outcome != "" {
				st = r.Outcome
				icon = stGreen.Render("✓")
				if r.Outcome != "succeeded" {
					icon = stRed.Render("✗")
				}
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
			errs := ""
			if tl, ok := m.runErrs(r.Spec.ID); ok && tl.N > 0 {
				errs = "  " + errBadge(tl.N) + " " + stRed.Render(cut(tl.Last, max(w-80, 10)))
			}
			add(fmt.Sprintf("  %s #%d %s %s %s %s %s", icon, i+1, fit(r.Spec.ID, 9), statusStyle(r.Status).Render(fit(st, 10)), stFaint.Render(fit(started, 12)), fit(dash(exit), 13), fit(cost, 8)) + errs)
			if r.Result != "" {
				add(stFaint.Render("       " + oneLine(r.Result)))
			}
		}
		if tot := d.Costs.Total; tot.Runs > 0 {
			add(stFaint.Render(fmt.Sprintf("  ended runs cost $%.2f over %d runs", tot.CostUSD, tot.Runs)))
		}
	}
	if len(d.Comments) > 0 {
		section(fmt.Sprintf("Comments (%d)", len(d.Comments)))
		for _, c := range d.Comments {
			add("  " + stFaint.Render(time.Unix(c.CreatedAt, 0).In(m.opts.Location).Format("01-02 15:04")) + " " + lipgloss.NewStyle().Foreground(agentColor(c.Author)).Render(c.Author) + ": " + oneLine(c.Text))
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
				mark = stRedBold.Render("⚠")
			}
			add("  " + stFaint.Render(m.clock(e.CreatedAt)) + " " + mark + " " + stMagenta.Render(fit(e.Type, 26)) + oneLine(e.Summary))
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
			exit = "⏏ " + strings.TrimPrefix(e.Type, "task.exit.")
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
