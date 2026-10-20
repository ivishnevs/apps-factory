package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/storage"
)

// Dashboard panels.
type panelID int

const (
	pTree panelID = iota
	pTasks
	pStats
	pActivity
	pPulse
)

var panelNames = map[panelID]string{pTree: "Agent Tree", pTasks: "Tasks", pStats: "Stats", pActivity: "Activity", pPulse: "Pulse"}

type dashState struct {
	focus     panelID
	zoom      bool
	sel       int // agent tree row (0 is the owner)
	taskSel   int
	actScroll int // lines above the newest
	// treeMap and taskMap give the row of each visible line of the tree and tasks panels (mouse).
	treeMap, taskMap []int
}

// actRow is one line of the merged activity stream: feed events and live run steps of every agent.
type actRow struct {
	At        time.Time
	Agent     string
	Tool      string
	Arg       string
	Kind      string
	Attention bool
	Project   string
	Task      string
	Run       string
}

const activityKeep = 600

// addActivity merges a row into the activity stream in time order.
func (m *Model) addActivity(r actRow) {
	m.activity = append(m.activity, r)
	for i := len(m.activity) - 1; i > 0 && m.activity[i].At.Before(m.activity[i-1].At); i-- {
		m.activity[i], m.activity[i-1] = m.activity[i-1], m.activity[i]
	}
	if len(m.activity) > activityKeep {
		m.activity = append([]actRow(nil), m.activity[len(m.activity)-activityKeep:]...)
	}
}

// feedActivity is a feed row as an activity line: who, what (a short event name) and the gist.
func feedActivity(item storage.FeedItem) actRow {
	r := actRow{At: item.CreatedAt, Agent: item.Actor, Arg: oneLine(item.Summary), Attention: item.Attention, Project: item.ProjectID, Task: item.TaskID, Run: item.RunID, Kind: "feed"}
	if r.Agent == "" {
		r.Agent = item.Agent
	}
	if r.Agent == "" {
		r.Agent = "factory"
	}
	if in, ok := interactionOf(item); ok {
		r.Agent, r.Tool = in.From, verbLabel(in.Verb)
		r.Arg = in.Text
		if in.To != "" {
			r.Arg = "→ " + in.To + "  " + in.Text
		}
		return r
	}
	parts := strings.Split(item.Type, ".")
	r.Tool = parts[len(parts)-1]
	switch {
	case item.Type == "task.started":
		r.Tool = "started"
		r.Arg = item.TaskID + " " + item.Summary
	case strings.HasPrefix(item.Type, "task.parked."):
		r.Tool = "parked"
	case strings.HasPrefix(item.Type, "task.alert."):
		r.Tool = "alert"
	case strings.HasPrefix(item.Type, "node."):
		r.Agent, r.Tool = "node", strings.TrimPrefix(item.Type, "node.")
	case item.Type == "run.command_refused":
		r.Tool = "refused"
	}
	return r
}

// treeRow is one row of the agent tree.
type treeRow struct {
	name   string // "" for the owner row
	prefix string
	agent  agentRow
	sub    *LogLine // the live run's current step
}

func (m *Model) treeRows() []treeRow {
	rows := []treeRow{{}}
	for _, r := range m.agentRows() {
		tr := treeRow{name: r.Name, prefix: r.tree, agent: r}
		if r.run != "" {
			if l, ok := m.agentLast[r.Name]; ok {
				l := l
				tr.sub = &l
			}
		}
		rows = append(rows, tr)
	}
	return rows
}

