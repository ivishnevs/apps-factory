package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type dashState struct {
	cursor int
}

// agentRow is one agent of the dashboard: its place in the reports_to tree and what it does now.
type agentRow struct {
	Agent
	depth    int
	tree     string // the tree prefix: ├ └ │
	state    string // running, queued, stopping, paused, idle
	task     string
	run      string
	held     bool // task is an open task it holds, not a live run
	cost     float64
	activity string
	at       time.Time
}

func (m *Model) agentRows() []agentRow {
	byManager := map[string][]Agent{}
	known := map[string]bool{}
	for _, a := range m.ov.Agents {
		known[a.Name] = true
	}
	for _, a := range m.ov.Agents {
		boss := a.ReportsTo
		if boss == "" || !known[boss] {
			boss = "owner"
		}
		byManager[boss] = append(byManager[boss], a)
	}
	for _, list := range byManager {
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	}
	rows := []agentRow{}
	var walk func(boss string, depth int, indent string)
	walk = func(boss string, depth int, indent string) {
		list := byManager[boss]
		for i, a := range list {
			r := m.agentRow(a, depth)
			last := i == len(list)-1
			next := indent
			if depth > 0 {
				r.tree = indent + map[bool]string{true: "└ ", false: "├ "}[last]
				next += map[bool]string{true: "  ", false: "│ "}[last]
			}
			rows = append(rows, r)
			if depth < 8 {
				walk(a.Name, depth+1, next)
			}
		}
	}
	walk("owner", 0, "")
	return rows
}

func (m *Model) agentRow(a Agent, depth int) agentRow {
	r := agentRow{Agent: a, depth: depth, state: "idle"}
	for _, p := range m.ov.Status {
		for _, run := range p.Runs {
			if run.Agent != a.Name || r.run != "" {
				continue
			}
			r.run, r.task = run.ID, run.TaskID
			switch run.Status {
			case "QUEUED":
				r.state = "queued"
			case "CANCELLING":
				r.state = "stopping"
			default:
				r.state = "running"
			}
		}
	}
	if r.run == "" {
		for _, p := range m.ov.Pauses {
			if p.Scope == "company" || p.Scope == "agent" && p.Target == a.Name {
				r.state = "paused"
			}
		}
		best := -1 << 31
		for _, t := range m.ov.Tasks {
			if t.Assignee == a.Name && t.Kind != "conversation" && t.Status != "DONE" && t.Status != "CANCELLED" && t.Status != "BACKLOG" && t.Priority > best {
				best, r.task, r.held = t.Priority, t.ID, true
			}
		}
	}
	for _, g := range m.ov.Costs.Groups {
		if g.Key == a.Name {
			r.cost = g.CostUSD
		}
	}
	if l, ok := m.agentLast[a.Name]; ok && r.run != "" {
		r.activity, r.at = l.Text, l.At
		if l.Kind == lineShell || l.Kind == lineMCP || l.Kind == lineExit {
			r.activity = l.Text
		}
		return r
	}
	for i := len(m.feed) - 1; i >= 0; i-- {
		f := m.feed[i]
		if f.Agent == a.Name || f.Actor == a.Name {
			r.activity, r.at = f.Type, f.CreatedAt
			if f.Summary != "" {
				r.activity = f.Summary
			}
			break
		}
	}
	return r
}

func (d *dashState) key(m *Model, key string) tea.Cmd {
	rows := m.agentRows()
	switch key {
	case "j", "down":
		d.cursor = min(d.cursor+1, len(rows)-1)
	case "k", "up":
		d.cursor = max(d.cursor-1, 0)
	case "g", "home":
		d.cursor = 0
	case "G", "end":
		d.cursor = max(len(rows)-1, 0)
	case "enter":
		if d.cursor < len(rows) {
			r := rows[d.cursor]
			if r.run != "" {
				return m.openRun(r.run)
			}
			m.tasks.setFilter(m, "a:"+r.Name)
			return m.switchTab(tabTasks)
		}
	case "t":
		if d.cursor < len(rows) {
			m.tasks.setFilter(m, "a:"+rows[d.cursor].Name)
			return m.switchTab(tabTasks)
		}
	case "f":
		if d.cursor < len(rows) {
			m.flow.filter = FlowFilter{Agent: rows[d.cursor].Name}
			return m.switchTab(tabFlow)
		}
	}
	return nil
}

