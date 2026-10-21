package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/storage"
)

// The Nodes screen (owner-ui 6.2, 6.3): each registered node with its slots, held resources, drivers (enabled,
// transient failures, disabled until when and why), disk against its watermarks, pause and last heartbeat;
// the active pauses with resume and pause actions; the nodes' health events from the feed.

type nodesState struct {
	sel    int
	pauses bool // the pauses panel has the focus
	psel   int
}

// nodeHealth is a node's last heartbeat health (runtime protocol v2).
type nodeHealth struct {
	Load1             float64 `json:"load1"`
	CPUs              int     `json:"cpus"`
	FreeDiskBytes     uint64  `json:"free_disk_bytes"`
	BusySlots         int     `json:"busy_slots"`
	Disk              string  `json:"disk"`
	DiskLowBytes      uint64  `json:"disk_low_bytes"`
	DiskCriticalBytes uint64  `json:"disk_critical_bytes"`
	Checks            []struct {
		Check  string `json:"check"`
		Status string `json:"status"`
		Detail string `json:"detail"`
	} `json:"checks"`
	StalledSeconds int64 `json:"stalled_seconds"`
}

func healthOf(n Node) nodeHealth {
	var h nodeHealth
	_ = json.Unmarshal(n.Health, &h)
	return h
}

// nodePause is the owner's pause of the node, if any.
func (m *Model) nodePause(id string) (storage.Pause, bool) {
	for _, p := range m.ov.Pauses {
		if p.Scope == "node" && p.Target == id {
			return p, true
		}
	}
	return storage.Pause{}, false
}

// nodeCare says what of a node needs the owner ("" when nothing): offline, a disabled driver, the disk.
func (m *Model) nodeCare(n Node) string {
	h := healthOf(n)
	switch {
	case !n.Online:
		return "offline"
	case h.Disk == "critical":
		return "disk critical"
	}
	for _, d := range n.Drivers {
		if !d.OK {
			return d.Name + " disabled"
		}
	}
	if h.Disk == "low" {
		return "disk low"
	}
	return ""
}

func (m *Model) nodesNeedingCare() int {
	n := 0
	for _, x := range m.ov.Nodes {
		if m.nodeCare(x) != "" {
			n++
		}
	}
	return n
}

func gib(b uint64) string { return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30)) }

func (s *nodesState) key(m *Model, key string) tea.Cmd {
	nodes, pauses := m.ov.Nodes, m.ownerPauses()
	switch key {
	case "h", "left":
		s.pauses = false
	case "l", "right":
		s.pauses = true
	case "j", "down":
		if s.pauses {
			s.psel = min(s.psel+1, max(len(pauses)-1, 0))
		} else {
			s.sel = min(s.sel+1, max(len(nodes)-1, 0))
		}
	case "k", "up":
		if s.pauses {
			s.psel = max(s.psel-1, 0)
		} else {
			s.sel = max(s.sel-1, 0)
		}
	case "p":
		if !s.pauses && s.sel < len(nodes) {
			n := nodes[s.sel]
			return m.openPauseChooser("node", n.NodeID)
		}
		return m.openPauseChooser("", "")
	case "u", "enter":
		if s.pauses {
			if s.psel < len(pauses) {
				p := pauses[s.psel]
				m.askConfirm(Action{Kind: "release", Scope: p.Scope, Target: p.Target})
			}
			return nil
		}
		if s.sel < len(nodes) {
			if _, ok := m.nodePause(nodes[s.sel].NodeID); ok {
				m.askConfirm(Action{Kind: "release", Scope: "node", Target: nodes[s.sel].NodeID})
				return nil
			}
			if key == "u" {
				m.setToast(nodes[s.sel].NodeID+" is not paused by you", true)
			}
		}
	}
	return nil
}

// ownerPauses are the active pauses but the kill switch (the company pause has its own key, K).
func (m *Model) ownerPauses() []storage.Pause {
	out := []storage.Pause{}
	for _, p := range m.ov.Pauses {
		if p.Scope != "company" {
			out = append(out, p)
		}
	}
	return out
}

// openPauseChooser pauses a scope: the scope (unless given), the target (unless given), a reason, then the
// confirmation.
func (m *Model) openPauseChooser(scope, target string) tea.Cmd {
	if scope == "" {
		scopes := []string{"project", "agent", "quota_key", "node"}
		labels := []string{"a project: no new runs in it", "an agent: no new runs of it", "a quota key: the agents sharing a subscription", "a node: it claims no new runs"}
		m.chooser = &chooserState{title: "Pause", note: "Live runs continue; new runs wait until you resume (K is the kill switch).", options: labels,
			pick: func(i int) tea.Cmd { return m.openPauseChooser(scopes[i], "") }}
		return nil
	}
	if target == "" {
		var options []string
		switch scope {
		case "project":
			options = m.projects()
		case "agent":
			for _, a := range m.ov.Agents {
				options = append(options, a.Name)
			}
		case "node":
			for _, n := range m.ov.Nodes {
				options = append(options, n.NodeID)
			}
		}
		if len(options) == 0 { // quota keys are not listed: type it, then the reason
			cmd := m.openInput("Pause which "+scopeName(scope)+"?", "", Action{}, func(_ *Action, text string) error {
				if text == "" {
					return fmt.Errorf("a %s is required", scopeName(scope))
				}
				m.openPauseChooser(scope, text)
				return nil
			})
			m.input.local = true
			return cmd
		}
		sort.Strings(options)
		m.chooser = &chooserState{title: "Pause which " + scopeName(scope) + "?", note: "new runs stop; live runs continue", options: options,
			pick: func(i int) tea.Cmd { return m.openPauseChooser(scope, options[i]) }}
		return nil
	}
	return m.openInput("Pause "+scopeName(scope)+" "+target+" — reason (shown on the dashboard)", "", Action{Kind: "hold", Scope: scope, Target: target}, func(a *Action, text string) error {
		a.Text = text
		return nil
	})
}

