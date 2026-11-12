package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"appfactory/internal/config"
	"appfactory/internal/presets"
	"appfactory/internal/storage"
)

// Team builder (team-builder D1–D4): the release's presets, teams and agents copied from them into a company's
// catalog (every write is a catalog save on a base version), agent documents edited as fields, and the setup status
// that names what keeps work from running.

var presetCache struct {
	sync.Mutex
	root string
	lib  *presets.Library
}

// presetLibrary is the release's preset library under Root (loaded once).
func (c *Core) presetLibrary() (*presets.Library, error) {
	if c.Root == "" {
		return nil, fmt.Errorf("%w: this Core has no release root with presets", storage.ErrInput)
	}
	presetCache.Lock()
	defer presetCache.Unlock()
	if presetCache.lib != nil && presetCache.root == c.Root {
		return presetCache.lib, nil
	}
	lib, err := presets.Load(c.Root)
	if err != nil {
		return nil, err
	}
	presetCache.root, presetCache.lib = c.Root, lib
	return lib, nil
}

// templateDocuments are a new company's documents: the base documents and a copy of team preset template whose agents
// keep their preset keys as names (the company's first team).
func (c *Core) templateDocuments(company, template string) (map[string]string, error) {
	lib, err := c.presetLibrary()
	if err != nil {
		return nil, err
	}
	t := lib.Teams[template]
	if t == nil {
		return nil, fmt.Errorf("unknown template %q", template)
	}
	docs, err := lib.Base(company)
	if err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, a := range t.Spec.Agents {
		names[a.Key] = a.Key
	}
	return lib.CopyTeam(company, presets.TeamCopy{Preset: template, Names: names}, docs)
}

func (c *Core) templates() []string {
	lib, err := c.presetLibrary()
	if err != nil {
		return nil
	}
	return lib.TeamNames()
}

// TeamCreate is the body of POST /catalog/teams: a team from a preset (names override its agents' names).
type TeamCreate struct {
	Base int64 `json:"base"`
	presets.TeamCopy
}

// AgentCreate is the body of POST /catalog/agents: an agent from a preset with overrides, or a custom Agent document.
type AgentCreate struct {
	Base       int64                  `json:"base"`
	Team       string                 `json:"team"`
	FromPreset string                 `json:"from_preset"`
	Name       string                 `json:"name"`
	Overrides  presets.AgentOverrides `json:"overrides"`
	Agent      *config.Agent          `json:"agent,omitempty"`
	Prompt     string                 `json:"prompt,omitempty"`
}

// AgentDoc is an agent's document and prompt as GET/PUT /catalog/agents/{name} carry them.
type AgentDoc struct {
	Base   int64        `json:"base,omitempty"`
	Agent  config.Agent `json:"agent"`
	Prompt string       `json:"prompt"`
	// Update is a newer release of the agent's preset (D2: a hint, nothing changes until it is saved).
	Update *PresetUpdate `json:"update,omitempty"`
}

// PresetUpdate is a newer version of a copy's preset and how its prompt differs.
type PresetUpdate struct {
	Preset  string   `json:"preset"`
	From    string   `json:"from"`
	To      string   `json:"to"`
	Prompt  string   `json:"prompt"`
	Changes []string `json:"changes"`
}

// FieldError is a refusal naming the field it is about (D4: loader errors map back to the form).
type FieldError struct {
	Field string `json:"field"`
	Error string `json:"error"`
}

