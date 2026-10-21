package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/storage"
)

// The Costs screen (owner-ui 6.4): the cost ledger over a range (today, 7 or 30 days) by day, agent, model and
// project, as bars with the share of the range total, and a sparkline of the days.

// costRanges are the ranges r cycles through, in days (1 is today since midnight).
var costRanges = []int{7, 30, 1}

// costsRefresh is how often the open Costs screen rereads the ledger.
const costsRefresh = 30 * time.Second

type costsState struct {
	view    *CostView
	err     string
	loading bool
	at      time.Time
	rng     int // index into costRanges
}

type costsMsg struct {
	view CostView
	err  error
}

// costsSince is the start of the selected range: midnight, days-1 days back.
func (m *Model) costsSince() time.Time {
	days := costRanges[m.costs.rng%len(costRanges)]
	return startOfDay(m.now().In(m.opts.Location)).AddDate(0, 0, -(days - 1))
}

func (m *Model) loadCosts() tea.Cmd {
	m.costs.loading = true
	c, ctx, since := m.c, m.ctx, m.costsSince()
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		v, err := c.Costs(rctx, since)
		return costsMsg{v, err}
	}
}

func (m *Model) onCosts(msg costsMsg) {
	m.costs.loading, m.costs.at = false, m.now()
	if msg.err != nil {
		m.costs.err = msg.err.Error()
		return
	}
	m.costs.err = ""
	v := msg.view
	m.costs.view = &v
}

func (s *costsState) key(m *Model, key string) tea.Cmd {
	if key == "r" {
		s.rng = (s.rng + 1) % len(costRanges)
		s.view = nil
		return m.loadCosts()
	}
	return nil
}

// groupCosts groups ledger rows recorded since a moment like GET /costs?by=… does (days in loc).
func groupCosts(rows []storage.RunCost, since time.Time, loc *time.Location) CostView {
	v := CostView{Since: since}
	for _, by := range costGroupings {
		r := v.report(by)
		r.Total = storage.CostGroup{Key: "total", Sources: map[string]int{}}
		groups := map[string]*storage.CostGroup{}
		for _, c := range rows {
			if c.RecordedAt < since.Unix() {
				continue
			}
			key := map[string]string{"agent": c.Agent, "model": c.Model, "project": c.ProjectID, "day": time.Unix(c.RecordedAt, 0).In(loc).Format("2006-01-02")}[by]
			for _, g := range []*storage.CostGroup{&r.Total, groups[key]} {
				if g == nil {
					g = &storage.CostGroup{Key: key, Sources: map[string]int{}}
					groups[key] = g
				}
				g.Runs++
				g.InputTokens += c.InputTokens
				g.OutputTokens += c.OutputTokens
				g.CacheReadTokens += c.CacheReadTokens
				g.CacheWriteTokens += c.CacheWriteTokens
				g.CostUSD += c.CostUSD
				g.Sources[c.CostSource]++
			}
		}
		for _, g := range groups {
			r.Groups = append(r.Groups, *g)
		}
		sort.Slice(r.Groups, func(i, j int) bool { return r.Groups[i].Key < r.Groups[j].Key })
	}
	return v
}

