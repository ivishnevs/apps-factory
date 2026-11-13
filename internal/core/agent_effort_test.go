package core

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// TestAgentEffortAPI (agent-effort-in-web 2.1, 2.2): GET /catalog/runtimes lists every runtime with the efforts it
// accepts and the models the agent form offers; an effort set in the agent form is saved as a catalog change with a
// note, shown on the team member and sent with the agent's runs; clearing it keeps the runtime default; an unknown
// effort is refused naming the effort field.
func TestAgentEffortAPI(t *testing.T) {
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	ctx := context.Background()
	co := storage.DefaultCompany
	c := &Core{Store: s, Root: "../..", Token: "owner"}
	c.Handler()
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	do := func(method, path string, body any) (int, string) {
		t.Helper()
		return call(t, c, method, path, c.Token, body)
	}

	code, out := do("GET", "/catalog/runtimes", nil)
	var runtimes []RuntimeView
	if code != 200 || json.Unmarshal([]byte(out), &runtimes) != nil || len(runtimes) != len(config.RuntimeOptions) {
		t.Fatal(code, out)
	}
	for _, r := range runtimes {
		if r.Models[0] != "default" || !slices.Equal(r.Efforts, config.RuntimeEfforts(protocol.RuntimeProvider(r.Provider))) {
			t.Fatalf("%+v", r)
		}
	}

	// The agent under test: the first agent of the catalog whose runtime accepts an effort.
	var name string
	for _, n := range config.Names(c.cat(co).Agents) {
		if len(config.RuntimeEfforts(protocol.RuntimeProvider(c.cat(co).Agents[n].Spec.Runtime.Provider))) > 0 {
			name = n
			break
		}
	}
	if name == "" {
		t.Fatal("no agent on a runtime with effort")
	}
	member := func() TeamMember {
		t.Helper()
		code, out := do("GET", "/catalog/teams", nil)
		var v struct{ Teams []TeamView }
		if code != 200 || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatal(code, out)
		}
		for _, team := range v.Teams {
			for _, m := range team.Agents {
				if m.Name == name {
					return m
				}
			}
		}
		t.Fatal("no member", name, out)
		return TeamMember{}
	}
	put := func(effort string) (int, string) {
		t.Helper()
		var doc AgentDoc
		if code, out := do("GET", "/catalog/agents/"+name, nil); code != 200 || json.Unmarshal([]byte(out), &doc) != nil {
			t.Fatal(code, out)
		}
		doc.Agent.Spec.Runtime.Effort = effort
		return do("PUT", "/catalog/agents/"+name, doc)
	}

	if code, out = put("high"); code != 200 {
		t.Fatal(code, out)
	}
	if h, _ := s.CatalogHistory(ctx, co); h[0].Note != "agent "+name+" edited" {
		t.Fatal(h[0].Note)
	}
	if m := member(); m.Effort != "high" {
		t.Fatalf("%+v", m)
	}
	if o := c.cat(co).Agents[name].RuntimeOptions(); o["effort"] != "high" {
		t.Fatal(o)
	}

	if code, out = put(""); code != 200 {
		t.Fatal(code, out)
	}
	if m := member(); m.Effort != "" {
		t.Fatalf("%+v", m)
	}
	if o := c.cat(co).Agents[name].RuntimeOptions(); o["effort"] != nil {
		t.Fatal("cleared effort still sent:", o)
	}

	code, out = put("huge")
	var fe FieldError
	if code != 400 || json.Unmarshal([]byte(out), &fe) != nil || fe.Field != "effort" {
		t.Fatal(code, out)
	}
}