func (d *dashState) key(m *Model, k string) tea.Cmd {
	panels := m.dashPanels()
	idx := 0
	for i, p := range panels {
		if p == d.focus {
			idx = i
		}
	}
	switch k {
	case "h", "left":
		d.focus = panels[(idx+len(panels)-1)%len(panels)]
		return nil
	case "l", "right":
		d.focus = panels[(idx+1)%len(panels)]
		return nil
	case "z":
		d.zoom = !d.zoom
		return nil
	case "esc":
		d.zoom = false
		return nil
	}
	switch d.focus {
	case pTree:
		rows := m.treeRows()
		switch k {
		case "j", "down":
			d.sel, d.taskSel = min(d.sel+1, len(rows)-1), 0
		case "k", "up":
			d.sel, d.taskSel = max(d.sel-1, 0), 0
		case "enter":
			r := rows[min(d.sel, len(rows)-1)]
			if r.agent.run != "" {
				return m.openRun(r.agent.run)
			}
			d.focus = pTasks
		case "f":
			if r := rows[min(d.sel, len(rows)-1)]; r.name != "" {
				m.flow.filter = FlowFilter{Agent: r.name}
				return m.switchTab(tabFlow)
			}
		}
	case pTasks:
		list := m.panelTasks()
		switch k {
		case "j", "down":
			d.taskSel = min(d.taskSel+1, max(len(list)-1, 0))
		case "k", "up":
			d.taskSel = max(d.taskSel-1, 0)
		case "enter":
			if d.taskSel < len(list) {
				return m.openDetail(list[d.taskSel].ID)
			}
		default:
			if d.taskSel < len(list) {
				return m.taskAction(list[d.taskSel], k)
			}
		}
	case pActivity:
		switch k {
		case "k", "up":
			d.actScroll++
		case "j", "down":
			d.actScroll = max(d.actScroll-1, 0)
		case "G", "end":
			d.actScroll = 0
		}
	}
	return nil
}

// dashPanels are the panels the current width shows.
func (m *Model) dashPanels() []panelID {
	if m.width >= 110 {
		return []panelID{pTree, pTasks, pStats, pActivity, pPulse}
	}
	return []panelID{pTree, pTasks, pActivity}
}

// selectedAgent is the agent row selected in the tree; ok is false for the owner row.
func (m *Model) selectedAgent() (agentRow, bool) {
	rows := m.treeRows()
	if m.dash.sel >= len(rows) {
		m.dash.sel = len(rows) - 1
	}
	if m.dash.sel <= 0 {
		return agentRow{}, false
	}
	return rows[m.dash.sel].agent, true
}

// panelTasks are the tasks of the Tasks panel: the selected agent's, or what waits on the owner.
func (m *Model) panelTasks() []Task {
	a, ok := m.selectedAgent()
	out := []Task{}
	for _, t := range m.ov.Tasks {
		if t.Kind == "conversation" || m.project != "" && t.ProjectID != m.project {
			continue
		}
		if ok && t.Assignee == a.Name || !ok && (m.needsOwner(t) || t.Status == "IN_REVIEW") {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := m.checkRank(out[i]), m.checkRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i].Priority > out[j].Priority
	})
	return out
}

func (m *Model) checkRank(t Task) int {
	if _, live := m.liveRunOf(t.ID); live {
		return 0
	}
	switch {
	case m.needsOwner(t):
		return 1
	case t.Status == "IN_REVIEW":
		return 2
	case t.Status == "READY" || t.Status == "BLOCKED":
		return 3
	case t.Status == "BACKLOG":
		return 4
	}
	return 5
}

func (m *Model) dashboardLines(w, h int) []string {
	if !m.loaded {
		return m.notLoaded()
	}
	d := &m.dash
	if d.zoom {
		return m.dashPanel(d.focus, w, h)
	}
	topH := max(h*11/20, 9)
	botH := h - topH
	var top, bottom []string
	if w >= 110 {
		tw := w * 36 / 100
		kw := w * 34 / 100
		sw := w - tw - kw
		top = hjoin(0, m.dashPanel(pTree, tw, topH), m.dashPanel(pTasks, kw, topH), m.dashPanel(pStats, sw, topH))
		pw := min(max(w*30/100, 34), 46)
		bottom = hjoin(0, m.dashPanel(pActivity, w-pw, botH), m.dashPanel(pPulse, pw, botH))
	} else {
		tw := w / 2
		top = hjoin(0, m.dashPanel(pTree, tw, topH), m.dashPanel(pTasks, w-tw, topH))
		bottom = m.dashPanel(pActivity, w, botH)
	}
	return append(top, bottom...)
}

func (m *Model) dashPanel(p panelID, w, h int) []string {
	focused := m.dash.focus == p
	inner := w - 4
	ih := h - 2
	var title string
	var lines []string
	switch p {
	case pTree:
		title, lines = "Agent Tree", m.treeLines(inner, ih)
	case pTasks:
		title = "Tasks · needs you"
		if a, ok := m.selectedAgent(); ok {
			title = "Tasks · " + a.Name
		}
		lines = m.taskPanelLines(inner, ih)
	case pStats:
		title, lines = m.statsLines(inner)
	case pActivity:
		title, lines = "Activity", m.activityLines(inner, ih)
	case pPulse:
		title, lines = "Pulse", m.pulseLines(inner)
	}
	if m.dash.zoom {
		title += "  ⤢"
	}
	return panel(title, lines, w, h, focused)
}

