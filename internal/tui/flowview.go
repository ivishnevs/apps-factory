package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// flowState is the flow view: who passes what to whom, as swimlanes over time or as pairs.
type flowState struct {
	filter FlowFilter
	pairs  bool
	// scroll is how many rows above the newest the lanes show; follow keeps it at 0.
	scroll int
	follow bool
}

func (f *flowState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "g":
		f.pairs = !f.pairs
	case "a":
		return m.openSetting("Flow: agent (empty for all)", f.filter.Agent, func(s string) { f.filter.Agent, f.scroll = s, 0 })
	case "t":
		return m.openSetting("Flow: task (empty for all)", f.filter.Task, func(s string) { f.filter.Task, f.scroll = s, 0 })
	case "x", "esc":
		f.filter, f.scroll = FlowFilter{}, 0
	case "k", "up":
		f.scroll++
		f.follow = false
	case "j", "down":
		f.scroll = max(f.scroll-1, 0)
		f.follow = f.scroll == 0
	case "pgup":
		f.scroll += m.height / 2
		f.follow = false
	case "pgdown", "space":
		f.scroll = max(f.scroll-m.height/2, 0)
		f.follow = f.scroll == 0
	case "G", "end":
		f.scroll, f.follow = 0, true
	}
	return nil
}

func verbStyle(verb string, attention bool) lipgloss.Style {
	if attention {
		return stRedBold
	}
	switch verb {
	case "create":
		return stCyan
	case "handoff", "submit", "forwarded":
		return stMagenta
	case "approved", "answer", "done":
		return stGreen
	case "changes_requested", "directive", "blocked", "escalate", "escalated", "reassigned", "paused":
		return stYellow
	case "question", "asked", "approval":
		return stBlue
	}
	return lipgloss.NewStyle().Foreground(theme.text)
}

// verbLabel shortens the long verbs for the timeline column.
func verbLabel(v string) string {
	switch v {
	case "changes_requested":
		return "changes"
	case "review_result":
		return "review"
	case "reprioritized":
		return "priority"
	}
	return v
}

func orAll(s string) string {
	if s == "" {
		return "all"
	}
	return s
}

