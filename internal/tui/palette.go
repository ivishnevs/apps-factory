package tui

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// The command palette (ctrl+k): fuzzy search over tasks, agents, live runs, projects and actions.

type paletteItem struct {
	icon, kind, label, detail string
	run                       func() tea.Cmd
}

type paletteState struct {
	ti     textinput.Model
	items  []paletteItem
	cursor int
}

func (m *Model) openPalette() {
	ti := textinput.New()
	ti.Prompt = "❯ "
	ti.Placeholder = "tasks, agents, runs, projects, actions…"
	ti.SetWidth(max(min(m.width-16, 64), 10))
	m.palette = &paletteState{ti: ti, items: m.paletteItems()}
}

func (m *Model) paletteItems() []paletteItem {
	items := []paletteItem{}
	for i, name := range tabNames {
		t := tab(i)
		items = append(items, paletteItem{"→", "go", "Go to " + name, fmt.Sprint(i + 1), func() tea.Cmd { return m.switchTab(t) }})
	}
	if _, stopped := m.companyStopped(); stopped {
		items = append(items, paletteItem{"▶", "action", "Start the company", "lift the kill switch", func() tea.Cmd { m.askConfirm(Action{Kind: "start"}); return nil }})
	} else {
		items = append(items, paletteItem{"■", "action", "Stop the company (kill switch)", "K", func() tea.Cmd {
			return m.openInput("⏻ Kill switch — stop the company. Reason", "", Action{Kind: "stop"}, func(a *Action, text string) error { a.Text = text; return nil })
		}})
	}
	items = append(items,
		paletteItem{"↻", "action", "Refresh now", "R", func() tea.Cmd { return m.loadOverview() }},
		paletteItem{"⚠", "action", "Show what needs me", "board filter !", func() tea.Cmd { m.board.setFilter(m, "!"); return m.switchTab(tabBoard) }},
		paletteItem{"⏸", "action", "Pause a project, agent, quota key or node…", "nodes p", func() tea.Cmd { return m.openPauseChooser("", "") }},
	)
	for _, p := range m.ov.Pauses {
		if p.Scope == "company" {
			continue
		}
		p := p
		items = append(items, paletteItem{"▶", "action", "Resume " + scopeName(p.Scope) + " " + p.Target, dash(p.Reason), func() tea.Cmd {
			m.askConfirm(Action{Kind: "release", Scope: p.Scope, Target: p.Target})
			return nil
		}})
	}
	if msg, ok := m.promptItem(); ok && !answerable(msg) && msg.ApprovalID == "" {
		items = append(items, paletteItem{"↩", "action", "Reply: " + oneLine(msg.Body), msg.Sender, func() tea.Cmd { return m.replyTo(msg) }},
			paletteItem{"✓", "action", "Mark read: " + oneLine(msg.Body), msg.Sender, func() tea.Cmd { return m.markRead(msg) }})
	} else if ok {
		items = append(items, paletteItem{"?", "action", "Answer: " + oneLine(msg.Body), msg.Sender, func() tea.Cmd {
			if cmd, ok := m.promptKey("A"); ok {
				return cmd
			}
			if cmd, ok := m.promptKey("Y"); ok {
				return cmd
			}
			return nil
		}})
	}
	items = append(items, paletteItem{"◇", "project", "All projects", "", func() tea.Cmd { m.project = ""; return nil }})
	for _, p := range m.projects() {
		p := p
		items = append(items, paletteItem{"◇", "project", p, "show only " + p, func() tea.Cmd { m.project = p; m.board.clamp(m); return nil }})
	}
	for i, r := range m.treeRows() {
		if r.name == "" {
			continue
		}
		i, r := i, r
		items = append(items, paletteItem{"●", "agent", r.name, r.agent.Role + " · " + r.agent.state, func() tea.Cmd {
			m.dash.sel, m.dash.focus = i, pTree
			return m.switchTab(tabDashboard)
		}})
	}
	for _, lr := range m.liveRuns() {
		lr := lr
		items = append(items, paletteItem{"▶", "run", lr.ID, lr.Agent + " · " + lr.TaskID, func() tea.Cmd { return m.openRun(lr.ID) }})
	}
	for _, t := range m.ov.Tasks {
		if t.Kind == "conversation" {
			continue
		}
		t := t
		items = append(items, paletteItem{"□", "task", t.ID + " " + t.Title, t.Status + " · " + t.Assignee, func() tea.Cmd { return m.openDetail(t.ID) }})
	}
	return items
}

// fuzzy scores how well query matches s as a subsequence (0: no match); consecutive and word-start
// matches score higher.
func fuzzy(query, s string) int {
	if query == "" {
		return 1
	}
	q := []rune(strings.ToLower(query))
	r := []rune(strings.ToLower(s))
	score, qi, run := 0, 0, 0
	for i := 0; i < len(r) && qi < len(q); i++ {
		if r[i] != q[qi] {
			run = 0
			continue
		}
		run++
		score += 1 + run*2
		if i == 0 || !unicode.IsLetter(r[i-1]) && !unicode.IsDigit(r[i-1]) {
			score += 4
		}
		qi++
	}
	if qi < len(q) {
		return 0
	}
	return score
}

func (p *paletteState) matches() []paletteItem {
	q := strings.TrimSpace(p.ti.Value())
	type scored struct {
		item  paletteItem
		score int
	}
	list := []scored{}
	for _, it := range p.items {
		if s := max(fuzzy(q, it.label), fuzzy(q, it.kind+" "+it.label)-1); s > 0 {
			list = append(list, scored{it, s})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	out := make([]paletteItem, 0, len(list))
	for _, s := range list {
		out = append(out, s.item)
	}
	return out
}

func (p *paletteState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc", "ctrl+k":
		m.palette = nil
		return nil
	case "down", "ctrl+n", "ctrl+j":
		p.cursor++
		return nil
	case "up", "ctrl+p":
		p.cursor = max(p.cursor-1, 0)
		return nil
	case "enter":
		list := p.matches()
		m.palette = nil
		if len(list) == 0 {
			return nil
		}
		return list[min(p.cursor, len(list)-1)].run()
	}
	var cmd tea.Cmd
	p.ti, cmd = p.ti.Update(k)
	p.cursor = 0
	return cmd
}

func (m *Model) paletteLines(w, h int) []string {
	p := m.palette
	list := p.matches()
	p.cursor = min(p.cursor, max(len(list)-1, 0))
	room := max(min(h-6, 12), 3)
	lines := []string{p.ti.View(), rule(w - 4)}
	first := scrollTo(p.cursor, 0, room, len(list))
	for i := first; i < len(list) && i < first+room; i++ {
		it := list[i]
		row := " " + stAccent.Render(it.icon) + " " + fit(stFaint.Render(it.kind), 8) + it.label
		row = rightAlign(row, stDim.Render(it.detail), w-4)
		if i == p.cursor {
			row = lipgloss.NewStyle().Background(theme.surfaceHi).Render(fit(row, w-4))
		}
		lines = append(lines, row)
	}
	if len(list) == 0 {
		lines = append(lines, stFaint.Render("  no match"))
	}
	return frame(stTitle.Render("Command palette"), lines, w, len(lines)+2, theme.accent, true, false)
}