func (c *Core) presetRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /presets/agents", func(w http.ResponseWriter, r *http.Request) {
		lib, err := c.presetLibrary()
		if err != nil {
			reply(w, nil, err)
			return
		}
		out := []*presets.Agent{}
		for _, n := range sortedKeys(lib.Agents) {
			out = append(out, lib.Agents[n])
		}
		reply(w, out, nil)
	})
	m.HandleFunc("GET /presets/teams", func(w http.ResponseWriter, r *http.Request) {
		lib, err := c.presetLibrary()
		if err != nil {
			reply(w, nil, err)
			return
		}
		out := []*presets.Team{}
		for _, n := range lib.TeamNames() {
			out = append(out, lib.Teams[n])
		}
		reply(w, out, nil)
	})
	m.HandleFunc("POST /catalog/teams", func(w http.ResponseWriter, r *http.Request) {
		var p TeamCreate
		if !decode(w, r, &p) {
			return
		}
		c.catalogEdit(w, r, p.Base, "team "+cmpTeam(p.Team, p.Preset)+" from preset "+p.Preset, func(lib *presets.Library, company string, docs map[string]string) (map[string]string, error) {
			return lib.CopyTeam(company, p.TeamCopy, docs)
		})
	})
	m.HandleFunc("POST /catalog/agents", func(w http.ResponseWriter, r *http.Request) {
		var p AgentCreate
		if !decode(w, r, &p) {
			return
		}
		c.catalogEdit(w, r, p.Base, "agent "+p.Name+" in team "+p.Team, func(lib *presets.Library, company string, docs map[string]string) (map[string]string, error) {
			if p.FromPreset != "" {
				out, _, err := lib.CopyAgent(company, p.Team, p.FromPreset, p.Name, p.Overrides, docs)
				return out, err
			}
			if p.Agent == nil {
				return nil, fieldErr("from_preset", "an agent preset or a custom agent required")
			}
			if p.Agent.Metadata.Name == "" {
				p.Agent.Metadata.Name = p.Name
			}
			p.Agent.Spec.Team = p.Team
			return putAgentDoc(company, docs, *p.Agent, p.Prompt, true)
		})
	})
	m.HandleFunc("GET /catalog/agents/{name}", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		cur, err := c.Store.CurrentCatalog(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		doc, err := agentDoc(company, cur.Docs, r.PathValue("name"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		doc.Base = cur.Version
		if lib, err := c.presetLibrary(); err == nil {
			doc.Update = presetUpdate(lib, doc)
		}
		reply(w, doc, nil)
	})
	m.HandleFunc("PUT /catalog/agents/{name}", func(w http.ResponseWriter, r *http.Request) {
		var p AgentDoc
		if !decode(w, r, &p) {
			return
		}
		p.Agent.Metadata.Name = r.PathValue("name")
		kinds, err := c.Store.SecretKinds(r.Context(), c.companyFrom(r))
		if err != nil {
			reply(w, nil, err)
			return
		}
		c.catalogEdit(w, r, p.Base, "agent "+p.Agent.Metadata.Name+" edited", func(lib *presets.Library, company string, docs map[string]string) (map[string]string, error) {
			if old, err := agentDoc(company, docs, p.Agent.Metadata.Name); err == nil { // agent-secrets-ui: no wrong-kind use
				if err = checkSecretKinds(old.Agent.Spec.Secrets, p.Agent.Spec.Secrets, kinds); err != nil {
					return nil, err
				}
			}
			return putAgentDoc(company, docs, p.Agent, p.Prompt, false)
		})
	})
	m.HandleFunc("DELETE /catalog/agents/{name}", func(w http.ResponseWriter, r *http.Request) {
		var base int64
		fmt.Sscan(r.URL.Query().Get("base"), &base)
		n := r.PathValue("name")
		c.catalogEdit(w, r, base, "agent "+n+" deleted", func(lib *presets.Library, company string, docs map[string]string) (map[string]string, error) {
			cat := c.cat(company)
			for _, t := range cat.Teams {
				if t.Spec.Lead == n {
					return nil, fieldErr("name", n+" leads team "+t.Metadata.Name+": make another agent its lead first")
				}
			}
			for an, a := range cat.Agents {
				if a.Spec.ReportsTo == n {
					return nil, fieldErr("name", an+" reports to "+n)
				}
			}
			dir, _ := config.CompanyDir(company)
			p := path.Join(filepath.ToSlash(dir), "agents", n+".yaml")
			if _, ok := docs[p]; !ok {
				return nil, fmt.Errorf("%w: no agent %s", storage.ErrNotFound, n)
			}
			out := cloneDocs(docs)
			delete(out, p)
			if a, ok := cat.Agents[n]; ok && a.Spec.Prompt.File == "prompts/"+n+".md" {
				delete(out, "prompts/"+n+".md")
			}
			return out, nil
		})
	})
	m.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.setupStatus(r.Context(), c.companyFrom(r))
		reply(w, v, err)
	})
}

func cmpTeam(team, preset string) string {
	if team != "" {
		return team
	}
	return "(" + preset + ")"
}

func fieldErr(field, msg string) error {
	return &fieldError{FieldError{Field: field, Error: msg}}
}

type fieldError struct{ FieldError }

func (e *fieldError) Error() string { return e.Field + ": " + e.FieldError.Error }