func (m *Model) costsLines(w, h int) []string {
	s := &m.costs
	days := costRanges[s.rng%len(costRanges)]
	rangeName := map[int]string{1: "today", 7: "last 7 days", 30: "last 30 days"}[days]
	if s.view == nil {
		body := []string{"", "  " + stFaint.Render("reading the cost ledger…")}
		if s.err != "" {
			body = []string{"", "  " + stRedBold.Render("✗ ") + s.err}
		}
		return panel("Costs · "+rangeName, body, w, h, true)
	}
	v := s.view
	total := v.Days.Total
	head := " " + stBold.Render("Costs · "+rangeName) + stFaint.Render(" since "+v.Since.In(m.opts.Location).Format("Jan 2")) + "   " +
		stGreen.Bold(true).Render(fmt.Sprintf("$%.2f", total.CostUSD)) + stFaint.Render(fmt.Sprintf(" · %d runs · %s tokens in · %s out", total.Runs, thousands(total.InputTokens+total.CacheReadTokens), thousands(total.OutputTokens)))
	if unknown := total.Sources["none"]; unknown > 0 {
		head += stYellow.Render(fmt.Sprintf(" · %d without a price", unknown))
	}
	if s.err != "" {
		head += stRed.Render("  ✗ " + s.err)
	}
	lines := []string{fit(head, w)}
	bodyH := h - 1
	dayLines := func(pw, ph int) []string {
		// Every day of the range, empty ones too, newest last; the sparkline above.
		byDay := map[string]storage.CostGroup{}
		for _, g := range v.Days.Groups {
			byDay[g.Key] = g
		}
		var keys []string
		var vals []float64
		for d := v.Since.In(m.opts.Location); !d.After(m.now()); d = d.AddDate(0, 0, 1) {
			k := d.Format("2006-01-02")
			keys = append(keys, k)
			vals = append(vals, byDay[k].CostUSD)
		}
		out := []string{stFaint.Render("spend ") + spark(vals, theme.green), ""}
		peak := 0.0 // day bars are scaled to the busiest day
		for _, x := range vals {
			peak = max(peak, x)
		}
		rows := []string{}
		for i, k := range keys {
			g := byDay[k]
			t, _ := time.ParseInLocation("2006-01-02", k, m.opts.Location)
			rows = append(rows, m.costRow(t.Format("Mon Jan 2"), g, peak, pw-4, i == len(keys)-1))
		}
		if room := ph - 2 - len(out); len(rows) > room {
			rows = rows[len(rows)-max(room, 0):] // the newest days
		}
		return append(out, rows...)
	}
	groupLines := func(r storage.CostReport, pw, ph int) []string {
		gs := append([]storage.CostGroup(nil), r.Groups...)
		sort.SliceStable(gs, func(i, j int) bool { return gs[i].CostUSD > gs[j].CostUSD })
		out := []string{}
		for _, g := range gs {
			if len(out) == ph-2 {
				break
			}
			out = append(out, m.costRow(dash(g.Key), g, total.CostUSD, pw-4, false))
		}
		if len(out) == 0 {
			out = append(out, stFaint.Render("no ended runs"))
		}
		return out
	}
	if w < 100 {
		// Narrow: the days, then agents and projects stacked.
		dh := max(bodyH/2, 6)
		rest := bodyH - dh
		gh := max(rest/2, 3)
		lines = append(lines, panel("By day", dayLines(w, dh), w, dh, false)...)
		lines = append(lines, hjoin(0, panel("By agent", groupLines(v.Agents, w/2, gh), w/2, gh, false), panel("By model", groupLines(v.Models, w-w/2, gh), w-w/2, gh, false))...)
		return append(lines, panel("By project", groupLines(v.Projects, w, rest-gh), w, rest-gh, false)...)
	}
	lw := w * 11 / 20
	rw := w - lw
	third := bodyH / 3
	right := append(append(panel("By agent", groupLines(v.Agents, rw, third), rw, third, false),
		panel("By model", groupLines(v.Models, rw, third), rw, third, false)...),
		panel("By project", groupLines(v.Projects, rw, bodyH-2*third), rw, bodyH-2*third, false)...)
	return append(lines, hjoin(0, panel("By day", dayLines(lw, bodyH), lw, bodyH, true), right)...)
}

// costRow is one group: its name, a bar of its cost against scale (the total, or the busiest day), its cost
// and runs.
func (m *Model) costRow(name string, g storage.CostGroup, scale float64, w int, today bool) string {
	nw := min(max(w/4, 10), 18)
	right := fmt.Sprintf("%8s %4d run%s", money(g.CostUSD), g.Runs, map[bool]string{true: "s", false: " "}[g.Runs != 1])
	if w < 50 {
		right = fmt.Sprintf("%8s", money(g.CostUSD))
	}
	bw := max(w-nw-len(right)-2, 4)
	c := theme.green
	if today {
		c = theme.accent
	}
	label := fit(name, nw)
	if name != "-" && !strings.Contains(name, " ") && agentKnown(m, name) {
		label = lipgloss.NewStyle().Foreground(agentColor(name)).Render(fit(name, nw))
	}
	return label + " " + hbar(int(g.CostUSD*100+0.5), int(scale*100+0.5), bw, c) + " " + stFaint.Render(right)
}

func agentKnown(m *Model, name string) bool {
	for _, a := range m.ov.Agents {
		if a.Name == name {
			return true
		}
	}
	return false
}
