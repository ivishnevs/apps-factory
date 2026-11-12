package core

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"appfactory/internal/skills"
	"appfactory/internal/storage"
	"appfactory/internal/support"
)

// Support reads for the owner's own coding agent and `factory doctor --company` (setup-assistant 1.1, 3.1): the company
// doctor, recent runs with their causes and usage in words, and what an agent really gets on a run. Owner routes.
func (c *Core) supportRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /doctor", func(w http.ResponseWriter, r *http.Request) {
		since := 24 * time.Hour
		if v := r.URL.Query().Get("since"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				http.Error(w, "since: a duration such as 24h", 400)
				return
			}
			since = d
		}
		v, err := c.CompanyDoctor(r.Context(), c.companyFrom(r), since)
		reply(w, v, err)
	})
	m.HandleFunc("GET /runs/recent", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		since := 24 * time.Hour
		if v := q.Get("since"); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				http.Error(w, "since: a duration such as 24h", 400)
				return
			}
			since = d
		}
		limit, _ := strconv.Atoi(q.Get("limit"))
		runs, err := c.Store.RecentRuns(r.Context(), c.companyFrom(r), c.Store.Now().Add(-since).Unix(), limit, q.Get("failed") != "")
		if err != nil {
			reply(w, nil, err)
			return
		}
		type explained struct {
			storage.RunSummary
			Why        *support.Answer `json:"why,omitempty"`
			UsageWords string          `json:"usage_explained"`
		}
		out := make([]explained, 0, len(runs))
		for _, run := range runs {
			e := explained{RunSummary: run, UsageWords: support.Usage(run.Usage)}
			if run.Status == "ENDED" && run.Outcome != "succeeded" {
				a := support.Outcome(run.Outcome, run.NodeFault, run.Result)
				e.Why = &a
			}
			out = append(out, e)
		}
		write(w, out)
	})
	// What the agent's runs get: its instructions as the catalog composes them (prompt, policies, skills list,
	// hierarchy), its skills with their sources and pinned hashes, and the Factory MCP contract every run appends.
	m.HandleFunc("GET /agents/{agent}/effective", func(w http.ResponseWriter, r *http.Request) {
		cat := c.cat(c.companyFrom(r))
		a, ok := cat.Agents[r.PathValue("agent")]
		if !ok {
			http.Error(w, "agent not found", 404)
			return
		}
		type skill struct {
			skills.Entry
			Source string `json:"source,omitempty"`
			Hash   string `json:"hash,omitempty"`
		}
		out := struct {
			Agent        string   `json:"agent"`
			Team         string   `json:"team,omitempty"`
			Role         string   `json:"role,omitempty"`
			Provider     string   `json:"provider"`
			Model        string   `json:"model,omitempty"`
			Instructions string   `json:"instructions"`
			Skills       []skill  `json:"skills"`
			Policies     []string `json:"policies"`
			RunContract  string   `json:"run_contract"`
		}{Agent: a.Metadata.Name, Team: a.Spec.Team, Role: a.Spec.Role, Provider: a.Spec.Runtime.Provider, Model: a.Spec.Runtime.Model,
			Instructions: a.Instructions, Skills: []skill{}, Policies: a.Spec.Policies,
			RunContract: "Every run appends the Factory MCP contract (tools, identity bound to the run), the run's brief (task, triggers, company and project rules) and, with explicit exits, the exit instructions."}
		if cat.Skills != nil {
			pins := cat.Skills.Pins(a.AvailableSkills)
			for _, e := range cat.Skills.Entries(a.AvailableSkills, "", "") {
				out.Skills = append(out.Skills, skill{Entry: e, Source: cat.Skills.Skills[e.Name].Source, Hash: pins[e.Name]})
			}
		}
		write(w, out)
	})
}

