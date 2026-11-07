package core

import (
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
)

// Agents' catalog authority (agent-company-authority, owner 2026-11-06): only a team's lead proposes catalog
// changes, and only for its own team (its agents, prompts, profile, policies and the team document); changes to
// other teams or company-wide documents (skills, quota keys, node hooks, MCP servers, prices) need the lead's
// company grant (spec.authority.company, given by people only); deleting a team or an agent needs the owner's
// approval. Every change still needs a person's approval.

// CatalogImpact is what a catalog change touches, in plain terms for its approval and the scope check.
type CatalogImpact struct {
	Teams       []string `json:"teams,omitempty"`       // teams whose documents change
	Company     []string `json:"company,omitempty"`     // company-wide documents that change
	Destructive []string `json:"destructive,omitempty"` // deleted teams and agents ("team T", "agent A")
	Grants      []string `json:"grants,omitempty"`      // agents whose company authority changes
}

// catalogImpact computes the teams, company documents and deletions of ch against the current catalog old and the
// catalog after the change (nil when it does not load).
func catalogImpact(company string, old, next *config.Catalog, ch CatalogChange) CatalogImpact {
	dir, _ := config.CompanyDir(company)
	dir = strings.TrimSuffix(path.Clean(strings.ReplaceAll(dir, "\\", "/")), "/") + "/"
	teams := map[string]bool{}
	var out CatalogImpact
	cats := []*config.Catalog{old}
	if next != nil {
		cats = append(cats, next)
	}
	owners := func(p string) []string {
		rel := strings.TrimPrefix(p, dir)
		kind, file, _ := strings.Cut(rel, "/")
		name := strings.TrimSuffix(file, path.Ext(file))
		var ts []string
		add := func(t string) {
			if t != "" && !slices.Contains(ts, t) {
				ts = append(ts, t)
			}
		}
		for _, c := range cats {
			switch {
			case strings.HasPrefix(p, "prompts/"):
				for _, a := range c.Agents {
					if a.Spec.Prompt.File == p {
						add(a.Spec.Team)
					}
				}
			case strings.HasPrefix(p, dir) && kind == "agents":
				if a, ok := c.Agents[name]; ok {
					add(a.Spec.Team)
				}
			case strings.HasPrefix(p, dir) && kind == "teams":
				add(name)
			case strings.HasPrefix(p, dir) && kind == "profiles":
				for t, team := range c.Teams {
					if team.Spec.Profile == name {
						add(t)
					}
				}
			case strings.HasPrefix(p, dir) && kind == "policies":
				for _, a := range c.Agents {
					if slices.Contains(a.Spec.Policies, name) {
						add(a.Spec.Team)
					}
				}
			}
		}
		return ts
	}
	for _, p := range changedPaths(ch) {
		ts := owners(p)
		if len(ts) == 0 {
			out.Company = append(out.Company, p)
		}
		for _, t := range ts {
			teams[t] = true
		}
	}
	for _, p := range ch.Delete {
		rel := strings.TrimPrefix(p, dir)
		kind, file, _ := strings.Cut(rel, "/")
		name := strings.TrimSuffix(file, path.Ext(file))
		if strings.HasPrefix(p, dir) && (kind == "agents" || kind == "teams") {
			out.Destructive = append(out.Destructive, strings.TrimSuffix(kind, "s")+" "+name)
		}
	}
	if next != nil {
		for name, a := range next.Agents {
			if o, ok := old.Agents[name]; a.Spec.Authority.Company && (!ok || !o.Spec.Authority.Company) {
				out.Grants = append(out.Grants, name)
			}
		}
	}
	for t := range teams {
		out.Teams = append(out.Teams, t)
	}
	sort.Strings(out.Teams)
	sort.Strings(out.Grants)
	return out
}

func changedPaths(ch CatalogChange) []string {
	var ps []string
	for p := range ch.Put {
		ps = append(ps, p)
	}
	ps = append(ps, ch.Delete...)
	sort.Strings(ps)
	return ps
}

// catalogScope refuses an agent's catalog change outside its authority, with a message the agent can act on.
func (c *Core) catalogScope(company, agent string, ch CatalogChange, next *config.Catalog) error {
	old := c.cat(company)
	a, ok := old.Agents[agent]
	if !ok {
		return fmt.Errorf("agent %s is not in the catalog", agent)
	}
	team := a.Spec.Team
	lead := team != "" && old.Lead(team) == agent || a.Spec.Role == "manager"
	if !lead {
		return fmt.Errorf("only a team's lead proposes catalog changes: ask your lead (%s) to make it", old.Lead(team))
	}
	im := catalogImpact(company, old, next, ch)
	if len(im.Grants) > 0 {
		return fmt.Errorf("company authority (spec.authority.company) is granted by people only, not through a catalog change: ask the owner")
	}
	if a.Spec.Authority.Company {
		return nil
	}
	if len(im.Company) > 0 {
		return fmt.Errorf("these documents are company-wide and need your company grant (an owner gives it on the team page): %s", strings.Join(im.Company, ", "))
	}
	for _, t := range im.Teams {
		if t != team {
			return fmt.Errorf("you may change your own team %s only; this change touches team %s, which needs your company grant (an owner gives it on the team page)", team, t)
		}
	}
	return nil
}

// catalogChangeOf reads a factory_catalog_change call's arguments into a change.
func catalogChangeOf(call factorytools.Call) CatalogChange {
	ch := CatalogChange{Put: map[string]string{}}
	if v, ok := call.Arguments["base"].(float64); ok {
		ch.Base = int64(v)
	}
	ch.Note, _ = call.Arguments["note"].(string)
	if put, ok := call.Arguments["put"].(map[string]any); ok {
		for p, v := range put {
			if content, ok := v.(string); ok {
				ch.Put[p] = content
			}
		}
	}
	if del, ok := call.Arguments["delete"].([]any); ok {
		for _, v := range del {
			if p, ok := v.(string); ok {
				ch.Delete = append(ch.Delete, p)
			}
		}
	}
	return ch
}