// elapsed is a timer: 0m 45s, 12m 03s, 2h 10m.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

func (m *Model) treeLines(w, h int) []string {
	now := m.now()
	rows := m.treeRows()
	lines := []string{}
	rowOf := []int{}
	selLine := 0
	for i, r := range rows {
		var left, right string
		if r.name == "" {
			att := m.attentionCount()
			left = stAccent.Render("◆") + " " + stBold.Render("owner") + stFaint.Render(" · you")
			right = stFaint.Render(fmt.Sprintf("%d agents", len(rows)-1))
			if att > 0 {
				right = stRedBold.Render(fmt.Sprintf("⚠ %d", att))
			}
		} else {
			a := r.agent
			name := lipgloss.NewStyle().Foreground(agentColor(a.Name)).Bold(a.state == "running").Render(a.Name)
			var dot, doing string
			switch a.state {
			case "running", "queued", "stopping":
				dot = pulse(m.frameN, theme.green)
				doing = a.taskTitle
				right = elapsed(now.Sub(a.runStart))
			case "paused":
				dot, doing = stYellow.Render("⏸"), stYellow.Render("paused")+stFaint.Render(" "+a.pauseReason)
				right = stFaint.Render("paused")
			default:
				if a.waiting != "" {
					dot, doing = stYellow.Render("◐"), stFaint.Render(a.waiting)
					right = stFaint.Render(ago(now, a.at))
				} else {
					dot, doing = stDim.Render("○"), stDim.Render("idle")
					if a.task != "" {
						doing = stDim.Render("idle · next " + a.task)
					}
					right = stDim.Render("idle")
				}
			}
			left = stDim.Render(r.prefix) + dot + " " + name + stFaint.Render(": ") + doing
			if t, ok := m.runErrs(a.run); ok && t.N > 0 {
				right = errBadge(t.N) + " " + right
			}
		}
		if i == m.dash.sel {
			selLine = len(lines)
			lines = append(lines, lipgloss.NewStyle().Background(theme.surfaceHi).Render(rightAlign(left, right, w)))
			rowOf = append(rowOf, i)
		} else {
			lines = append(lines, rightAlign(left, right, w))
			rowOf = append(rowOf, i)
		}
		if t, ok := m.runErrs(r.agent.run); ok && t.N > 0 && i == m.dash.sel && r.name != "" {
			// The selected agent's last tool error.
			cont := strings.ReplaceAll(strings.ReplaceAll(r.prefix, "├ ", "│ "), "└ ", "  ")
			lines = append(lines, rightAlign(stDim.Render(cont+"  ")+stRedBold.Render("✗ ")+stRed.Render(cut(t.Last, w)), stDim.Render(m.clock(t.At)), w))
			rowOf = append(rowOf, i)
		}
		if r.sub != nil {
			cont := strings.ReplaceAll(strings.ReplaceAll(r.prefix, "├ ", "│ "), "└ ", "  ")
			step := lineKindStyle(r.sub.Kind).Render(kindIcon(r.sub.Kind)+" ") + stFaint.Render(cut(subText(*r.sub), w))
			lines = append(lines, rightAlign(stDim.Render(cont+"  └ ")+step, stDim.Render(elapsed(now.Sub(r.sub.At))), w))
			rowOf = append(rowOf, i)
		}
	}
	off := scrollTo(selLine, 0, h, len(lines))
	m.dash.treeMap = rowOf[off:]
	return lines[off:]
}

// subText is a live step as a tree sub-row: "Bash: go test", "Read: module_1.go".
func subText(l LogLine) string {
	if l.Tool != "" && l.Kind != lineShell {
		return l.Tool + ": " + l.Arg
	}
	if l.Kind == lineShell {
		return strings.TrimPrefix(l.Arg, "$ ")
	}
	return l.Text
}

