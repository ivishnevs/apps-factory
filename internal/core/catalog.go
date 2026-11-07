package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"appfactory/internal/config"
	"appfactory/internal/skills"
	"appfactory/internal/storage"
)

// Catalogs per company (company-self-service D1, D2, D4): with Root set, every company's current stored
// catalog version is loaded and swapped after each save; the catalog of a request is its company's.

type storedCatalog struct {
	version int64
	catalog *config.Catalog
}

// emptyCatalog is the catalog of a company that has none: no agents, no skills.
func emptyCatalog() *config.Catalog {
	return &config.Catalog{Agents: map[string]config.Agent{}, Profiles: map[string]config.Agent{}, Skills: &skills.Registry{Skills: map[string]skills.Skill{}}}
}

// cat is company's catalog: its stored catalog, else (without stored catalogs) Core's fields for Core's own
// company, else an empty one.
func (c *Core) cat(company string) *config.Catalog {
	if company == "" {
		company = c.company()
	}
	c.catalogMu.RLock()
	sc := c.catalogs[company]
	c.catalogMu.RUnlock()
	if sc != nil {
		return sc.catalog
	}
	if c.Root == "" && company == c.company() {
		reg := c.Skills
		if reg == nil {
			reg = &skills.Registry{Skills: map[string]skills.Skill{}}
		}
		return &config.Catalog{Agents: c.Agents, Skills: reg, NodeHooks: c.NodeHooks, Teams: c.Teams}
	}
	return emptyCatalog()
}

// catalogVersion is company's loaded stored version (0 without one).
func (c *Core) catalogVersion(company string) int64 {
	c.catalogMu.RLock()
	defer c.catalogMu.RUnlock()
	if sc := c.catalogs[company]; sc != nil {
		return sc.version
	}
	return 0
}

// companies are the companies Core runs its loops for: its own and every company with a stored catalog.
func (c *Core) companies() []string {
	set := map[string]bool{c.company(): true}
	c.catalogMu.RLock()
	for co := range c.catalogs {
		set[co] = true
	}
	c.catalogMu.RUnlock()
	out := make([]string, 0, len(set))
	for co := range set {
		out = append(out, co)
	}
	sort.Strings(out)
	return out
}

// loadCatalog loads company's documents as its catalog (validation is the same load).
func (c *Core) loadCatalog(company string, docs map[string]string) (*config.Catalog, error) {
	if c.Store == nil {
		return config.LoadStoredCatalog(c.Root, company, docs)
	}
	return config.LoadStoredCatalogWith(c.Root, company, docs, c.resolveSkill)
}

// LoadCatalogs loads the current stored catalog of every company that has one, seeding Core's own company
// from the repository when it has none yet. Without Root it does nothing.
func (c *Core) LoadCatalogs(ctx context.Context) error {
	if c.Root == "" {
		return nil
	}
	// Installed skills resolve against the index, so the built-in skills are indexed first (skill-registry D4).
	if err := c.IndexBuiltinSkills(ctx); err != nil {
		return fmt.Errorf("indexing the built-in skills: %w", err)
	}
	if _, err := c.Store.CurrentCatalog(ctx, c.company()); errors.Is(err, storage.ErrNotFound) {
		if err = c.SeedCatalog(ctx, c.company()); err != nil {
			return fmt.Errorf("seeding the catalog of %s: %w", c.company(), err)
		}
	} else if err != nil {
		return err
	}
	companies, err := c.Store.CatalogCompanies(ctx)
	if err != nil {
		return err
	}
	for _, co := range companies {
		if err = c.reloadCatalog(ctx, co); err != nil {
			return fmt.Errorf("catalog of %s: %w", co, err)
		}
	}
	return nil
}

// SeedCatalog saves company's catalog directory of the repository as its first version (author seed).
func (c *Core) SeedCatalog(ctx context.Context, company string) error {
	docs, err := config.SeedDocuments(c.Root, company)
	if err != nil {
		return err
	}
	if _, err = c.SaveCatalog(ctx, company, storage.CatalogSave{Docs: docs, Author: "seed", Note: "seeded from the repository"}); err != nil {
		return err
	}
	slog.Info("catalog seeded from the repository", "company", company, "documents", len(docs))
	return nil
}

// reloadCatalog loads company's current stored version into Core.
func (c *Core) reloadCatalog(ctx context.Context, company string) error {
	cur, err := c.Store.CurrentCatalog(ctx, company)
	if err != nil {
		return err
	}
	if cur.Version == c.catalogVersion(company) {
		return nil
	}
	docs := cur.Docs
	// structured-catalog: a switched company's catalog is its entities (documents mirror them version by version).
	if src, err := c.Store.CatalogSource(ctx, company); err == nil && src == "entities" {
		es, err := c.Store.CatalogEntities(ctx, company)
		if err != nil {
			return err
		}
		docs = storage.RenderEntities(es)
	}
	cat, err := c.loadCatalog(company, docs)
	if err != nil {
		return err
	}
	c.catalogMu.Lock()
	if c.catalogs == nil {
		c.catalogs = map[string]*storedCatalog{}
	}
	if old := c.catalogs[company]; old == nil || old.version < cur.Version {
		c.catalogs[company] = &storedCatalog{version: cur.Version, catalog: cat}
	}
	c.catalogMu.Unlock()
	return nil
}

// SaveCatalog validates in.Docs with the loader, saves them as company's next version and makes them the
// company's catalog for everything that starts afterwards (D2).
func (c *Core) SaveCatalog(ctx context.Context, company string, in storage.CatalogSave) (storage.CatalogVersion, error) {
	if c.Root == "" {
		return storage.CatalogVersion{}, fmt.Errorf("%w: this Core serves its catalog from files (no FACTORY_ROOT catalog storage)", storage.ErrInput)
	}
	in.Validate = func(docs map[string]string) error {
		cat, err := c.loadCatalog(company, docs)
		if err != nil {
			return err
		}
		return c.teamsInUse(ctx, company, cat)
	}
	v, err := c.Store.SaveCatalog(ctx, company, in)
	if err != nil {
		return v, err
	}
	// structured-catalog: every save keeps the entities in step (the document API is the compatibility layer).
	if _, err = c.Store.ImportCatalogEntities(ctx, company, v.Version, in.Author); err != nil {
		return v, err
	}
	if err = c.reloadCatalog(ctx, company); err != nil {
		return v, err
	}
	c.catalogChanged(ctx, company)
	return v, nil
}

// catalogChanged applies what a new catalog means beyond lookups: conversation tasks of new agents.
func (c *Core) catalogChanged(ctx context.Context, company string) {
	c.wakeups.signal(company) // the NodeHooks document may have changed (hooks_changed in the poll answer)
	if c.Store == nil {
		return
	}
	if err := c.Store.EnsureConversationTasks(ctx, company, config.Names(c.cat(company).Agents)); err != nil {
		slog.Error("conversation tasks after a catalog change", "company", company, "error", err)
	}
}

// teamsInUse refuses a catalog that removes a team a project still belongs to (company-teams D4).
func (c *Core) teamsInUse(ctx context.Context, company string, cat *config.Catalog) error {
	projects, err := c.Store.Projects(ctx, company)
	if err != nil {
		return err
	}
	for _, p := range projects {
		if _, ok := cat.Teams[p.Team]; p.Team != "" && !ok {
			return fmt.Errorf("team %s is used by project %s: move the project to another team first", p.Team, p.ID)
		}
	}
	return nil
}