// CompanyDoctor judges company's state now; failed runs count over the last since.
func (c *Core) CompanyDoctor(ctx context.Context, company string, since time.Duration) (support.CompanyDoctor, error) {
	now := c.Store.Now()
	in := support.DoctorInput{Company: company, Now: now, Since: now.Add(-since)}
	nodes, err := c.Store.Nodes(ctx, company)
	if err != nil {
		return support.CompanyDoctor{}, err
	}
	pauses, err := c.Store.Pauses(ctx, company)
	if err != nil {
		return support.CompanyDoctor{}, err
	}
	nodePause := map[string]storage.Pause{}
	for _, p := range pauses {
		if p.Scope == "node" {
			nodePause[p.Target] = p
		}
		in.Pauses = append(in.Pauses, support.DoctorPause{Scope: p.Scope, Target: p.Target, Reason: p.Reason, Until: p.Until})
	}
	for _, n := range nodes {
		var h struct {
			Disk     string `json:"disk"`
			Free     uint64 `json:"free_disk_bytes"`
			Low      uint64 `json:"disk_low_bytes"`
			Critical uint64 `json:"disk_critical_bytes"`
			Checks   []struct {
				Check  string `json:"check"`
				Detail string `json:"detail"`
				Hint   string `json:"hint"`
			} `json:"checks"`
		}
		_ = json.Unmarshal(n.Health, &h)
		dn := support.DoctorNode{ID: n.NodeID, Name: n.Name, Online: n.Online, LastSeen: n.LastSeen, Disk: h.Disk, FreeBytes: h.Free, Low: h.Low, Critical: h.Critical, Drivers: []support.NodeDriver{}}
		if p, ok := nodePause[n.NodeID]; ok {
			dn.Paused, dn.PauseReason = true, p.Reason
		}
		for _, d := range n.Drivers {
			dn.Drivers = append(dn.Drivers, support.NodeDriver{Name: d.Name, OK: d.OK, Check: d.Check, Detail: d.Detail, Until: d.Until})
		}
		for _, ch := range h.Checks {
			dn.Checks = append(dn.Checks, support.NodeCheck{Check: ch.Check, Detail: ch.Detail, Hint: ch.Hint})
		}
		in.Nodes = append(in.Nodes, dn)
	}
	runs, err := c.Store.RecentRuns(ctx, company, in.Since.Unix(), 500, true)
	if err != nil {
		return support.CompanyDoctor{}, err
	}
	for _, r := range runs {
		in.FailedRuns = append(in.FailedRuns, support.FailedRun{ID: r.ID, TaskID: r.TaskID, Agent: r.Agent, NodeID: r.NodeID, Outcome: r.Outcome, NodeFault: r.NodeFault, Result: r.Result})
	}
	snaps, err := c.Store.TaskSnapshots(ctx, company, "")
	if err != nil {
		return support.CompanyDoctor{}, err
	}
	for _, snap := range snaps {
		v := c.evaluateIn(company, snap)
		tw := support.TaskWait{TaskID: snap.Task.ID, Project: snap.Task.ProjectID, Assignee: snap.Task.Assignee, Kind: snap.Task.Kind, Verdict: v}
		if run := snap.CurrentRun; run != nil && run.Status == "QUEUED" {
			q, err := c.Store.QueuedWhy(ctx, company, run.ID)
			if err != nil {
				return support.CompanyDoctor{}, err
			}
			if q != nil {
				tw.Queued = &support.QueuedWhy{Reason: q.Reason, Detail: q.Detail, Holder: q.Holder, Inactive: q.Inactive}
			}
		}
		if support.Stuck(tw) {
			in.Tasks = append(in.Tasks, tw)
		}
	}
	p, err := c.Store.PendingCounts(ctx, company)
	if err != nil {
		return support.CompanyDoctor{}, err
	}
	in.Pending = support.Pending{Inbox: p.Inbox, Approvals: p.Approvals, Alerts: p.Alerts, Inspect: p.Inspect, Reviews: p.Reviews, Total: p.Total}
	return support.BuildDoctor(in), nil
}
