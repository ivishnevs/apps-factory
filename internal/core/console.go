package core

import (
	"net/http"

	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Owner console v0 (waits-events-cost-cli, first delivery): company feed and a status summary.

type RunStatus struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	TaskID    string `json:"task_id,omitempty"`
	Status    string `json:"status"`
	StartedAt int64  `json:"started_at,omitempty"`
	// Queued says why a QUEUED run is not claimed yet (owner-ui 6.1).
	Queued *storage.QueuedReason `json:"queued,omitempty"`
}

type ProjectStatus struct {
	Project tasks.Project  `json:"project"`
	Runs    []RunStatus    `json:"runs"`
	Tasks   map[string]int `json:"tasks"`
	// Waiting are the non-terminal tasks that neither run nor are about to be dispatched.
	Waiting  []tasks.Verdict `json:"waiting"`
	Runnable int             `json:"runnable"`
	Inbox    int             `json:"inbox"`
}

func (c *Core) consoleRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /feed", func(w http.ResponseWriter, r *http.Request) {
		a, l, ok := httpPage(w, r)
		if !ok {
			return
		}
		q := r.URL.Query()
		f := c.feedFilter(q)
		v, err := c.Store.Feed(r.Context(), c.companyFrom(r), f, a, l)
		reply(w, v, err)
	})
	m.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.status(r, r.URL.Query().Get("project"))
		reply(w, v, err)
	})
}

func (c *Core) status(r *http.Request, only string) ([]ProjectStatus, error) {
	ctx, company := r.Context(), c.companyFrom(r)
	projects, err := c.Store.Projects(ctx, company)
	if err != nil {
		return nil, err
	}
	if only != "" {
		p, err := c.Store.Project(ctx, company, only)
		if err != nil {
			return nil, err
		}
		projects = []tasks.Project{p}
	}
	runs, err := c.Store.ActiveRuns(ctx, company)
	if err != nil {
		return nil, err
	}
	var queuedIDs []string
	for _, run := range runs {
		if run.Status == "QUEUED" {
			queuedIDs = append(queuedIDs, run.Spec.ID)
		}
	}
	queued, err := c.Store.QueuedWhyAll(ctx, company, queuedIDs)
	if err != nil {
		return nil, err
	}
	out := []ProjectStatus{}
	for _, p := range projects {
		st := ProjectStatus{Project: p, Runs: []RunStatus{}, Tasks: map[string]int{}, Waiting: []tasks.Verdict{}}
		for _, run := range runs {
			if run.Spec.Project == p.ID {
				st.Runs = append(st.Runs, RunStatus{ID: run.Spec.ID, Agent: run.Spec.Agent, TaskID: run.Spec.TaskID, Status: run.Status, StartedAt: run.StartedAt, Queued: queued[run.Spec.ID]})
			}
		}
		all, err := c.Store.Tasks(ctx, company, p.ID, "")
		if err != nil {
			return nil, err
		}
		for _, t := range all {
			st.Tasks[t.Status]++
		}
		snaps, err := c.Store.TaskSnapshots(ctx, company, p.ID)
		if err != nil {
			return nil, err
		}
		for _, snap := range snaps {
			switch v := c.evaluate(snap); v.Reason {
			case tasks.ReasonRunnable:
				st.Runnable++
			case tasks.ReasonLiveRun:
			default:
				st.Waiting = append(st.Waiting, v)
			}
		}
		inbox, err := c.Store.Inbox(ctx, company, p.ID)
		if err != nil {
			return nil, err
		}
		st.Inbox = len(inbox)
		out = append(out, st)
	}
	return out, nil
}
