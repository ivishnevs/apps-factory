package config

import (
	"strings"
	"testing"

	"appfactory/internal/policies"
)

// reviewFixture is a team of a lead (reporting to the owner), a manager under it, a developer under the manager and
// a QA agent under the lead; names are fixtures, the rules are general.
func reviewFixture() *Catalog {
	agent := func(name, role, reportsTo string) Agent {
		var a Agent
		a.Metadata.Name, a.Spec.Team, a.Spec.Role, a.Spec.ReportsTo = name, "t1", role, reportsTo
		return a
	}
	var team Team
	team.Metadata.Name, team.Spec.Lead = "t1", "lead"
	return &Catalog{
		Agents: map[string]Agent{
			"lead": agent("lead", "manager", Owner),
			"mgr":  agent("mgr", "manager", "lead"),
			"dev":  agent("dev", "developer", "mgr"),
			"qa":   agent("qa", "qa", "lead"),
		},
		Teams: map[string]Team{"t1": team},
	}
}

// TestDefaultReviewer (approval-gates-merge 3.1): the assignee's review rule reviewer, else the team's default
// reviewer, else its manager, else the team's lead, else the owner; never the assignee.
func TestDefaultReviewer(t *testing.T) {
	c := reviewFixture()
	for agent, want := range map[string]string{"dev": "mgr", "mgr": "lead", "qa": "lead", "lead": Owner, "unknown": Owner} {
		if got := c.DefaultReviewer(agent); got != want {
			t.Fatalf("by hierarchy: %s → %s, want %s", agent, got, want)
		}
	}
	// The team's default reviewer (a role or an agent) comes before the managers, but never reviews its own work.
	tm := c.Teams["t1"]
	tm.Spec.Review.Reviewer = "role:qa"
	c.Teams["t1"] = tm
	for agent, want := range map[string]string{"dev": "qa", "mgr": "qa", "qa": "lead", "lead": "qa"} {
		if got := c.DefaultReviewer(agent); got != want {
			t.Fatalf("team default: %s → %s, want %s", agent, got, want)
		}
	}
	// The assignee's own review rule comes first.
	dev := c.Agents["dev"]
	dev.Rules = []policies.Rule{{Name: "r", Type: "review", Reviewer: "mgr"}}
	c.Agents["dev"] = dev
	if got := c.DefaultReviewer("dev"); got != "mgr" {
		t.Fatal("review rule", got)
	}
	for _, w := range ReviewCoverage(c) {
		if strings.HasPrefix(w, "agent dev ") {
			t.Fatal("dev has a rule reviewer", w)
		}
		if strings.HasPrefix(w, "agent mgr ") && !strings.Contains(w, "the team's default reviewer qa") {
			t.Fatal(w)
		}
	}
}

// TestTeamReviewerValid: a team's default reviewer is an agent of the team or a role one of its agents has.
func TestTeamReviewerValid(t *testing.T) {
	c := reviewFixture()
	for reviewer, ok := range map[string]bool{"qa": true, "role:qa": true, "role:designer": false, "nobody": false} {
		tm := c.Teams["t1"]
		tm.Spec.Review.Reviewer = reviewer
		c.Teams["t1"] = tm
		if err := validTeams(c.Teams, c.Agents); (err == nil) != ok {
			t.Fatalf("%s: %v", reviewer, err)
		}
	}
}