func (m *Model) taskPanelLines(w, h int) []string {
	list := m.panelTasks()
	d := &m.dash
	if d.taskSel >= len(list) {
		d.taskSel = max(len(list)-1, 0)
	}
	if len(list) == 0 {
		return []string{stFaint.Render("nothing here")}
	}
	rowOf := []int{}
	lines := []string{}
	selLine := 0
	for i, t := range list {
		box, tag, title := "[ ]", "", t.Title
		st := stFaint
		switch {
		case m.isLive(t.ID):
			box, tag, st = "[→]", stYellow.Render("← "+m.spin.View()+"running"), stBold
		case m.needsOwner(t):
			box, tag, st = stRedBold.Render("[!]"), stRedBold.Render("← needs you"), stBold
		case t.Status == "DONE":
			box, st = "[✓]", stDim
		case t.Status == "CANCELLED":
			box, st = "[✗]", stDim
		case t.Status == "IN_REVIEW":
			box, tag, st = "[◎]", stCyan.Render("in review"), lipgloss.NewStyle().Foreground(theme.text)
		case t.Status == "BACKLOG":
			st = stDim
		case t.Reason == "runnable":
			tag, st = stBlue.Render("ready"), lipgloss.NewStyle().Foreground(theme.text)
		default:
			st = lipgloss.NewStyle().Foreground(theme.text)
		}
		if !strings.HasPrefix(box, "\x1b") {
			box = st.Render(box)
		}
		line := rightAlign(box+" "+st.Render(title)+" "+stDim.Render(t.ID), tag, w)
		if i == d.taskSel && d.focus == pTasks {
			line = lipgloss.NewStyle().Background(theme.surfaceHi).Render(line)
		}
		if i == d.taskSel {
			selLine = len(lines)
		}
		lines = append(lines, line)
		rowOf = append(rowOf, i)
		if why := m.whyLine(t); why != "" {
			lines = append(lines, "    "+stFaint.Render(cut(why, w-4)))
			rowOf = append(rowOf, i)
		}
	}
	off := scrollTo(selLine, 0, h, len(lines))
	m.dash.taskMap = rowOf[off:]
	return lines[off:]
}

func (m *Model) isLive(task string) bool {
	_, ok := m.liveRunOf(task)
	return ok
}

// whyLine says what a waiting task waits on: "blocked by: P1-3 (developer-1)", "waits on: you — …".
func (m *Model) whyLine(t Task) string {
	if terminal(t.Status) || m.isLive(t.ID) || t.Status == "BACKLOG" || t.Reason == "runnable" || t.Reason == "" {
		return ""
	}
	who := t.Clearer.Who
	if who == "owner" {
		who = "you"
	}
	switch {
	case t.Reason == "dependencies":
		return "↳ blocked by: " + strings.Join(t.Dependencies, ", ") + " (" + t.Clearer.Who + ")"
	case strings.HasPrefix(t.Reason, "parked:"):
		return "↳ " + strings.TrimPrefix(t.Reason, "parked:") + " · waits on " + who + ": " + t.Clearer.Action
	case t.Reason == "capacity":
		return "↳ queued: " + t.Clearer.Action
	}
	return "↳ " + t.Reason + ": " + t.Clearer.Action
}

// runOf is the live run record of an agent (usage so far), if any.
func (m *Model) runOf(a agentRow) (storage.Run, bool) {
	if a.run == "" {
		return storage.Run{}, false
	}
	r, ok := m.ov.LiveRuns[a.run]
	return r, ok
}