func (m *Model) dashboardLines(w, h int) []string {
	if !m.loaded {
		if m.ovErr != nil {
			return []string{"", "  Core is unreachable: " + m.ovErr.Error(), "", "  Retrying every few seconds. Check FACTORY_URL and FACTORY_TOKEN, or try factory ui --demo."}
		}
		return []string{"", "  Loading…"}
	}
	now := m.now()
	rows := m.agentRows()
	if m.dash.cursor >= len(rows) {
		m.dash.cursor = max(len(rows)-1, 0)
	}
	nameW := 10
	for _, r := range rows {
		nameW = max(nameW, ansiWidth(r.tree+r.Name)+2)
	}
	nameW = min(nameW, 24)
	runW := 0
	if w >= 100 {
		runW = 10
	}
	const stateW, taskW, costW, ageW = 9, 8, 7, 5
	actW := max(w-nameW-stateW-taskW-runW-costW-ageW, 10)
	header := fit("AGENT", nameW) + fit("STATE", stateW) + fit("TASK", taskW)
	if runW > 0 {
		header += fit("RUN", runW)
	}
	header += fit("TODAY", costW) + fit("AGE", ageW) + "LAST ACTIVITY"
	lines := []string{stTitle.Render("Agents") + stFaint.Render(fmt.Sprintf("  %d agents · reports_to tree · enter: run/tasks · t tasks · f flow", len(rows))), stFaint.Render(cut(header, w))}

	summary := m.summaryLines(w, now)
	room := max(h-len(lines)-len(summary)-1, 3)
	start := 0
	if m.dash.cursor >= room {
		start = m.dash.cursor - room + 1
	}
	for i := start; i < len(rows) && i < start+room; i++ {
		r := rows[i]
		name := r.tree + r.Name
		task := r.task
		taskCell := fit(dash(task), taskW)
		if r.held {
			taskCell = stFaint.Render(taskCell)
		}
		line := fit(name, nameW) + statusStyle(r.state).Render(fit(r.state, stateW)) + taskCell
		if runW > 0 {
			line += fit(dash(r.run), runW)
		}
		line += fit(money(r.cost), costW) + stFaint.Render(fit(ago(now, r.at), ageW)) + cut(r.activity, actW)
		if i == m.dash.cursor {
			line = stSelected.Render(fit(stripStyles(line), w))
		}
		lines = append(lines, line)
	}
	lines = append(lines, "")
	return append(lines, summary...)
}

// summaryLines are the dashboard's task counts, attention counters and node strip.
func (m *Model) summaryLines(w int, now time.Time) []string {
	counts := map[string]int{}
	for _, p := range m.ov.Status {
		for s, n := range p.Tasks {
			counts[s] += n
		}
	}
	order := []string{"IN_PROGRESS", "IN_REVIEW", "READY", "BLOCKED", "BACKLOG", "DONE", "CANCELLED"}
	parts := []string{}
	for _, s := range order {
		if counts[s] > 0 {
			parts = append(parts, statusStyle(s).Render(fmt.Sprintf("%s %d", s, counts[s])))
			delete(counts, s)
		}
	}
	for s, n := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", s, n))
	}
	waiting, runnable := 0, 0
	for _, p := range m.ov.Status {
		waiting += len(p.Waiting)
		runnable += p.Runnable
	}
	out := []string{stBold.Render("Tasks     ") + strings.Join(parts, " · ") + stFaint.Render(fmt.Sprintf("  (runnable %d)", runnable))}

	pc := m.ov.Pending
	attention := 0
	for _, f := range m.feed {
		if f.Attention && now.Sub(f.CreatedAt) < 24*time.Hour {
			attention++
		}
	}
	att := fmt.Sprintf("inbox %d · approvals %d · alerts %d · inspect %d · in review %d · feed ! %d (24h)", pc.Inbox, pc.Approvals, pc.Alerts, pc.Inspect, pc.Reviews, attention)
	attStyle := stFaint
	if pc.Total > 0 || attention > 0 {
		attStyle = stRedBold
	}
	out = append(out, stBold.Render("Attention ")+attStyle.Render(att))

	nodes := []string{}
	for _, n := range m.ov.Nodes {
		name := n.Name
		if name == "" {
			name = n.NodeID
		}
		var hl struct {
			Load1  float64 `json:"load1"`
			Checks []struct {
				Check  string `json:"check"`
				Status string `json:"status"`
			} `json:"checks"`
		}
		_ = json.Unmarshal(n.Health, &hl)
		failing := []string{}
		for _, c := range hl.Checks {
			if c.Status == "fail" {
				failing = append(failing, c.Check)
			}
		}
		var s string
		if n.Online {
			s = stGreen.Render("● "+name) + fmt.Sprintf(" %d/%d load %.1f", n.InUse.Runs, n.Slots, hl.Load1)
		} else {
			s = stRed.Render("○ "+name) + " offline " + ago(now, time.Unix(n.LastSeen, 0))
		}
		if len(failing) > 0 {
			s += stRedBold.Render(" fail:" + strings.Join(failing, ","))
		}
		nodes = append(nodes, s)
	}
	if len(nodes) == 0 {
		nodes = append(nodes, stFaint.Render("no nodes registered"))
	}
	out = append(out, stBold.Render("Nodes     ")+strings.Join(nodes, " · "))
	pauses := []string{}
	for _, p := range m.ov.Pauses {
		t := p.Scope
		if p.Target != "" {
			t += " " + p.Target
		}
		if p.Reason != "" {
			t += ": " + p.Reason
		}
		pauses = append(pauses, t)
	}
	if len(pauses) > 0 {
		out = append(out, stBold.Render("Pauses    ")+stYellow.Render(strings.Join(pauses, " · ")))
	}
	for i := range out {
		out[i] = cut(out[i], w)
	}
	return out
}

// stripStyles drops ANSI styles (a selected row is re-styled as a whole).
func stripStyles(s string) string { return ansiStrip(s) }