func (m *Model) nodesLines(w, h int) []string {
	if !m.loaded {
		return m.notLoaded()
	}
	s := &m.nodes
	nodes := m.ov.Nodes
	s.sel = min(s.sel, max(len(nodes)-1, 0))
	pauses := m.ownerPauses()
	s.psel = min(s.psel, max(len(pauses)-1, 0))
	lw := w * 3 / 5
	if w < 110 {
		lw = w
	}
	cards := []string{}
	if len(nodes) == 0 {
		cards = append(cards, "", stFaint.Render("  no registered node: v1 workers only"))
	}
	for i, n := range nodes {
		cards = append(cards, m.nodeCard(n, lw-4, i == s.sel && !s.pauses)...)
	}
	if e := m.ov.Errors["nodes"]; e != "" {
		cards = append([]string{stRed.Render("✗ " + e)}, cards...)
	}
	if w < 110 {
		ph := min(len(pauses)+3, max(h/4, 4))
		eh := max(h/5, 4)
		nh := max(h-ph-eh, 6)
		ph = h - nh - eh
		out := panel(fmt.Sprintf("Nodes · %d", len(nodes)), m.scrollCards(cards, nh-2), w, nh, !s.pauses)
		out = append(out, panel(fmt.Sprintf("Pauses · %d", len(pauses)), m.pauseLines(pauses, w-4), w, ph, s.pauses)...)
		return append(out, panel("Node events", m.nodeEvents(w-4, eh-2), w, eh, false)...)
	}
	rw := w - lw
	ph := max(min(len(pauses)+4, h/2), 5)
	right := append(panel(fmt.Sprintf("Pauses · %d", len(pauses)), m.pauseLines(pauses, rw-4), rw, ph, s.pauses), panel("Node events", m.nodeEvents(rw-4, h-ph-2), rw, h-ph, false)...)
	return hjoin(0, panel(fmt.Sprintf("Nodes · %d", len(nodes)), m.scrollCards(cards, h-2), lw, h, !s.pauses), right)
}

// scrollCards keeps the selected node's card in view.
func (m *Model) scrollCards(lines []string, h int) []string {
	start := 0
	for i, l := range lines {
		if strings.HasPrefix(ansiStrip(l), "▸") {
			start = i
		}
	}
	off := scrollTo(start, 0, h, len(lines))
	if start-off > 0 && start+8 > off+h {
		off = min(start, max(len(lines)-h, 0))
	}
	return lines[off:]
}