func (m *Model) statsLines(w int) (string, []string) {
	now := m.now()
	const kw = 12
	a, ok := m.selectedAgent()
	if !ok {
		active, total := 0, len(m.ov.Agents)
		for _, r := range m.agentRows() {
			if r.run != "" {
				active++
			}
		}
		open := 0
		for _, t := range m.ov.Tasks {
			if !terminal(t.Status) && t.Kind == "" {
				open++
			}
		}
		online := 0
		for _, n := range m.ov.Nodes {
			if n.Online {
				online++
			}
		}
		return "Company", []string{
			kv("Company", stBold.Render(m.ov.Health.Company), kw),
			kv("Agents", fmt.Sprintf("%d (%s)", total, stGreen.Render(fmt.Sprintf("%d active", active))), kw),
			kv("Open tasks", fmt.Sprint(open), kw),
			kv("Needs you", stRedBold.Render(fmt.Sprint(m.attentionCount())), kw),
			kv("Spend today", stBold.Render(money(m.ov.Costs.Total.CostUSD))+stFaint.Render(fmt.Sprintf(" · %d runs", m.ov.Costs.Total.Runs)), kw),
			kv("Spend 24h", spark(m.spendBuckets("", 24), theme.accent), kw),
			kv("Nodes", fmt.Sprintf("%d/%d online", online, len(m.ov.Nodes)), kw),
		}
	}
	lines := []string{kv("Agent", lipgloss.NewStyle().Foreground(agentColor(a.Name)).Bold(true).Render(a.Name)+stFaint.Render(" · "+a.Role), kw)}
	model := a.Model
	run, live := m.runOf(a)
	var in, out, cr, cw int64
	var cost float64
	for _, u := range run.Usage {
		in, out, cr, cw, cost = in+u.Input, out+u.Output, cr+u.CacheRead, cw+u.CacheWrite, cost+u.CostUSD
		if u.Model != "" {
			model = u.Model
		}
	}
	lines = append(lines, kv("Model", model+stFaint.Render(" · "+a.Provider), kw))
	if live {
		total := stFaint.Render(" (run total)")
		lines = append(lines,
			kv("Run", a.run+stFaint.Render(" · "+a.task), kw),
			kv("Context", contextText(m.runCtx[a.run], model, max(w-kw-1, 10)), kw),
			kv("Tokens In", thousands(in)+total, kw),
			kv("Tokens Out", thousands(out)+total, kw),
			kv("Cache R/W", thousands(cr)+stFaint.Render(" / ")+thousands(cw)+total, kw),
			kv("Run cost", stBold.Render(money(cost)), kw),
			kv("Duration", elapsed(now.Sub(a.runStart)), kw),
		)
	} else {
		lines = append(lines, kv("Status", statusStyle(a.state).Render(a.state), kw), kv("Next", dash(a.task), kw))
	}
	lines = append(lines,
		kv("Today", stBold.Render(money(a.cost))+stFaint.Render(fmt.Sprintf(" · %d runs", a.runs)), kw),
		kv("Spend 24h", spark(m.spendBuckets(a.Name, 24), agentColor(a.Name)), kw),
		kv("Activity", spark(m.activityBuckets(a.Name, 24, 5*time.Minute), agentColor(a.Name)), kw),
	)
	title := "Stats · " + a.Name
	return title, lines
}

// spendBuckets are the last n hours of ended-run cost (of one agent, or all).
func (m *Model) spendBuckets(agent string, n int) []float64 {
	out := make([]float64, n)
	now := m.now()
	for _, r := range m.ov.RecentCosts.Runs {
		if agent != "" && r.Agent != agent {
			continue
		}
		h := int(now.Sub(time.Unix(r.RecordedAt, 0)).Hours())
		if h >= 0 && h < n {
			out[n-1-h] += r.CostUSD
		}
	}
	return out
}

// activityBuckets count an agent's activity rows in n buckets of size d up to now.
func (m *Model) activityBuckets(agent string, n int, d time.Duration) []float64 {
	out := make([]float64, n)
	now := m.now()
	for _, r := range m.activity {
		if r.Agent != agent {
			continue
		}
		i := int(now.Sub(r.At) / d)
		if i >= 0 && i < n {
			out[n-1-i]++
		}
	}
	return out
}