// lanes are the parties of the interactions: the owner, then the agents in reports_to order, then others.
func (m *Model) lanes(ins []Interaction) []string {
	seen := map[string]bool{}
	for _, in := range ins {
		seen[in.From] = true
		if in.To != "" {
			seen[in.To] = true
		}
	}
	out := []string{}
	if seen["owner"] {
		out = append(out, "owner")
		delete(seen, "owner")
	}
	for _, r := range m.agentRows() {
		if seen[r.Name] {
			out = append(out, r.Name)
			delete(seen, r.Name)
		}
	}
	rest := []string{}
	for k := range seen {
		rest = append(rest, k)
	}
	sortStrings(rest)
	return append(out, rest...)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (m *Model) flowLines(w, h int) []string {
	f := &m.flow
	filter := f.filter
	filter.Project = m.project
	ins := interactions(m.feed, filter)
	inner := w - 4
	mode := "swimlanes"
	if f.pairs {
		mode = "pairs"
	}
	title := "Flow · " + mode + stFaint.Render(fmt.Sprintf(" · %d steps · project %s · agent %s · task %s", len(ins), orAll(m.project), orAll(f.filter.Agent), orAll(f.filter.Task)))
	if len(ins) == 0 {
		return panel(title, []string{"", stFaint.Render("  no interactions yet")}, w, h, true)
	}
	if f.pairs {
		return panel(title, m.flowPairs(ins, inner), w, h, true)
	}
	lanes := m.lanes(ins)
	const timeW = 6
	labelW := max(inner*40/100, 30)
	laneW := (inner - timeW - labelW) / max(len(lanes), 1)
	if laneW < 5 {
		return panel(title, m.flowTimeline(ins, inner, h-2), w, h, true)
	}
	laneArea := laneW * len(lanes)
	labelW = inner - timeW - laneArea
	center := func(i int) int { return i*laneW + laneW/2 }
	idx := map[string]int{}
	for i, l := range lanes {
		idx[l] = i
	}
	// Header: lane names in their colors centered on their lanes; when names are wider than a lane they
	// alternate between two lines, so each has two lanes of room.
	stagger := false
	for _, l := range lanes {
		if len([]rune(l)) > laneW-1 {
			stagger = true
		}
	}
	headRows := 1
	if stagger {
		headRows = 2
	}
	header := []string{}
	for row := 0; row < headRows; row++ {
		line, cursor := strings.Repeat(" ", timeW), 0
		for i, l := range lanes {
			if stagger && i%2 != row {
				continue
			}
			room := laneW - 1
			if stagger {
				room = 2*laneW - 1
			}
			name := []rune(cut(l, room))
			start := max(center(i)-len(name)/2, cursor)
			start = min(start, max(laneArea-len(name), cursor))
			line += strings.Repeat(" ", start-cursor) + lipgloss.NewStyle().Foreground(agentColor(l)).Bold(true).Render(string(name))
			cursor = start + len(name)
		}
		header = append(header, line)
	}
	rows := []string{}
	for _, in := range ins {
		cells := make([]string, laneArea)
		for i := range cells {
			cells[i] = " "
		}
		for i := range lanes {
			cells[center(i)] = stDim.Render("│")
		}
		fi, fok := idx[in.From]
		ti, tok := idx[in.To]
		ac := lipgloss.NewStyle().Foreground(agentColor(in.From))
		if fok && tok && fi != ti {
			a, b := center(fi), center(ti)
			lo, hi := min(a, b), max(a, b)
			for c := lo; c <= hi; c++ {
				cells[c] = ac.Render("─")
			}
			cells[a] = ac.Render("●")
			if b > a {
				cells[b] = ac.Render("▶")
			} else {
				cells[b] = ac.Render("◀")
			}
		} else if fok {
			cells[center(fi)] = ac.Render("◆")
		}
		text := in.Text
		if in.Verb == "create" {
			text = "“" + text + "”"
		}
		label := verbStyle(in.Verb, in.Attention).Render(fit(verbLabel(in.Verb), 10)) + stFaint.Render(fit(dash(in.Task), 7)) + text
		rows = append(rows, stDim.Render(fit(in.At.In(m.opts.Location).Format("15:04"), timeW))+strings.Join(cells, "")+" "+cut(label, labelW-1))
	}
	room := max(h-2-len(header), 1)
	end := len(rows) - f.scroll
	if end < room {
		end = min(room, len(rows))
		f.scroll = len(rows) - end
	}
	return panel(title, append(header, rows[max(end-room, 0):end]...), w, h, true)
}

// flowTimeline is the flow as a list when the lanes do not fit.
func (m *Model) flowTimeline(ins []Interaction, w, h int) []string {
	rows := []string{}
	for _, in := range ins {
		to := in.To
		if to == "" {
			to = "·"
		}
		route := lipgloss.NewStyle().Foreground(agentColor(in.From)).Render(in.From) + stFaint.Render(" → ") + lipgloss.NewStyle().Foreground(agentColor(to)).Render(to)
		rows = append(rows, cut(stDim.Render(in.At.In(m.opts.Location).Format("15:04"))+" "+fit(route, 34)+verbStyle(in.Verb, in.Attention).Render(fit(verbLabel(in.Verb), 10))+stFaint.Render(fit(dash(in.Task), 7))+in.Text, w))
	}
	end := len(rows) - m.flow.scroll
	if end < h {
		end = min(h, len(rows))
		m.flow.scroll = len(rows) - end
	}
	return rows[max(end-h, 0):end]
}

func (m *Model) flowPairs(ins []Interaction, w int) []string {
	es := edges(ins)
	fw, tw := 8, 8
	for _, e := range es {
		fw, tw = max(fw, len(e.From)), max(tw, len(e.To))
	}
	fw, tw = min(fw, 20), min(tw, 20)
	most := 1
	for _, e := range es {
		most = max(most, e.Total)
	}
	out := []string{stFaint.Render(fit("FROM", fw) + "      " + fit("TO", tw) + fit("  N", 12) + fit("LAST", 7) + "KINDS")}
	for _, e := range es {
		bar := lipgloss.NewStyle().Foreground(agentColor(e.From)).Render(strings.Repeat("▇", max(e.Total*6/most, 1)))
		out = append(out, cut(lipgloss.NewStyle().Foreground(agentColor(e.From)).Render(fit(e.From, fw))+stFaint.Render(" ──▶  ")+lipgloss.NewStyle().Foreground(agentColor(e.To)).Render(fit(e.To, tw))+
			fmt.Sprintf("%3d ", e.Total)+fit(bar, 8)+stFaint.Render(fit(e.Last.In(m.opts.Location).Format("15:04"), 7))+e.verbsText(), w))
	}
	out = append(out, "", stTitle.Render("▌ reports_to"))
	for _, r := range m.agentRows() {
		boss := r.ReportsTo
		if boss == "" {
			boss = "owner"
		}
		out = append(out, cut("  "+stDim.Render(r.tree)+lipgloss.NewStyle().Foreground(agentColor(r.Name)).Render(r.Name)+stFaint.Render(" → "+boss), w))
	}
	return out
}
