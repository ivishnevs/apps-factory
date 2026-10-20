package core

import (
	"net/http"
	"net/url"
	"slices"

	"appfactory/internal/config"
	"appfactory/internal/storage"
)

// subtree is the agent and every agent whose reports_to chain reaches it (waits-events-cost-cli D3).
func (c *Core) subtree(agent string) []string {
	out := []string{agent}
	for _, name := range config.Names(c.Agents) {
		if name != agent && slices.Contains(config.Chain(c.Agents, name), agent) {
			out = append(out, name)
		}
	}
	return out
}

// feedListTool is factory_feed_list: the feed of the run's agent's subtree, across its company's projects.
func (c *Core) feedListTool(w http.ResponseWriter, r *http.Request, args map[string]any, run storage.Run) {
	f := storage.FeedFilter{Agents: c.subtree(run.Spec.Agent)}
	f.Task, _ = args["task"].(string)
	f.Attention, _ = args["attention"].(bool)
	after, limit := int64(0), 20
	if v, ok := args["after"].(float64); ok {
		after = int64(v)
	}
	if v, ok := args["limit"].(float64); ok {
		limit = int(v)
	}
	if limit < 1 || limit > 50 {
		http.Error(w, "limit 1..50", 400)
		return
	}
	v, err := c.Store.Feed(r.Context(), c.companyFrom(r), f, after, limit)
	reply(w, v, err)
}

// feedFilter reads the owner feed filters: project, task, attention and subtree (an agent's subtree).
func (c *Core) feedFilter(q url.Values) storage.FeedFilter {
	f := storage.FeedFilter{Project: q.Get("project"), Task: q.Get("task"), Attention: q.Get("attention") == "1" || q.Get("attention") == "true"}
	if a := q.Get("subtree"); a != "" {
		f.Agents = c.subtree(a)
	}
	return f
}
