package config

import (
	"slices"
	"strings"
	"testing"
)

// twoTeams is the fixture's catalog with a second team: gamedev (lead agent, dev, qa) and saas (lead boss,
// coder, tester); the saas team's agents take their profile from the team.
func twoTeams(t *testing.T) string {
	t.Helper()
	root := fixture(t)
	put(t, root, "configs/teams/main.yaml", "apiVersion: factory/v1\nkind: Team\nmetadata:\n  name: main\n  description: Games\nspec:\n  lead: agent\n  audit_assignee: dev\n")
	put(t, root, "configs/teams/saas.yaml", "apiVersion: factory/v1\nkind: Team\nmetadata:\n  name: saas\nspec:\n  lead: boss\n  profile: saas\n")
	put(t, root, "configs/profiles/saas.yaml", "apiVersion: factory/v1\nkind: AgentProfile\nmetadata:\n  name: saas\nspec:\n  extends: base\n  team: saas\n  runtime:\n    provider: claude\n")
	agent := func(name, team, extra string) {
		put(t, root, "configs/agents/"+name+".yaml", "apiVersion: factory/v1\nkind: Agent\nmetadata:\n  name: "+name+"\nspec:\n"+team+extra)
	}
	agent("dev", "  extends: base\n", "  reports_to: agent\n")
	agent("qa", "  extends: base\n", "  role: qa\n  reports_to: agent\n")
	agent("boss", "  team: saas\n", "")
	agent("coder", "  team: saas\n", "  reports_to: boss\n")
	agent("tester", "  team: saas\n", "  role: qa\n  reports_to: boss\n")
	return root
}

// TestTeams (company-teams 3.1, 5.2, 5.3, 8.1): teams with leads and audit assignees, the team's profile as the
// default extends, the default communication inside the team, reviewer roles and policy rules per team.
func TestTeams(t *testing.T) {
	root := twoTeams(t)
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.TeamNames(); !slices.Equal(got, []string{"main", "saas"}) {
		t.Fatal(got)
	}
	if c.Teams["main"].Auditor() != "dev" || c.Teams["saas"].Auditor() != "boss" || c.Lead("saas") != "boss" || c.Lead("") != "" {
		t.Fatal(c.Teams)
	}
	if got := c.TeamAgents("saas"); !slices.Equal(got, []string{"boss", "coder", "tester"}) {
		t.Fatal(got)
	}
	// The team's profile is the default extends of its agents.
	if c.Agents["coder"].Spec.Runtime.Provider != "claude" || c.Agents["dev"].Spec.Runtime.Provider != "codex" {
		t.Fatal("team profile", c.Agents["coder"].Spec.Runtime, c.Agents["dev"].Spec.Runtime)
	}
	// Communication: own team and the owner; nobody of the other team.
	coder := c.Agents["coder"].Spec.Communication.CanMessage
	if !slices.Contains(coder, "tester") || !slices.Contains(coder, "boss") || !slices.Contains(coder, Owner) || slices.Contains(coder, "dev") || slices.Contains(coder, "agent") {
		t.Fatal("coder", coder)
	}
	// Reviewer role per team: qa of each team.
	if got := ReviewerFor(c.Agents, "coder", "role:qa"); got != "tester" {
		t.Fatal("saas reviewer", got)
	}
	if got := ReviewerFor(c.Agents, "dev", "role:qa"); got != "qa" {
		t.Fatal("main reviewer", got)
	}
	if got := ReviewerFor(c.Agents, "dev", "tester"); got != "" {
		t.Fatal("a reviewer agent of another team", got)
	}
}

func TestTeamsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		path, text, want string
	}{
		"agent without a team":                {"configs/agents/loner.yaml", "apiVersion: factory/v1\nkind: Agent\nmetadata:\n  name: loner\nspec:\n  role: dev\n  reports_to: agent\n  runtime:\n    provider: codex\n  prompt:\n    file: prompts/agent.md\n  execution:\n    timeout: 1m\n    max_concurrent_runs: 1\n", "agent loner: spec.team required"},
		"unknown team":                        {"configs/agents/coder.yaml", "apiVersion: factory/v1\nkind: Agent\nmetadata:\n  name: coder\nspec:\n  extends: saas\n  team: ghosts\n  reports_to: boss\n", "unknown team ghosts"},
		"manager in another team":             {"configs/agents/coder.yaml", "apiVersion: factory/v1\nkind: Agent\nmetadata:\n  name: coder\nspec:\n  team: saas\n  reports_to: agent\n", "agent coder of team saas reports to agent of team main"},
		"second agent reporting to the owner": {"configs/agents/coder.yaml", "apiVersion: factory/v1\nkind: Agent\nmetadata:\n  name: coder\nspec:\n  team: saas\n", "agent coder reports to the owner but is not the lead of team saas"},
		"lead of another team":                {"configs/teams/saas.yaml", "apiVersion: factory/v1\nkind: Team\nmetadata:\n  name: saas\nspec:\n  lead: dev\n  profile: saas\n", "is not the lead of team saas (dev)"},
		"audit assignee of another team":      {"configs/teams/saas.yaml", "apiVersion: factory/v1\nkind: Team\nmetadata:\n  name: saas\nspec:\n  lead: boss\n  audit_assignee: qa\n  profile: saas\n", "audit_assignee qa is not an agent of the team"},
		"unknown team profile":                {"configs/teams/saas.yaml", "apiVersion: factory/v1\nkind: Team\nmetadata:\n  name: saas\nspec:\n  lead: boss\n  profile: nope\n", "team saas: unknown profile nope"},
		"old default report":                  {"configs/company.yaml", "owner:\n  default_report: agent\n", "set per team"},
		"old audit assignee":                  {"configs/company.yaml", "projects:\n  audit_assignee: agent\n", "set per team"},
	} {
		t.Run(name, func(t *testing.T) {
			root := twoTeams(t)
			put(t, root, tc.path, tc.text)
			if _, err := LoadCatalog(root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

// TestTeamPolicySelector (company-teams 5.3): a rule with teams applies to that team's agents only; mandatory
// rules apply to everyone.
func TestTeamPolicySelector(t *testing.T) {
	root := twoTeams(t)
	put(t, root, "configs/policies/saas-rules.yaml", "apiVersion: factory/v1\nkind: Policy\nmetadata:\n  name: saas-rules\nspec:\n  mandatory: true\n  rules:\n    - name: no-deploy\n      type: access\n      teams: [saas]\n      operations: [factory.deploy.prod]\n      effect: deny\n      instruction: SaaS deploys through the release train.\n")
	c, err := LoadCatalog(root)
	if err != nil {
		t.Fatal(err)
	}
	if d := c.Agents["coder"].Authorize("factory.deploy.prod"); d.Effect != "deny" {
		t.Fatal("saas agent allowed", d)
	}
	if d := c.Agents["dev"].Authorize("factory.deploy.prod"); d.Effect == "deny" {
		t.Fatal("gamedev agent denied by a saas rule", d)
	}
}
