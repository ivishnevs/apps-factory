package core

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strconv"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Catalog API (company-self-service 3.5): the owner reads the company's stored catalog, changes documents on
// top of the version it read (put/delete; the full set is validated by the loader), validates without saving,
// lists history and rolls back (a new version with an older version's documents).

// CatalogChange is a change on top of Base: Put adds or replaces documents, Delete removes paths.
type CatalogChange struct {
	Base   int64             `json:"base"`
	Put    map[string]string `json:"put,omitempty"`
	Delete []string          `json:"delete,omitempty"`
	Note   string            `json:"note,omitempty"`
	DryRun bool              `json:"dry_run,omitempty"`
}

// TeamView is one team of the resolved catalog.
type TeamView struct {
	Name          string       `json:"name"`
	Description   string       `json:"description,omitempty"`
	Lead          string       `json:"lead"`
	AuditAssignee string       `json:"audit_assignee"`
	Profile       string       `json:"profile,omitempty"`
	Agents        []TeamMember `json:"agents"`
	Projects      []string     `json:"projects"`
}

// teamViews are company's teams with their agents and projects.
func (c *Core) teamViews(company string, projects []tasks.Project) []TeamView {
	cat := c.cat(company)
	out := []TeamView{}
	for _, name := range cat.TeamNames() {
		t := cat.Teams[name]
		v := TeamView{Name: name, Description: t.Metadata.Description, Lead: t.Spec.Lead, AuditAssignee: t.Auditor(), Profile: t.Spec.Profile, Agents: []TeamMember{}, Projects: []string{}}
		for _, agent := range cat.TeamAgents(name) {
			m := teamMember(cat.Agents, agent)
			if dir, err := config.CompanyDir(company); err == nil {
				m.Document = path.Join(filepath.ToSlash(dir), "agents", agent+".yaml")
			}
			v.Agents = append(v.Agents, m)
		}
		for _, p := range projects {
			if p.Team == name {
				v.Projects = append(v.Projects, p.ID)
			}
		}
		out = append(out, v)
	}
	return out
}

// teamMember is agent as the catalog resolves it.
func teamMember(agents map[string]config.Agent, name string) TeamMember {
	a := agents[name]
	required, reviewer := a.Review()
	m := TeamMember{Name: name, Team: a.Spec.Team, Skills: append([]string{}, a.Spec.Skills.Include...), Description: a.Metadata.Description, Role: a.Spec.Role, ReportsTo: config.Manager(agents, name), Prompt: a.Spec.Prompt.File,
		Provider: a.Spec.Runtime.Provider, Model: a.Spec.Runtime.Model, CreatesTasks: a.Spec.Communication.CanCreateTasks, ReviewRequired: required, Reviewer: config.ReviewerFor(agents, name, reviewer)}
	for _, p := range a.ReviewPaths() {
		m.ReviewPaths = append(m.ReviewPaths, p.Match+" → "+config.ReviewerFor(agents, name, p.Reviewer))
	}
	return m
}

// TeamMember is one agent of the resolved catalog.
type TeamMember struct {
	Name string `json:"name"`
	Team string `json:"team,omitempty"`
	// Document is the agent's catalog document; Skills its own spec.skills.include (the skill picker edits both).
	Document       string   `json:"document,omitempty"`
	Skills         []string `json:"skills"`
	Description    string   `json:"description,omitempty"`
	Role           string   `json:"role"`
	ReportsTo      string   `json:"reports_to"`
	Prompt         string   `json:"prompt"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model,omitempty"`
	CreatesTasks   bool     `json:"creates_tasks,omitempty"`
	ReviewRequired bool     `json:"review_required,omitempty"`
	Reviewer       string   `json:"reviewer,omitempty"`
	ReviewPaths    []string `json:"review_paths,omitempty"`
}

// CatalogDocument is a document's path and size (and content when read).
type CatalogDocument struct {
	Path    string `json:"path"`
	Size    int    `json:"size"`
	Content string `json:"content,omitempty"`
}

// CatalogView is a version with its document list.
type CatalogView struct {
	storage.CatalogVersion
	Company   string            `json:"company"`
	Documents []CatalogDocument `json:"documents"`
}

func catalogView(company string, cat storage.Catalog) CatalogView {
	v := CatalogView{CatalogVersion: cat.CatalogVersion, Company: company, Documents: []CatalogDocument{}}
	for p, content := range cat.Docs {
		v.Documents = append(v.Documents, CatalogDocument{Path: p, Size: len(content)})
	}
	sort.Slice(v.Documents, func(i, j int) bool { return v.Documents[i].Path < v.Documents[j].Path })
	return v
}

// changedDocs applies ch to the documents of version ch.Base.
func (c *Core) changedDocs(ctx context.Context, company string, ch CatalogChange) (map[string]string, error) {
	base, err := c.Store.CatalogAt(ctx, company, ch.Base)
	if err != nil {
		return nil, err
	}
	if ch.Base == 0 || base.Version != ch.Base {
		return nil, fmt.Errorf("%w: base version required (the version the change was made on)", storage.ErrInput)
	}
	docs := map[string]string{}
	for p, content := range base.Docs {
		docs[p] = content
	}
	for _, p := range ch.Delete {
		if _, ok := docs[p]; !ok {
			return nil, fmt.Errorf("%w: %s is not a document of version %d", storage.ErrInput, p, ch.Base)
		}
		delete(docs, p)
	}
	for p, content := range ch.Put {
		docs[p] = content
	}
	return docs, nil
}

