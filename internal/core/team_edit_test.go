package core

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// TestTeamPageEdits (team-page-clarity 2.2, 2.4): the team view carries the team settings in readable form and each
// agent's messaging and assignment scope; owners and admins change the lead (the new lead takes the old one's place),
// the onboarding agent (empty: the lead) and the settings (the team profile) on a catalog base, Core refusing an
// agent of another team, a bad run time and a subscription of the wrong runtime; an agent's assignment scope saved
// from its form stays as set, none included.
func TestTeamPageEdits(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	ctx := context.Background()
	c := &Core{Store: s, Root: "../..", Token: "owner"}
	c.Handler()
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	created, err := c.CreateCompany(ctx, CompanyCreate{ID: "acme", Template: "full-team"})
	if err != nil {
		t.Fatal(err)
	}
	tok := created.Owner.Token
	do := func(method, path string, body any) (int, string) {
		t.Helper()
		return call(t, c, method, path, tok, body)
	}
	if got := requiredRole("PUT", "/catalog/teams/product"); got != storage.RoleAdmin {
		t.Fatal("team edits are for owners and admins:", got)
	}
	view := func() TeamView {
		t.Helper()
		code, out := do("GET", "/catalog/teams", nil)
		var v struct{ Teams []TeamView }
		if code != 200 || json.Unmarshal([]byte(out), &v) != nil || len(v.Teams) != 1 {
			t.Fatal(code, out)
		}
		return v.Teams[0]
	}

	// The settings are the profile's values, readable.
	v := view()
	st := v.Settings
	if st.Profile != "product-agent" || st.Provider != "codex" || st.Timeout != "30m" || st.MaxConcurrentRuns != 0 || st.RoleLimits["developer"] != 2 || len(st.QuotaKeys) == 0 {
		t.Fatalf("%+v", st)
	}
	for _, m := range v.Agents {
		if m.Name == "product-owner" && (m.AssignScope != config.AssignDirectReports || !slices.Contains(m.CanMessage, "owner") || !slices.Contains(m.CanMessage, "qa")) {
			t.Fatalf("%+v", m)
		}
		if m.Name == "qa" && m.AssignScope != config.AssignNone {
			t.Fatalf("%+v", m)
		}
	}

	// Change the lead: the developer reports to the owner, the old lead and its reports to the developer; the
	// onboarding agent (unset) follows the lead.
	base := c.catalogVersion("acme")
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, Lead: "developer"}); code != 200 {
		t.Fatal(code, out)
	}
	cat := c.cat("acme")
	if cat.Teams["product"].Spec.Lead != "developer" || cat.Teams["product"].Auditor() != "developer" {
		t.Fatalf("%+v", cat.Teams["product"].Spec)
	}
	for n, want := range map[string]string{"developer": "owner", "product-owner": "developer", "qa": "developer", "designer": "developer"} {
		if got := config.Manager(cat.Agents, n); got != want {
			t.Fatalf("%s reports to %s, want %s", n, got, want)
		}
	}
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, Lead: "qa"}); code != 409 {
		t.Fatal("stale base", code, out)
	}
	base = c.catalogVersion("acme")
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, Lead: "nobody"}); code != 400 || !strings.Contains(out, `"field":"lead"`) {
		t.Fatal(code, out)
	}

	// The onboarding agent: another agent, then the lead again.
	qa, lead := "qa", ""
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, AuditAssignee: &qa}); code != 200 || c.cat("acme").Teams["product"].Auditor() != "qa" {
		t.Fatal(code, out)
	}
	base = c.catalogVersion("acme")
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, AuditAssignee: &lead}); code != 200 || c.cat("acme").Teams["product"].Spec.AuditAssignee != "" {
		t.Fatal(code, out)
	}

	// Settings: Core refuses a bad run time and a subscription of another runtime; a valid change reaches the agents.
	base = c.catalogVersion("acme")
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, Settings: &TeamSettingsEdit{Provider: "codex", Timeout: "forever"}}); code != 400 || !strings.Contains(out, `"field":"timeout"`) {
		t.Fatal(code, out)
	}
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, Settings: &TeamSettingsEdit{Provider: "codex", QuotaKey: "claude-subscription", Timeout: "30m"}}); code != 400 || !strings.Contains(out, "claude-subscription") {
		t.Fatal(code, out)
	}
	if code, out := do("PUT", "/catalog/teams/product", TeamEdit{Base: base, Settings: &TeamSettingsEdit{Provider: "claude", QuotaKey: "claude-subscription", Model: "sonnet", MaxConcurrentRuns: 3, Timeout: "2h"}}); code != 200 {
		t.Fatal(code, out)
	}
	cat = c.cat("acme")
	if a := cat.Agents["designer"]; a.Spec.Runtime.Provider != "claude" || a.Spec.Runtime.Model != "sonnet" || a.Spec.Execution.Timeout != "2h" || a.Spec.Execution.MaxConcurrentRuns != 3 {
		t.Fatalf("%+v %+v", a.Spec.Runtime, a.Spec.Execution)
	}
	if st = view().Settings; st.Provider != "claude" || st.Model != "sonnet" || st.MaxConcurrentRuns != 3 || st.Timeout != "2h" || len(st.Overrides) != 0 {
		t.Fatalf("%+v", st)
	}

	// An agent that sets its own model overrides the settings.
	base = c.catalogVersion("acme")
	var doc AgentDoc
	if code, out := do("GET", "/catalog/agents/qa", nil); code != 200 || json.Unmarshal([]byte(out), &doc) != nil {
		t.Fatal(code, out)
	}
	doc.Agent.Spec.Runtime.Model = "haiku"
	if code, out := do("PUT", "/catalog/agents/qa", doc); code != 200 {
		t.Fatal(code, out)
	}
	if st = view().Settings; !slices.Equal(st.Overrides, []string{"qa"}) {
		t.Fatalf("%+v", st.Overrides)
	}

	// The lead's assignment scope saved as none stays none (it has reports, so unset would be direct_reports);
	// a chosen list stays a list.
	for _, scope := range []config.AssignScope{{Mode: config.AssignNone}, {Mode: config.AssignList, Agents: []string{"qa"}}} {
		if code, out := do("GET", "/catalog/agents/developer", nil); code != 200 || json.Unmarshal([]byte(out), &doc) != nil {
			t.Fatal(code, out)
		}
		doc.Agent.Spec.Communication.CanAssignTasks = scope
		doc.Agent.Spec.Communication.CanMessage = []string{"owner"}
		if code, out := do("PUT", "/catalog/agents/developer", doc); code != 200 {
			t.Fatal(code, out)
		}
		if got := config.AssignScopeOf(c.cat("acme").Agents, "developer"); got.Mode != scope.Mode || !slices.Equal(got.Agents, scope.Agents) {
			t.Fatalf("%+v, want %+v", got, scope)
		}
	}
}
