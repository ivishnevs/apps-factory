package tui

import (
	"sort"
	"time"
)

// agentRow is one agent in its place in the reports_to tree and what it does now.
type agentRow struct {
	Agent
	depth       int
	tree        string // the tree prefix: ├ └ │
	state       string // running, queued, stopping, paused, idle
	task        string
	taskTitle   string
	run         string
	runStart    time.Time
	held        bool // task is an open task it holds, not a live run
	cost        float64
	runs        int
	activity    string
	at          time.Time
	pauseReason string
	waiting     string // what its open work waits on, when it is not running
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
			r.tree = indent + map[bool]string{true: "└ ", false: "├ "}[last]
			next := indent + map[bool]string{true: "  ", false: "│ "}[last]
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
			r.run, r.task, r.runStart = run.ID, run.TaskID, time.Unix(run.StartedAt, 0)
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
	if t, ok := m.taskByID(r.task); ok {
		r.taskTitle = t.Title
	}
	if r.run == "" {
		for _, p := range m.ov.Pauses {
			if p.Scope == "company" || p.Scope == "agent" && p.Target == a.Name {
				r.state, r.pauseReason = "paused", p.Reason
			}
		}
		best := -1 << 31
		for _, t := range m.ov.Tasks {
			if t.Assignee != a.Name || t.Kind == "conversation" || terminal(t.Status) || t.Status == "BACKLOG" {
				continue
			}
			// What it waits on: the owner first, then anyone else; never itself or plain queueing.
			if w := m.whyLine(t); w != "" && t.Reason != "capacity" && t.Clearer.Who != a.Name && (r.waiting == "" || t.Clearer.Who == "owner") {
				who := t.Clearer.Who
				if who == "owner" {
					who = "you"
				}
				r.waiting, r.at = "waits on "+who+" · "+t.ID, time.Unix(t.UpdatedAt, 0)
			}
			if t.Priority > best {
				best, r.task, r.taskTitle, r.held = t.Priority, t.ID, t.Title, true
			}
		}
	}
	for _, g := range m.ov.Costs.Groups {
		if g.Key == a.Name {
			r.cost, r.runs = g.CostUSD, g.Runs
		}
	}
	if l, ok := m.agentLast[a.Name]; ok && r.run != "" {
		r.activity = activityText(l)
		if r.at.IsZero() {
			r.at = l.At
		}
		return r
	}
	for i := len(m.feed) - 1; i >= 0; i-- {
		f := m.feed[i]
		if f.Agent == a.Name || f.Actor == a.Name {
			if r.at.IsZero() {
				r.at = f.CreatedAt
			}
			r.activity = f.Type
			if f.Summary != "" {
				r.activity = f.Summary
			}
			break
		}
	}
	return r
}
