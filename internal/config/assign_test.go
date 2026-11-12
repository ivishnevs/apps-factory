package config

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// tvorogTree is the reporting tree of the 2026-11-11 case: the CEO under the owner, the PM and App Release under the
// CEO, the developers under the PM.
func tvorogTree() map[string]Agent {
	agents := map[string]Agent{}
	add := func(name, manager, team string) {
		var a Agent
		a.Metadata.Name = name
		a.Spec.ReportsTo = manager
		a.Spec.Team = team
		agents[name] = a
	}
	add("ceo", "owner", "tvorog")
	add("product-manager", "ceo", "tvorog")
	add("app-release", "ceo", "tvorog")
	add("developer", "product-manager", "tvorog")
	add("developer-lite", "product-manager", "tvorog")
	add("other", "", "elsewhere")
	return agents
}

func TestAssignScopeParsing(t *testing.T) {
	for in, want := range map[string]AssignScope{
		"none":           {Mode: AssignNone},
		"direct_reports": {Mode: AssignDirectReports},
		"subtree":        {Mode: AssignSubtree},
		"true":           {Mode: AssignDirectReports},
		"false":          {Mode: AssignNone},
		"[a, b]":         {Mode: AssignList, Agents: []string{"a", "b"}},
		"null":           {},
	} {
		var got struct {
			S AssignScope `yaml:"s"`
		}
		if err := yaml.Unmarshal([]byte("s: "+in), &got); err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got.S.Mode != want.Mode || strings.Join(got.S.Agents, ",") != strings.Join(want.Agents, ",") {
			t.Fatalf("%s: %+v, want %+v", in, got.S, want)
		}
	}
	var bad struct {
		S AssignScope `yaml:"s"`
	}
	if err := yaml.Unmarshal([]byte("s: everyone"), &bad); err == nil {
		t.Fatal("an unknown scope word was accepted")
	}
	for in, want := range map[string]string{`true`: AssignDirectReports, `"subtree"`: AssignSubtree, `["x"]`: AssignList, `null`: ""} {
		var s AssignScope
		if err := json.Unmarshal([]byte(in), &s); err != nil || s.Mode != want {
			t.Fatalf("json %s: %+v %v", in, s, err)
		}
	}
	b, _ := json.Marshal(AssignScope{Mode: AssignList, Agents: []string{"x"}})
	if string(b) != `["x"]` {
		t.Fatalf("marshal list: %s", b)
	}
}

func TestAssignByHierarchy(t *testing.T) {
	agents := tvorogTree()
	// Defaults: managers assign to their direct reports, the others to no one.
	if got := AssignTargets(agents, "ceo"); strings.Join(got, ",") != "app-release,product-manager" {
		t.Fatalf("ceo targets %v", got)
	}
	if CanAssign(agents, "developer") {
		t.Fatal("an agent without reports may assign by default")
	}
	// The 2026-11-11 case: the CEO gives developer-lite a task directly.
	if MayAssign(agents, "ceo", "developer-lite") {
		t.Fatal("the CEO may assign past the PM")
	}
	if why := AssignRefusal(agents, "ceo", "developer-lite"); !strings.Contains(why, "give the task to product-manager") {
		t.Fatalf("refusal %q", why)
	}
	for _, ok := range [][2]string{{"product-manager", "developer-lite"}, {"ceo", "product-manager"}, {"ceo", "ceo"}, {"", "developer"}, {Owner, "developer"}} {
		if !MayAssign(agents, ok[0], ok[1]) {
			t.Fatalf("%s may not assign %s", ok[0], ok[1])
		}
	}
	// Explicit scopes.
	ceo := agents["ceo"]
	ceo.Spec.Communication.CanAssignTasks = AssignScope{Mode: AssignSubtree}
	agents["ceo"] = ceo
	if !MayAssign(agents, "ceo", "developer-lite") {
		t.Fatal("subtree does not reach developer-lite")
	}
	ceo.Spec.Communication.CanAssignTasks = AssignScope{Mode: AssignList, Agents: []string{"app-release"}}
	agents["ceo"] = ceo
	if MayAssign(agents, "ceo", "product-manager") || !MayAssign(agents, "ceo", "app-release") {
		t.Fatal("list scope")
	}
	dev := agents["developer"]
	if why := AssignRefusal(agents, "developer", "developer-lite"); !strings.Contains(why, "may not assign") {
		t.Fatalf("refusal for an agent without scope %q (scope %+v)", why, dev.Spec.Communication.CanAssignTasks)
	}
}

func TestAssignScopeValidation(t *testing.T) {
	agents := tvorogTree()
	ceo := agents["ceo"]
	for list, want := range map[string]string{"nobody": "unknown agent", "ceo": "names itself", "other": "of team elsewhere"} {
		ceo.Spec.Communication.CanAssignTasks = AssignScope{Mode: AssignList, Agents: []string{list}}
		agents["ceo"] = ceo
		if err := validAssignScopes(agents); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: %v", list, err)
		}
	}
	ceo.Spec.Communication.CanAssignTasks = AssignScope{}
	agents["ceo"] = ceo
	dev := agents["developer"]
	dev.Spec.Communication.CanAssignTasks = AssignScope{Mode: AssignDirectReports}
	agents["developer"] = dev
	if w := AssignWarnings(&Catalog{Agents: agents}); len(w) != 1 || !strings.Contains(w[0], "developer") {
		t.Fatalf("warnings %v", w)
	}
}

func TestRosterFollowsScope(t *testing.T) {
	agents := tvorogTree()
	c := &Catalog{Agents: agents}
	ceo := agents["ceo"]
	ceo.Spec.Communication.CanCreateTasks = true
	agents["ceo"] = ceo
	got := hierarchyInstructions(c, "ceo")
	if !strings.Contains(got, "- product-manager (") || strings.Contains(got, "- developer-lite (") {
		t.Fatalf("roster:\n%s", got)
	}
	if !strings.Contains(got, "Its team (give their work to product-manager): developer, developer-lite") {
		t.Fatalf("roster without the PM's team:\n%s", got)
	}
	if strings.Contains(hierarchyInstructions(c, "developer"), "Agents you assign work to") {
		t.Fatal("an agent without scope gets a roster")
	}
}