func (m *Model) activityLines(w, h int) []string {
	rows := []actRow{}
	for _, r := range m.activity {
		if m.project == "" || r.Project == "" || r.Project == m.project {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return []string{stFaint.Render("waiting for activity…")}
	}
	agentW := 10
	for _, r := range rows {
		agentW = max(agentW, len(r.Agent)+2)
	}
	agentW = min(agentW, 20)
	const toolW = 14
	end := len(rows) - m.dash.actScroll
	if end < h {
		end = min(h, len(rows))
		m.dash.actScroll = len(rows) - end
	}
	start := max(end-h, 0)
	lines := []string{}
	for i := start; i < end; i++ {
		r := rows[i]
		ag := lipgloss.NewStyle().Foreground(agentColor(r.Agent)).Render(fit("["+r.Agent+"]", agentW))
		tool := strings.TrimPrefix(r.Tool, "factory_")
		ts := stCyan
		switch {
		case r.Attention:
			ts = stRedBold
			tool = "⚠ " + tool
		case r.Kind == lineRefusal || r.Kind == lineError || tool == "refused":
			ts = stRedBold
		case r.Kind == lineExit:
			ts = stGreen.Bold(true)
		case r.Kind == lineSay:
			tool, ts = "says", stFaint
		case r.Kind == "feed":
			ts = stMagenta
		}
		arg := r.Arg
		if r.Kind == lineSay {
			arg = "“" + arg + "”"
		}
		line := stDim.Render(r.At.In(m.opts.Location).Format("15:04:05")) + "  " + ag + " " + ts.Render(fit(tool, toolW-1)) + " " + arg
		if i == end-1 && m.dash.actScroll == 0 && m.frameN%2 == 1 {
			line += stAccent.Render("▍")
		}
		lines = append(lines, cut(line, w))
	}
	return lines
}

// boardColumns are the kanban columns.
var boardColumns = []struct{ key, name string }{
	{"backlog", "Backlog"}, {"ready", "Ready"}, {"running", "Running"}, {"waiting", "Waiting"}, {"review", "In review"}, {"done", "Done"}, {"cancelled", "Cancelled"},
}

func columnColor(key string) lipgloss.Style {
	switch key {
	case "ready":
		return stBlue
	case "running":
		return stGreen
	case "waiting":
		return stYellow
	case "review":
		return stCyan
	case "done":
		return stFaint
	case "cancelled":
		return stDim
	}
	return stFaint
}

func (m *Model) pulseLines(w int) []string {
	counts := map[string]int{}
	total := 0
	for _, t := range m.boardTasks() {
		counts[m.columnOf(t)]++
		total++
	}
	lines := []string{}
	bw := max(w-15, 4)
	for _, c := range boardColumns[:6] {
		lines = append(lines, columnColor(c.key).Render(fit(c.name, 10))+hbar(counts[c.key], total, bw, columnColor(c.key).GetForeground())+fmt.Sprintf(" %3d", counts[c.key]))
	}
	sw := max(min(w-18, 24), 6)
	spend := m.spendBuckets("", sw)
	sum := 0.0
	for _, v := range m.spendBuckets("", 24) {
		sum += v
	}
	lines = append(lines, "", stFaint.Render(fit("Spend 24h", 10))+spark(spend, theme.accent)+" "+stBold.Render(money(sum)))
	nodes := []string{}
	for _, n := range m.ov.Nodes {
		name := n.Name
		if name == "" {
			name = n.NodeID
		}
		if n.Online {
			nodes = append(nodes, stGreen.Render("●")+" "+name+stFaint.Render(fmt.Sprintf(" %d/%d", n.InUse.Runs, n.Slots)))
		} else {
			nodes = append(nodes, stRed.Render("○")+" "+name+stFaint.Render(" off"))
		}
	}
	lines = append(lines, stFaint.Render(fit("Nodes", 10))+strings.Join(nodes, "  "))
	// Per-agent activity over the last 2 h.
	lines = append(lines, "")
	for _, r := range m.agentRows() {
		lines = append(lines, lipgloss.NewStyle().Foreground(agentColor(r.Name)).Render(fit(r.Name, 17))+spark(m.activityBuckets(r.Name, max(w-18, 6), 2*time.Hour/time.Duration(max(w-18, 6))), agentColor(r.Name)))
	}
	return lines
}

// dashZones make the dashboard panels clickable: a click focuses the panel and selects the row under it.
func (m *Model) dashZones(w, h int) {
	type rect struct {
		p          panelID
		x, y, w, h int
	}
	rects := []rect{}
	if m.dash.zoom {
		rects = append(rects, rect{m.dash.focus, 0, 0, w, h})
	} else {
		topH := max(h*11/20, 9)
		if w >= 110 {
			tw, kw := w*36/100, w*34/100
			pw := min(max(w*30/100, 34), 46)
			rects = append(rects, rect{pTree, 0, 0, tw, topH}, rect{pTasks, tw, 0, kw, topH}, rect{pStats, tw + kw, 0, w - tw - kw, topH},
				rect{pActivity, 0, topH, w - pw, h - topH}, rect{pPulse, w - pw, topH, pw, h - topH})
		} else {
			tw := w / 2
			rects = append(rects, rect{pTree, 0, 0, tw, topH}, rect{pTasks, tw, 0, w - tw, topH}, rect{pActivity, 0, topH, w, h - topH})
		}
	}
	const bodyY = 2
	for _, r := range rects {
		r := r
		m.zones = append(m.zones, zone{x0: r.x, y0: bodyY + r.y, x1: r.x + r.w, y1: bodyY + r.y + r.h, click: func(_, y int) tea.Cmd {
			m.dash.focus = r.p
			line := y - 1
			switch r.p {
			case pTree:
				if line >= 0 && line < len(m.dash.treeMap) {
					m.dash.sel, m.dash.taskSel = m.dash.treeMap[line], 0
				}
			case pTasks:
				if line >= 0 && line < len(m.dash.taskMap) {
					m.dash.taskSel = m.dash.taskMap[line]
				}
			}
			return nil
		}})
	}
}