// catalogEdit builds the next documents with build and saves them on base: a stale base is 409, a document the loader
// refuses is 400 naming the field it is about when it can tell.
func (c *Core) catalogEdit(w http.ResponseWriter, r *http.Request, base int64, note string, build func(*presets.Library, string, map[string]string) (map[string]string, error)) {
	company := c.companyFrom(r)
	cur, err := c.Store.CurrentCatalog(r.Context(), company)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if base != cur.Version {
		jsonStatus(w, http.StatusConflict, map[string]any{"error": fmt.Sprintf("the catalog is at version %d, not %d: reload it", cur.Version, base), "version": cur.Version})
		return
	}
	lib, _ := c.presetLibrary()
	docs, err := build(lib, company, cur.Docs)
	if err == nil {
		var v storage.CatalogVersion
		v, err = c.SaveCatalog(r.Context(), company, storage.CatalogSave{Base: base, Docs: docs, Author: author(r), Note: note})
		if err == nil {
			reply(w, v, nil)
			return
		}
	}
	var fe *fieldError
	switch {
	case errors.As(err, &fe):
		jsonStatus(w, http.StatusBadRequest, fe.FieldError)
	case errors.Is(err, presets.ErrInput), errors.Is(err, storage.ErrInput):
		jsonStatus(w, http.StatusBadRequest, FieldError{Field: guessField(err.Error()), Error: err.Error()})
	default:
		reply(w, nil, err)
	}
}

// guessField maps a loader or copy error to the agent form's field (D4).
func guessField(msg string) string {
	for _, f := range []struct{ needle, field string }{{"can_assign_tasks", "can_assign_tasks"}, {"secrets: ", "secrets"}, {"context_window", "context_window"}, {"conversation.max_concurrent_runs", "conversation_max_runs"}, {"max_concurrent_runs", "max_concurrent_runs"}, {"reports_to", "reports_to"}, {"reports to", "reports_to"}, {"can_message", "can_message"},
		{"quota_key", "quota_key"}, {"provider", "provider"}, {"model", "model"}, {"skill", "skills"}, {"polic", "policies"}, {"prompt", "prompt"},
		{"team", "team"}, {"agent name", "name"}, {"exists", "name"}, {"role", "role"}} {
		if strings.Contains(strings.ToLower(msg), f.needle) {
			return f.field
		}
	}
	return ""
}

// agentDoc reads an agent's document and its company prompt.
func agentDoc(company string, docs map[string]string, n string) (AgentDoc, error) {
	dir, err := config.CompanyDir(company)
	if err != nil {
		return AgentDoc{}, err
	}
	text, ok := docs[path.Join(filepath.ToSlash(dir), "agents", n+".yaml")]
	if !ok {
		return AgentDoc{}, fmt.Errorf("%w: no agent %s", storage.ErrNotFound, n)
	}
	var a config.Agent
	if err = yaml.Unmarshal([]byte(text), &a); err != nil {
		return AgentDoc{}, err
	}
	return AgentDoc{Agent: a, Prompt: docs[a.Spec.Prompt.File]}, nil
}

// putAgentDoc writes an agent document (and its prompt as prompts/<name>.md when given); create refuses a taken name.
func putAgentDoc(company string, docs map[string]string, a config.Agent, prompt string, create bool) (map[string]string, error) {
	dir, err := config.CompanyDir(company)
	if err != nil {
		return nil, err
	}
	n := a.Metadata.Name
	if !config.ValidName(n) {
		return nil, fieldErr("name", "lowercase letters, digits and '-'")
	}
	p := path.Join(filepath.ToSlash(dir), "agents", n+".yaml")
	if _, ok := docs[p]; ok == create {
		if create {
			return nil, fieldErr("name", "agent "+n+" exists (names are unique per company)")
		}
		return nil, fmt.Errorf("%w: no agent %s", storage.ErrNotFound, n)
	}
	out := cloneDocs(docs)
	if prompt != "" {
		a.Spec.Prompt.File = "prompts/" + n + ".md"
		out[a.Spec.Prompt.File] = prompt
	}
	a.APIVersion, a.Kind = "factory/v1", "Agent"
	b, err := agentYAML(a)
	if err != nil {
		return nil, err
	}
	out[p] = string(b)
	return out, nil
}

