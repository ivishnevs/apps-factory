package core

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/presets"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// TestTeamBuilderAPI (team-builder 4.1–4.4, 6.1): the gallery lists presets with versions and skills; a company made
// from a template is a copy of its team preset; a second team and an agent from a preset (with overrides) are catalog
// versions on a base, a stale base is 409; an agent reads and saves as a document and prompt; deleting a lead is
// refused naming the field; a loader error names its field; the setup status names a missing secret, no node and
// teams without projects; an agent copied from an older preset gets an update hint.
func TestTeamBuilderAPI(t *testing.T) {
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
	code, out := do("GET", "/presets/teams", nil)
	if code != 200 || !strings.Contains(out, `"name":"game-dev"`) || !strings.Contains(out, `"version":"`) || !strings.Contains(out, `"skills":["mobile-app-testing","openspec","task-pull-request"]`) {
		t.Fatal(code, out)
	}
	if code, out = do("GET", "/presets/agents", nil); code != 200 || !strings.Contains(out, `"name":"ios-developer"`) {
		t.Fatal(code, out)
	}
	if a := c.cat("acme").Agents["developer"]; a.Spec.Team != "product" || !strings.HasPrefix(a.Metadata.Preset, "developer@") {
		t.Fatalf("%+v", a.Metadata)
	}

	base := c.catalogVersion("acme")
	if code, out = do("POST", "/catalog/teams", TeamCreate{Base: base, TeamCopy: presets.TeamCopy{Preset: "saas", Team: "web"}}); code != 200 {
		t.Fatal(code, out)
	}
	if code, out = do("POST", "/catalog/teams", TeamCreate{Base: base, TeamCopy: presets.TeamCopy{Preset: "solo", Team: "other"}}); code != 409 {
		t.Fatal("stale base", code, out)
	}
	base = c.catalogVersion("acme")
	if code, out = do("POST", "/catalog/agents", AgentCreate{Base: base, Team: "product", FromPreset: "ios-developer", Name: "ios", Overrides: presets.AgentOverrides{Provider: "claude", QuotaKey: "claude-subscription"}}); code != 200 {
		t.Fatal(code, out)
	}
	if ios := c.cat("acme").Agents["ios"]; ios.Spec.Runtime.Provider != "claude" || ios.Spec.ReportsTo != "product-owner" {
		t.Fatalf("%+v", ios.Spec)
	}
	base = c.catalogVersion("acme")
	if code, out = do("POST", "/catalog/agents", AgentCreate{Base: base, Team: "product", FromPreset: "developer", Name: "dev2", Overrides: presets.AgentOverrides{ReportsTo: "nobody"}}); code != 400 || !strings.Contains(out, `"field":"reports_to"`) {
		t.Fatal(code, out)
	}
	// work-and-conversation-runs: a refused window or conversation cap names its field for the editor.
	if code, out = do("POST", "/catalog/agents", AgentCreate{Base: base, Team: "product", FromPreset: "developer", Name: "dev2", Overrides: presets.AgentOverrides{ContextWindow: config.ContextWindow{Conversation: "50k"}}}); code != 400 || !strings.Contains(out, `"field":"context_window"`) {
		t.Fatal(code, out)
	}
	if code, out = do("POST", "/catalog/agents", AgentCreate{Base: base, Team: "product", FromPreset: "developer", Name: "dev2", Overrides: presets.AgentOverrides{ConversationMaxRuns: 9}}); code != 400 || !strings.Contains(out, `"field":"conversation_max_runs"`) {
		t.Fatal(code, out)
	}

	// Read and save an agent as fields.
	var doc AgentDoc
	code, out = do("GET", "/catalog/agents/web-frontend", nil)
	if code != 200 || json.Unmarshal([]byte(out), &doc) != nil || doc.Prompt == "" || doc.Agent.Spec.Team != "web" || doc.Base != base {
		t.Fatal(code, out)
	}
	doc.Agent.Metadata.Description = "Builds the storefront"
	doc.Prompt = "You build the storefront."
	if code, out = do("PUT", "/catalog/agents/web-frontend", doc); code != 200 {
		t.Fatal(code, out)
	}
	if a := c.cat("acme").Agents["web-frontend"]; a.Metadata.Description != "Builds the storefront" || !strings.HasPrefix(a.Instructions, "You build the storefront.") {
		t.Fatalf("%+v", a.Metadata)
	}

	// Deleting a lead is refused; an agent nobody reports to goes.
	base = c.catalogVersion("acme")
	if code, out = do("DELETE", "/catalog/agents/web-product-owner?base="+strconv.FormatInt(base, 10), nil); code != 400 || !strings.Contains(out, "leads team web") {
		t.Fatal(code, out)
	}
	if code, out = do("DELETE", "/catalog/agents/ios?base="+strconv.FormatInt(base, 10), nil); code != 200 {
		t.Fatal(code, out)
	}
	if _, ok := c.cat("acme").Agents["ios"]; ok {
		t.Fatal("not deleted")
	}

	// Setup: a custom agent needing a secret, no node, teams without projects.
	base = c.catalogVersion("acme")
	custom := c.cat("acme").Agents["developer"]
	custom.Metadata = config.Metadata{Name: "deployer", Description: "Deploys"}
	custom.Spec.Secrets = []config.SecretUse{{Name: "DEPLOY_TOKEN", Env: "DEPLOY_TOKEN"}}
	custom.Spec.Prompt.File = ""
	if code, out = do("POST", "/catalog/agents", AgentCreate{Base: base, Team: "product", Agent: &custom, Prompt: "Deploy."}); code != 200 {
		t.Fatal(code, out)
	}
	code, out = do("GET", "/setup", nil)
	if code != 200 || !strings.Contains(out, `"name":"DEPLOY_TOKEN"`) || !strings.Contains(out, `"no_node":true`) || !strings.Contains(out, `"teams_without_projects":["product","web"]`) || !strings.Contains(out, `"ready":false`) {
		t.Fatal(code, out)
	}

	// An agent copied from an older release of its preset gets an update hint.
	cur, _ := s.CurrentCatalog(ctx, "acme")
	docs := cloneDocs(cur.Docs)
	docs["configs/acme/agents/qa.yaml"] = strings.Replace(docs["configs/acme/agents/qa.yaml"], "preset: qa@", "preset: qa@0ld", 1)
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Base: cur.Version, Docs: docs, Author: "test"}); err != nil {
		t.Fatal(err)
	}
	if code, out = do("GET", "/catalog/agents/qa", nil); code != 200 || !strings.Contains(out, `"update":{"preset":"qa"`) {
		t.Fatal(code, out)
	}
}