// ApplyCatalogChange validates ch on top of its base and, unless it is a dry run, saves it as the next version.
func (c *Core) ApplyCatalogChange(ctx context.Context, company, author string, ch CatalogChange) (storage.CatalogVersion, error) {
	docs, err := c.changedDocs(ctx, company, ch)
	if err != nil {
		return storage.CatalogVersion{}, err
	}
	if ch.DryRun {
		cat, err := c.loadCatalog(company, docs)
		if err != nil {
			return storage.CatalogVersion{}, fmt.Errorf("%w: %v", storage.ErrInput, err)
		}
		return storage.CatalogVersion{Version: ch.Base, Documents: len(docs), Warnings: append(config.ReviewCoverage(cat), config.CapabilityWarnings(cat)...)}, nil
	}
	v, err := c.SaveCatalog(ctx, company, storage.CatalogSave{Base: ch.Base, Docs: docs, Author: author, Note: ch.Note})
	if err == nil {
		v.Warnings = append(config.ReviewCoverage(c.cat(company)), config.CapabilityWarnings(c.cat(company))...)
	}
	return v, err
}

func (c *Core) catalogRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /catalog", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		version, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		cat, err := c.Store.CatalogAt(r.Context(), company, version)
		if err != nil {
			reply(w, nil, err)
			return
		}
		write(w, catalogView(company, cat))
	})
	// The teams as the catalog resolves them (company-teams 3.2): each team's lead, audit assignee, profile,
	// agents (who does what and reports to whom) and projects.
	m.HandleFunc("GET /catalog/teams", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		projects, err := c.Store.Projects(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		write(w, map[string]any{"company": company, "version": c.catalogVersion(company), "teams": c.teamViews(company, projects)})
	})
	m.HandleFunc("GET /catalog/history", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.CatalogHistory(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
	m.HandleFunc("GET /catalog/document", func(w http.ResponseWriter, r *http.Request) {
		version, _ := strconv.ParseInt(r.URL.Query().Get("version"), 10, 64)
		cat, err := c.Store.CatalogAt(r.Context(), c.companyFrom(r), version)
		if err != nil {
			reply(w, nil, err)
			return
		}
		p := r.URL.Query().Get("path")
		content, ok := cat.Docs[p]
		if !ok {
			http.Error(w, fmt.Sprintf("no document %q in version %d", p, cat.Version), 404)
			return
		}
		write(w, map[string]any{"version": cat.Version, "path": p, "size": len(content), "content": content})
	})
	m.HandleFunc("POST /catalog", func(w http.ResponseWriter, r *http.Request) {
		var ch CatalogChange
		if !decodeMax(w, r, &ch, 64<<20) {
			return
		}
		v, err := c.ApplyCatalogChange(r.Context(), c.companyFrom(r), author(r), ch)
		reply(w, v, err)
	})
	m.HandleFunc("POST /catalog/rollback", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Base int64  `json:"base"`
			To   int64  `json:"to"`
			Note string `json:"note"`
		}
		if !decode(w, r, &p) {
			return
		}
		company := c.companyFrom(r)
		old, err := c.Store.CatalogAt(r.Context(), company, p.To)
		if err == nil && (p.To == 0 || old.Version != p.To) {
			err = fmt.Errorf("%w: to: an earlier version required", storage.ErrInput)
		}
		if err != nil {
			reply(w, nil, err)
			return
		}
		note := p.Note
		if note == "" {
			note = fmt.Sprintf("rollback to version %d", p.To)
		}
		v, err := c.SaveCatalog(r.Context(), company, storage.CatalogSave{Base: p.Base, Docs: old.Docs, Author: author(r), Note: note})
		reply(w, v, err)
	})
}

// catalogTool is factory_catalog_get and the approved factory_catalog_change of an agent (3.7).
func (c *Core) catalogTool(w http.ResponseWriter, r *http.Request, name string, args map[string]any, run storage.Run) {
	company := c.companyFrom(r)
	version := int64(0)
	if v, ok := args["version"].(float64); ok {
		version = int64(v)
	}
	if name == "factory_catalog_get" {
		cat, err := c.Store.CatalogAt(r.Context(), company, version)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if p, _ := args["path"].(string); p != "" {
			content, ok := cat.Docs[p]
			if !ok {
				http.Error(w, fmt.Sprintf("no document %q in version %d", p, cat.Version), 404)
				return
			}
			write(w, map[string]any{"version": cat.Version, "path": p, "content": content})
			return
		}
		write(w, catalogView(company, cat))
		return
	}
	ch := CatalogChange{Put: map[string]string{}}
	if v, ok := args["base"].(float64); ok {
		ch.Base = int64(v)
	}
	ch.Note, _ = args["note"].(string)
	if put, ok := args["put"].(map[string]any); ok {
		for p, v := range put {
			content, ok := v.(string)
			if !ok {
				http.Error(w, "put."+p+": string content required", 400)
				return
			}
			ch.Put[p] = content
		}
	}
	if del, ok := args["delete"].([]any); ok {
		for _, v := range del {
			if p, ok := v.(string); ok {
				ch.Delete = append(ch.Delete, p)
			}
		}
	}
	v, err := c.ApplyCatalogChange(r.Context(), company, run.Spec.Agent, ch)
	reply(w, v, err)
}