// agentYAML is an agent's document as written in the catalog: its own fields only (no computed instructions, rules
// or skills), without null or empty values.
func agentYAML(a config.Agent) ([]byte, error) {
	j, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err = json.Unmarshal(j, &m); err != nil {
		return nil, err
	}
	for _, k := range []string{"instructions", "rules", "available_skills", "skill_hashes", "mcp_servers"} {
		delete(m, k)
	}
	prune(m)
	// The assignment scope is written as its word or list: none would be JSON false, which prune drops, and an
	// agent with reports would then assign to them again (team-page-clarity 2.4).
	if s := a.Spec.Communication.CanAssignTasks; s.Set() {
		v, err := s.MarshalYAML()
		if err != nil {
			return nil, err
		}
		sub(sub(m, "spec"), "communication")["can_assign_tasks"] = v
	}
	return yaml.Marshal(m)
}

// prune drops null, empty and zero values (an unset field inherits from the agent's profile).
func prune(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			e = prune(e)
			switch y := e.(type) {
			case nil:
				delete(x, k)
				continue
			case string:
				if y == "" {
					delete(x, k)
					continue
				}
			case bool:
				if !y {
					delete(x, k)
					continue
				}
			case float64:
				if y == 0 {
					delete(x, k)
					continue
				}
			case map[string]any:
				if len(y) == 0 {
					delete(x, k)
					continue
				}
			case []any:
				if len(y) == 0 {
					delete(x, k)
					continue
				}
			}
			x[k] = e
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = prune(e)
		}
	}
	return v
}

// presetUpdate is the hint for an agent copied from an older release of its preset.
func presetUpdate(lib *presets.Library, doc AgentDoc) *PresetUpdate {
	ref := doc.Agent.Metadata.Preset
	name, ver, ok := strings.Cut(ref, "@")
	if !ok {
		return nil
	}
	a := lib.Agents[name]
	if a == nil || a.Version == ver {
		return nil
	}
	u := &PresetUpdate{Preset: name, From: ver, To: a.Version, Prompt: a.Prompt}
	for _, l := range lineDiff(doc.Prompt, a.Prompt) {
		u.Changes = append(u.Changes, l)
	}
	return u
}

// lineDiff lists lines removed (-) and added (+) between two texts (a short summary, not a full diff).
func lineDiff(before, after string) []string {
	in := map[string]bool{}
	for _, l := range strings.Split(before, "\n") {
		in[l] = true
	}
	out := map[string]bool{}
	for _, l := range strings.Split(after, "\n") {
		out[l] = true
	}
	var d []string
	for _, l := range strings.Split(before, "\n") {
		if !out[l] && strings.TrimSpace(l) != "" {
			d = append(d, "- "+l)
		}
	}
	for _, l := range strings.Split(after, "\n") {
		if !in[l] && strings.TrimSpace(l) != "" {
			d = append(d, "+ "+l)
		}
	}
	return d
}

// Setup is what keeps a company's work from running (D4): it never refuses anything, it explains.
type Setup struct {
	MissingSecrets []NeededSecret `json:"missing_secrets"`
	NoNode         bool           `json:"no_node"`
	TeamsWithout   []string       `json:"teams_without_projects"`
	Ready          bool           `json:"ready"`
}

func (c *Core) setupStatus(ctx context.Context, company string) (Setup, error) {
	out := Setup{MissingSecrets: []NeededSecret{}, TeamsWithout: []string{}}
	kinds, err := c.Store.SecretKinds(ctx, company)
	if err != nil {
		return out, err
	}
	for _, n := range c.neededSecrets(company, kinds) {
		if !n.Set {
			out.MissingSecrets = append(out.MissingSecrets, n)
		}
	}
	nodes, err := c.Store.Nodes(ctx, company)
	if err != nil {
		return out, err
	}
	out.NoNode = len(nodes) == 0
	projects, err := c.Store.Projects(ctx, company)
	if err != nil {
		return out, err
	}
	has := map[string]bool{}
	for _, p := range projects {
		has[p.Team] = true
	}
	cat := c.cat(company)
	teams := make([]string, 0, len(cat.Teams))
	for n := range cat.Teams {
		teams = append(teams, n)
	}
	sort.Strings(teams)
	for _, t := range teams {
		if !has[t] && !(len(cat.Teams) == 1 && has[""]) {
			out.TeamsWithout = append(out.TeamsWithout, t)
		}
	}
	out.Ready = len(out.MissingSecrets) == 0 && !out.NoNode && len(out.TeamsWithout) == 0
	return out, nil
}

func cloneDocs(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
