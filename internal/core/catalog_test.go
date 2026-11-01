package core

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// TestStoredCatalogs (company-self-service D1, D2, D4): Core seeds its own company's catalog from the
// repository, serves a second company's stored catalog side by side, swaps a saved version without a restart,
// and refuses a version the loader rejects or one saved on a stale base.
func TestStoredCatalogs(t *testing.T) {
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
	disk, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.cat(storage.DefaultCompany); len(got.Agents) != len(disk.Agents) || got.Agents["manager"].Instructions != disk.Agents["manager"].Instructions || c.catalogVersion(storage.DefaultCompany) != 1 {
		t.Fatalf("seed: %d agents, version %d", len(got.Agents), c.catalogVersion(storage.DefaultCompany))
	}
	if h, _ := s.CatalogHistory(ctx, storage.DefaultCompany); len(h) != 1 || h[0].Author != "seed" {
		t.Fatalf("%+v", h)
	}
	// A second start loads the stored version instead of seeding again.
	again := &Core{Store: s, Root: "../..", Token: "owner"}
	if err = again.LoadCatalogs(ctx); err != nil || again.catalogVersion(storage.DefaultCompany) != 1 {
		t.Fatal(err, again.catalogVersion(storage.DefaultCompany))
	}

	// Company acme: the smoke team under configs/acme/, with its own manager prompt.
	if err = s.CreateCompany(ctx, "acme", "Acme"); err != nil {
		t.Fatal(err)
	}
	smoke, err := config.SeedDocuments("../..", "smoke")
	if err != nil {
		t.Fatal(err)
	}
	acme := map[string]string{}
	for p, content := range smoke {
		acme[strings.Replace(p, "configs/smoke/", "configs/acme/", 1)] = content
	}
	acme["prompts/manager.md"] = "ACME MANAGER PROMPT"
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Docs: acme, Author: "owner"}); err != nil {
		t.Fatal(err)
	}
	if m := c.cat("acme").Agents["manager"]; !strings.HasPrefix(m.Instructions, "ACME MANAGER PROMPT") {
		t.Fatalf("acme manager: %q", m.Instructions)
	}
	if strings.HasPrefix(c.cat(storage.DefaultCompany).Agents["manager"].Instructions, "ACME") {
		t.Fatal("acme's prompt leaked into the default company")
	}
	if got := c.companies(); strings.Join(got, ",") != "acme,default" {
		t.Fatal(got)
	}

	// Apply without restart: the next version is the catalog at once; the old one stays in history.
	acme["prompts/manager.md"] = "ACME MANAGER V2"
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Base: 1, Docs: acme, Author: "owner"}); err != nil {
		t.Fatal(err)
	}
	if m := c.cat("acme").Agents["manager"]; !strings.HasPrefix(m.Instructions, "ACME MANAGER V2") || c.catalogVersion("acme") != 2 {
		t.Fatal(m.Instructions, c.catalogVersion("acme"))
	}

	// A version the loader rejects is not saved; a stale base conflicts.
	broken := map[string]string{}
	for p, content := range acme {
		broken[p] = content
	}
	broken["configs/acme/agents/manager.yaml"] = regexp.MustCompile(`extends: \S+`).ReplaceAllString(acme["configs/acme/agents/manager.yaml"], "extends: missing-profile")
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Base: 2, Docs: broken, Author: "owner"}); !errors.Is(err, storage.ErrInput) || !strings.Contains(err.Error(), "missing-profile") || strings.Contains(err.Error(), "../") {
		t.Fatal("broken catalog saved:", err)
	}
	if _, err = c.SaveCatalog(ctx, "acme", storage.CatalogSave{Base: 1, Docs: acme, Author: "owner"}); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("stale base accepted:", err)
	}
	if c.catalogVersion("acme") != 2 {
		t.Fatal(c.catalogVersion("acme"))
	}
}