// nodeCard is one node: the state line, then slots, resources, drivers, disk, load and pause.
func (m *Model) nodeCard(n Node, w int, selected bool) []string {
	now := m.now()
	h := healthOf(n)
	const kw = 9
	dot, state := stGreen.Render("●"), stGreen.Render("online")
	if !n.Online {
		dot, state = stRedBold.Render("○"), stRedBold.Render("offline")
	}
	pause, paused := m.nodePause(n.NodeID)
	if paused || h.Disk == "critical" {
		dot, state = stYellow.Render("⏸"), stYellow.Render("paused")
	}
	mark := " "
	name := stBold.Render(n.Name)
	if selected {
		mark = stAccent.Render("▸")
		name = stAccent.Bold(true).Render(n.Name)
	}
	label := name
	if n.Name != n.NodeID {
		label += stFaint.Render(" (" + n.NodeID + ")")
	}
	beat := stFaint.Render("beat " + ago(now, time.Unix(n.LastSeen, 0)) + " ago")
	if !n.Online {
		beat = stRed.Render("silent " + ago(now, time.Unix(n.LastSeen, 0)))
	}
	out := []string{rightAlign(mark+dot+" "+label+stFaint.Render(" · ")+state, beat, w)}
	ind := "   "
	row := func(k, v string) { out = append(out, ind+kv(k, v, kw)) }
	// Slots.
	used := n.InUse.Runs
	row("slots", hbar(used, max(n.Slots, 1), min(n.Slots*2, 16), theme.green)+fmt.Sprintf(" %d/%d in use", used, n.Slots))
	// Held resources.
	if len(n.Resources) > 0 {
		keys := make([]string, 0, len(n.Resources))
		for k := range n.Resources {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := []string{}
		for _, k := range keys {
			held := n.InUse.Resources[k]
			s := fmt.Sprintf("%s %d/%d", k, held, n.Resources[k])
			if held >= n.Resources[k] {
				s = stYellow.Render(s + " full")
			}
			parts = append(parts, s)
		}
		row("held", strings.Join(parts, stFaint.Render(" · ")))
	}
	// Drivers.
	for i, d := range n.Drivers {
		k := ""
		if i == 0 {
			k = "drivers"
		}
		var v string
		switch {
		case !d.OK:
			check := d.Check
			if check == "" {
				check = "probe"
			}
			v = stRedBold.Render("✗ "+d.Name) + stRed.Render(" disabled")
			if d.Until > now.Unix() {
				v += stRed.Render(" until " + m.clock(time.Unix(d.Until, 0)))
			}
			v += stFaint.Render(": " + check + " check failed")
			if d.Detail != "" {
				v += stFaint.Render(": " + oneLine(d.Detail))
			}
		case d.TransientFailures > 0:
			v = stYellow.Render("◐ "+d.Name) + stFaint.Render(" "+d.Version) + stYellow.Render(fmt.Sprintf(" enabled · %d transient failure%s", d.TransientFailures, map[bool]string{true: "s"}[d.TransientFailures > 1]))
		default:
			v = stGreen.Render("● "+d.Name) + stFaint.Render(" "+d.Version+" enabled")
		}
		out = append(out, ind+kv(k, cut(v, w-kw-3), kw))
	}
	if len(n.Drivers) == 0 {
		row("drivers", stRed.Render("none"))
	}
	// Disk against the watermarks.
	if h.FreeDiskBytes > 0 {
		v := gib(h.FreeDiskBytes) + " free"
		st := stFaint
		switch h.Disk {
		case "critical":
			st = stRedBold
			v += " · below critical: claiming paused"
		case "low":
			st = stYellow
			v += " · below low"
		}
		marks := ""
		if h.DiskLowBytes > 0 {
			marks = stFaint.Render(" (low " + gib(h.DiskLowBytes) + ", critical " + gib(h.DiskCriticalBytes) + ")")
		}
		row("disk", st.Render(v)+marks)
	}
	if h.CPUs > 0 {
		row("load", fmt.Sprintf("%.1f on %d cpus", h.Load1, h.CPUs))
	}
	for _, c := range h.Checks {
		if c.Status == "fail" {
			row("check", stRed.Render("✗ "+c.Check)+stFaint.Render(" "+oneLine(c.Detail)))
		}
	}
	if paused {
		r := "by the owner"
		if pause.Reason != "" {
			r += ": " + pause.Reason
		}
		row("paused", stYellow.Render(r)+stFaint.Render(" · "+ago(now, time.Unix(pause.CreatedAt, 0))+" ago · u resumes"))
	}
	return append(out, "")
}

func (m *Model) pauseLines(pauses []storage.Pause, w int) []string {
	now := m.now()
	lines := []string{}
	if p, stopped := m.companyStopped(); stopped {
		lines = append(lines, stRedBold.Render("■ company STOPPED")+stFaint.Render(" "+dash(p.Reason)+" · K starts it"), "")
	}
	if len(pauses) == 0 {
		lines = append(lines, stFaint.Render("no pauses · p pauses a project, agent, quota key or node"))
	}
	for i, p := range pauses {
		what := stYellow.Render("⏸ "+scopeName(p.Scope)) + " " + stBold.Render(p.Target)
		meta := p.Actor + " · " + ago(now, time.Unix(p.CreatedAt, 0)) + " ago"
		if p.Until > 0 {
			meta += " · until " + m.clock(time.Unix(p.Until, 0))
		}
		line := rightAlign(what+stFaint.Render("  "+dash(p.Reason)), stFaint.Render(meta), w)
		if i == m.nodes.psel && m.nodes.pauses {
			line = lipgloss.NewStyle().Background(theme.surfaceHi).Render(line)
		}
		lines = append(lines, line)
	}
	return lines
}

// nodeEvents are the latest feed rows about nodes (health, drivers, pauses), attention first marked.
func (m *Model) nodeEvents(w, h int) []string {
	rows := []string{}
	for i := len(m.feed) - 1; i >= 0 && len(rows) < h; i-- {
		f := m.feed[i]
		if !strings.HasPrefix(f.EntityID, "node:") && !strings.HasPrefix(f.Type, "node.") && !strings.HasPrefix(f.Type, "driver_") {
			continue
		}
		icon := stFaint.Render("·")
		st := stFaint
		if f.Attention {
			icon, st = stRedBold.Render("⚠"), lipgloss.NewStyle().Foreground(theme.text)
		}
		rows = append(rows, cut(stDim.Render(m.clock(f.CreatedAt))+" "+icon+" "+stYellow.Render(f.Type)+" "+st.Render(oneLine(f.Summary)), w))
	}
	if len(rows) == 0 {
		rows = append(rows, stFaint.Render("no node events"))
	}
	return rows
}
