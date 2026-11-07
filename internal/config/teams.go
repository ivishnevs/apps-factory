package config

import (
	"appfactory/internal/tasks"
	"fmt"
	"path"
	"strings"

	"appfactory/internal/configfile"
)

// Teams inside a company (company-teams D1–D3): the company owns every shared resource (nodes, quota keys,
// secrets, skills, profiles, policies, catalog versions); a team owns its agents, its lead, its audit assignee
// and its projects. Every agent names its team (spec.team); the agents reporting to the owner are exactly the
// team leads.

// Team is a document of configs/<dir>/teams/<name>.yaml.
type Team struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       TeamSpec `yaml:"spec" json:"spec"`
}

// TeamSpec is a team's lead (an agent of the team reporting to the owner: it gets the owner's unaddressed
// messages), its onboarding audit assignee (default: the lead) and the default profile of its agents.
type TeamSpec struct {
	Lead          string `yaml:"lead" json:"lead"`
	AuditAssignee string `yaml:"audit_assignee" json:"audit_assignee,omitempty"`
	Profile       string `yaml:"profile" json:"profile,omitempty"`
	// Approvals is who approves the team's artifacts and decisions (approval-delegation); projects may override it.
	Approvals tasks.ApprovalModes `yaml:"approvals" json:"approvals,omitzero"`
}

// Auditor is the team's onboarding audit assignee.
func (t Team) Auditor() string {
	if t.Spec.AuditAssignee != "" {
		return t.Spec.AuditAssignee
	}
	return t.Spec.Lead
}

// TeamNames are the catalog's team names, sorted.
func (c *Catalog) TeamNames() []string { return sortedKeys(c.Teams) }

// TeamOf is the team of agent ("" for an unknown agent or a catalog without teams).
func (c *Catalog) TeamOf(agent string) string { return c.Agents[agent].Spec.Team }

// TeamAgents are the agents of team, sorted.
func (c *Catalog) TeamAgents(team string) []string {
	var out []string
	for _, n := range Names(c.Agents) {
		if c.Agents[n].Spec.Team == team {
			out = append(out, n)
		}
	}
	return out
}

// Lead is team's lead, or the only team's lead when team is empty and the catalog has one team.
func (c *Catalog) Lead(team string) string {
	if team == "" && len(c.Teams) == 1 {
		for _, t := range c.Teams {
			return t.Spec.Lead
		}
	}
	return c.Teams[team].Spec.Lead
}

// loadTeams reads the team documents of dir/teams.
func loadTeams(src source, dir string) (map[string]Team, error) {
	out := map[string]Team{}
	paths, err := src.glob(path.Join(dir, "teams"), "*.yaml")
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		b, err := src.read(p)
		if err != nil {
			return nil, err
		}
		var t Team
		if err = configfile.Decode(b, &t); err != nil {
			return nil, fmt.Errorf("%s: %w", src.name(p), err)
		}
		if t.APIVersion != "factory/v1" || t.Kind != "Team" || !namePattern.MatchString(t.Metadata.Name) {
			return nil, fmt.Errorf("%s: invalid version, kind or name", src.name(p))
		}
		if _, ok := out[t.Metadata.Name]; ok {
			return nil, fmt.Errorf("duplicate Team %s", t.Metadata.Name)
		}
		out[t.Metadata.Name] = t
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no teams in %s: a catalog needs at least one team (teams/NAME.yaml, kind Team)", src.name(path.Join(dir, "teams")))
	}
	return out, nil
}

// validTeams checks the agents against the teams (D2, D3): every agent names a team of the catalog; a team's
// lead and audit assignee are its own agents; managers are in the agent's own team; the agents reporting to the
// owner are exactly the leads; every team has an agent.
func validTeams(teams map[string]Team, agents map[string]Agent) error {
	for _, name := range Names(agents) {
		a := agents[name]
		if a.Spec.Team == "" {
			return fmt.Errorf("agent %s: spec.team required (one of %s)", name, strings.Join(sortedKeys(teams), ", "))
		}
		if _, ok := teams[a.Spec.Team]; !ok {
			return fmt.Errorf("agent %s: unknown team %s", name, a.Spec.Team)
		}
		m := Manager(agents, name)
		if m == Owner {
			if teams[a.Spec.Team].Spec.Lead != name {
				return fmt.Errorf("agent %s reports to the owner but is not the lead of team %s (%s): only team leads report to the owner", name, a.Spec.Team, teams[a.Spec.Team].Spec.Lead)
			}
			continue
		}
		if mt := agents[m].Spec.Team; mt != a.Spec.Team {
			return fmt.Errorf("agent %s of team %s reports to %s of team %s: a manager must be in the same team", name, a.Spec.Team, m, mt)
		}
	}
	for _, team := range sortedKeys(teams) {
		t := teams[team]
		for field, agent := range map[string]string{"lead": t.Spec.Lead, "audit_assignee": t.Auditor()} {
			if agent == "" {
				return fmt.Errorf("team %s: %s required", team, field)
			}
			if a, ok := agents[agent]; !ok || a.Spec.Team != team {
				return fmt.Errorf("team %s: %s %s is not an agent of the team", team, field, agent)
			}
		}
		if Manager(agents, t.Spec.Lead) != Owner {
			return fmt.Errorf("team %s: lead %s must report to the owner", team, t.Spec.Lead)
		}
	}
	return nil
}

// teamProfile checks that a team's profile exists.
func teamProfiles(teams map[string]Team, profiles map[string]map[string]any) error {
	for _, team := range sortedKeys(teams) {
		if p := teams[team].Spec.Profile; p != "" {
			if _, ok := profiles[p]; !ok {
				return fmt.Errorf("team %s: unknown profile %s", team, p)
			}
		}
	}
	return nil
}
