package core

import (
	"appfactory/internal/config"
	"net/http"
	"strings"
)

func (c *Core) configRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /agents/{agent}", func(w http.ResponseWriter, r *http.Request) {
		a, ok := c.Agents[r.PathValue("agent")]
		if !ok {
			http.Error(w, "agent not found", 404)
			return
		}
		write(w, a)
	})
	m.HandleFunc("POST /agents/{agent}/authorize", func(w http.ResponseWriter, r *http.Request) {
		a, ok := c.Agents[r.PathValue("agent")]
		if !ok {
			http.Error(w, "agent not found", 404)
			return
		}
		var p struct {
			Operation string `json:"operation"`
		}
		if !decode(w, r, &p) {
			return
		}
		if strings.TrimSpace(p.Operation) == "" {
			http.Error(w, "operation required", 400)
			return
		}
		write(w, a.Authorize(p.Operation))
	})
	m.HandleFunc("GET /agents/{agent}/skills", func(w http.ResponseWriter, r *http.Request) {
		op := "factory.skills.list"
		if r.URL.Query().Get("q") != "" || r.URL.Query().Get("category") != "" {
			op = "factory.skills.search"
		}
		a, ok := c.skillAccess(w, r, op)
		if !ok {
			return
		}
		write(w, c.Skills.Entries(a.AvailableSkills, r.URL.Query().Get("q"), r.URL.Query().Get("category")))
	})
	m.HandleFunc("GET /agents/{agent}/skills/{skill}", func(w http.ResponseWriter, r *http.Request) {
		a, ok := c.skillAccess(w, r, "factory.skills.read")
		if !ok {
			return
		}
		name := r.PathValue("skill")
		found := false
		for _, n := range a.AvailableSkills {
			if n == name {
				found = true
			}
		}
		if !found {
			http.Error(w, "skill not available to agent", 403)
			return
		}
		content, err := c.Skills.Read(name, r.URL.Query().Get("resource"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		write(w, map[string]string{"name": name, "content": content})
	})
}
func (c *Core) skillAccess(w http.ResponseWriter, r *http.Request, op string) (config.Agent, bool) {
	a, ok := c.Agents[r.PathValue("agent")]
	if !ok {
		http.Error(w, "agent not found", 404)
		return a, false
	}
	if c.Skills == nil {
		http.Error(w, "skill registry unavailable", 503)
		return a, false
	}
	d := a.Authorize(op)
	if d.Effect != "allow" {
		status := 403
		if d.Effect == "approval" {
			status = 423
		}
		w.WriteHeader(status)
		write(w, d)
		return a, false
	}
	return a, true
}
