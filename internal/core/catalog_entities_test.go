package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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
