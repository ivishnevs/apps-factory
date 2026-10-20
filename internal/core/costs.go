package core

import (
	"net/http"
	"strconv"
	"time"

	"appfactory/internal/storage"
)

// Cost queries (waits-events-cost-cli D5), owner token only: GET /costs?by=task|agent|day|model with
// task, agent, model and since (Unix seconds or a duration such as 24h) filters; GET /tasks/{id}/costs is a
// task's runs and their total (a closed task's cost).
func (c *Core) costRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /costs", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		f := storage.CostFilter{Task: q.Get("task"), Agent: q.Get("agent"), Model: q.Get("model"), By: q.Get("by")}
		if since := q.Get("since"); since != "" {
			if d, err := time.ParseDuration(since); err == nil {
				f.Since = c.Store.Now().Add(-d).Unix()
			} else if n, err := strconv.ParseInt(since, 10, 64); err == nil {
				f.Since = n
			} else {
				http.Error(w, "since: Unix seconds or a duration such as 24h", 400)
				return
			}
		}
		v, err := c.Store.Costs(r.Context(), c.companyFrom(r), f)
		reply(w, v, err)
	})
	m.HandleFunc("GET /tasks/{id}/costs", func(w http.ResponseWriter, r *http.Request) {
		if _, err := c.Store.Task(r.Context(), c.companyFrom(r), r.PathValue("id")); err != nil {
			reply(w, nil, err)
			return
		}
		v, err := c.Store.Costs(r.Context(), c.companyFrom(r), storage.CostFilter{Task: r.PathValue("id")})
		reply(w, v, err)
	})
}
