package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// flowState is the flow view: who passes what to whom, as a timeline or as a graph of edges.
type flowState struct {
	filter FlowFilter
	graph  bool
	// scroll is how many lines above the newest the timeline shows; follow keeps it at 0.
	scroll int
	follow bool
}

func (f *flowState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "g":
		f.graph = !f.graph
	case "p":
		f.filter.Project = m.nextProject(f.filter.Project)
		f.scroll = 0
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
	return lipgloss.NewStyle()
}

func (m *Model) flowLines(w, h int) []string {
	f := &m.flow
	ins := interactions(m.feed, f.filter)
	filters := []string{"project " + orAll(f.filter.Project), "task " + orAll(f.filter.Task), "agent " + orAll(f.filter.Agent)}
	mode := "timeline"
	if f.graph {
		mode = "graph"
	}
	lines := []string{
		cut(stTitle.Render("Flow")+" "+stFaint.Render("who passes what to whom · "+mode+" · "+fmt.Sprintf("%d interactions", len(ins))), w),
		cut(stFaint.Render(strings.Join(filters, " · ")+" · p project · a agent · t task · x clear · g graph/timeline"), w),
	}
	if f.graph {
		return append(lines, m.flowGraph(ins, w)...)
	}
	if len(ins) == 0 {
		return append(lines, "", stFaint.Render("  no interactions yet"))
	}
	routeW := 12
	for _, in := range ins {
		routeW = max(routeW, len([]rune(in.From))+len([]rune(dash(in.To)))+3)
	}
	routeW = min(routeW, max(w*2/5, 20))
	const timeW, verbW, taskW = 6, 10, 7
	textW := max(w-timeW-routeW-verbW-taskW-1, 8)
	rows := make([]string, 0, len(ins))
	for _, in := range ins {
		to := in.To
		if to == "" {
			to = "·"
		}
		route := in.From + " → " + to
		text := strings.Trim(in.Text, " ")
		if in.Verb == "create" {
			text = "\"" + text + "\""
		}
		rows = append(rows, stFaint.Render(fit(in.At.In(m.opts.Location).Format("15:04"), timeW))+stBold.Render(col(route, routeW+1))+
			verbStyle(in.Verb, in.Attention).Render(col(verbLabel(in.Verb), verbW))+col(dash(in.Task), taskW)+cut(text, textW))
	}
	room := max(h-len(lines), 1)
	end := len(rows) - f.scroll
	if end < room {
		end = min(room, len(rows))
		f.scroll = len(rows) - end
	}
	start := max(end-room, 0)
	return append(lines, rows[start:end]...)
}

func (m *Model) flowGraph(ins []Interaction, w int) []string {
	es := edges(ins)
	if len(es) == 0 {
		return []string{"", stFaint.Render("  no interactions yet")}
	}
	fw, tw := 8, 8
	for _, e := range es {
		fw, tw = max(fw, len(e.From)), max(tw, len(e.To))
	}
	fw, tw = min(fw, 20), min(tw, 20)
	out := []string{"", stFaint.Render(cut(fit("FROM", fw)+"      "+fit("TO", tw)+fit("   N", 5)+fit("LAST", 7)+"KINDS", w))}
	for _, e := range es {
		out = append(out, cut(stBold.Render(fit(e.From, fw))+" ──▶  "+stBold.Render(fit(e.To, tw))+fmt.Sprintf("%4d ", e.Total)+
			stFaint.Render(fit(e.Last.In(m.opts.Location).Format("15:04"), 7))+e.verbsText(), w))
	}
	// Who works for whom, so the edges read against the reports_to tree.
	out = append(out, "", stBold.Render("reports_to"))
	for _, r := range m.agentRows() {
		boss := r.ReportsTo
		if boss == "" {
			boss = "owner"
		}
		out = append(out, cut("  "+r.tree+r.Name+stFaint.Render(" → "+boss), w))
	}
	return out
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
