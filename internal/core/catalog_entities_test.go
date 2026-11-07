package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"

	"appfactory/internal/storage"
)

// TestCatalogEntitiesRoundTrip (structured-catalog 1.1): the catalog imported into entities renders a catalog with
// the same resolved agents (config hash per agent), the condition for switching a company over.
func TestCatalogEntitiesRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	h.c.Root, h.c.Agents = "../..", nil
	if err := h.c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	co := storage.DefaultCompany
	n, err := h.s.ImportCatalogEntities(ctx, co, 0, "test")
	if err != nil || n == 0 {
		t.Fatal(n, err)
	}
	es, err := h.s.CatalogEntities(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, e := range es {
		kinds[e.Kind]++
	}
	if kinds["agent"] == 0 || kinds["team"] == 0 || kinds["prompt"] == 0 {
		t.Fatalf("kinds %v", kinds)
	}
	rendered, err := h.c.loadCatalog(co, storage.RenderEntities(es))
	if err != nil {
		t.Fatal(err)
	}
	hash := func(v any) [32]byte { b, _ := json.Marshal(v); return sha256.Sum256(b) }
	current := h.c.cat(co)
	if len(rendered.Agents) != len(current.Agents) {
		t.Fatal(len(rendered.Agents), len(current.Agents))
	}
	for name, a := range current.Agents {
		if hash(rendered.Agents[name]) != hash(a) {
			t.Fatalf("agent %s resolves differently from the entities", name)
		}
	}
}

// TestCatalogEntityAPI (structured-catalog 1.2–1.4): switch to entities, rename a team by its title through the
// entity API, see it in the change log, undo it, and export/import the catalog as YAML.
func TestCatalogEntityAPI(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	h.c.Root, h.c.Agents = "../..", nil
	if err := h.c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	co := storage.DefaultCompany
	if w := h.http("owner", "POST", "/catalog/entities/switch", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if src, _ := h.s.CatalogSource(ctx, co); src != "entities" {
		t.Fatal(src)
	}
	es, _ := h.s.CatalogEntities(ctx, co)
	var team storage.CatalogEntity
	for _, e := range es {
		if e.Kind == "team" {
			team = e
			break
		}
	}
	title := "The Core Crew"
	if w := h.http("owner", "PUT", "/catalog/entities/team/"+team.Name, map[string]any{"row_version": team.RowVersion, "title": title, "note": "rename"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := h.http("owner", "PUT", "/catalog/entities/team/"+team.Name, map[string]any{"row_version": team.RowVersion, "title": "stale"}); w.Code != 409 {
		t.Fatal("a stale row_version was accepted", w.Code)
	}
	es, _ = h.s.CatalogEntities(ctx, co)
	for _, e := range es {
		if e.Kind == "team" && e.Name == team.Name && e.Title != title {
			t.Fatalf("title %q", e.Title)
		}
	}
	changes, _ := h.s.CatalogChanges(ctx, co)
	if len(changes) == 0 || len(changes[0].Changes) != 1 {
		t.Fatalf("%+v", changes)
	}
	if w := h.http("owner", "POST", "/catalog/changes/"+itoa(changes[0].Seq)+"/undo", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	es, _ = h.s.CatalogEntities(ctx, co)
	for _, e := range es {
		if e.Kind == "team" && e.Name == team.Name && e.Title == title {
			t.Fatal("undo kept the title")
		}
	}
	cur, _ := h.s.CurrentCatalog(ctx, co)
	back, err := ImportYAML(ExportYAML(cur.Docs))
	if err != nil || len(back) != len(cur.Docs) {
		t.Fatal(len(back), len(cur.Docs), err)
	}
	for p, c := range cur.Docs {
		if strings.TrimRight(back[p], "\n") != strings.TrimRight(c, "\n") {
			t.Fatalf("%s does not round-trip:\n%q\n%q", p, c, back[p])
		}
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
