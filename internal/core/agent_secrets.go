package core

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"appfactory/internal/config"
	"appfactory/internal/presets"
	"appfactory/internal/storage"
)

// Granting secrets to agents from the web (agent-secrets-ui): an agent's secret uses as the editor shows them (own,
// inherited from its profile, its MCP servers' header secrets) and two writes of spec.secrets, each a catalog change
// with a note: the agent's own uses from its editor, and one secret given to several agents from the Secrets page.
// Owners and admins only (requiredRole: writes under /catalog and /secrets); agents reach none of these routes.
// Core refuses a use whose secret is set with the other kind (a file given as a string variable or the reverse).

// AgentSecretUse is one secret an agent's runs get, with where it comes from and whether it is set with its kind.
type AgentSecretUse struct {
	Name string `json:"name"`
	Env  string `json:"env,omitempty"`
	// Kind is what the use needs: string (the value in Env) or file (the file's path in Env).
	Kind string `json:"kind"`
	// Profile names the profile an inherited use comes from; Server the external MCP server whose header needs it.
	// Both are read-only in the agent's editor.
	Profile string `json:"profile,omitempty"`
	Server  string `json:"server,omitempty"`
	// State: set, missing (no such secret) or wrong_kind (set with the other kind); SecretKind is the stored kind.
	State      string `json:"state"`
	SecretKind string `json:"secret_kind,omitempty"`
}

// AgentSecrets is GET /catalog/agents/{name}/secrets.
type AgentSecrets struct {
	Base int64            `json:"base"`
	Uses []AgentSecretUse `json:"uses"`
}

// AgentSecretsPut is the body of PUT /catalog/agents/{name}/secrets: the agent's own uses (inherited ones stay).
type AgentSecretsPut struct {
	Base    int64              `json:"base"`
	Secrets []config.SecretUse `json:"secrets"`
}

// SecretGrant is the body of POST /secrets/{name}/agents: give the secret to these agents in the variable Env
// (default: the secret's name), as a file when the secret is one.
type SecretGrant struct {
	Base   int64    `json:"base"`
	Agents []string `json:"agents"`
	Env    string   `json:"env,omitempty"`
}

const (
	secretSet       = "set"
	secretMissing   = "missing"
	secretWrongKind = "wrong_kind"
)

func (c *Core) agentSecretRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /catalog/agents/{name}/secrets", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.agentSecrets(r.Context(), c.companyFrom(r), r.PathValue("name"))
		reply(w, v, err)
	})
	m.HandleFunc("PUT /catalog/agents/{name}/secrets", func(w http.ResponseWriter, r *http.Request) {
		var p AgentSecretsPut
		if !decode(w, r, &p) {
			return
		}
		company, name := c.companyFrom(r), r.PathValue("name")
		kinds, err := c.Store.SecretKinds(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		uses := make([]config.SecretUse, 0, len(p.Secrets))
		for _, u := range p.Secrets {
			u.Name, u.Env = strings.TrimSpace(u.Name), strings.TrimSpace(u.Env)
			if u.Env == "" {
				u.Env = u.Name
			}
			uses = append(uses, u)
		}
		cur, err := c.Store.CurrentCatalog(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		doc, err := agentDoc(company, cur.Docs, name)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if err = checkSecretKinds(doc.Agent.Spec.Secrets, uses, kinds); err != nil {
			var fe *fieldError
			if errors.As(err, &fe) {
				jsonStatus(w, http.StatusBadRequest, fe.FieldError)
				return
			}
			reply(w, nil, err)
			return
		}
		note := "agent " + name + " secrets: " + secretChanges(doc.Agent.Spec.Secrets, uses)
		c.catalogEdit(w, r, p.Base, note, func(_ *presets.Library, company string, docs map[string]string) (map[string]string, error) {
			doc, err := agentDoc(company, docs, name)
			if err != nil {
				return nil, err
			}
			doc.Agent.Spec.Secrets = uses
			return putAgentDoc(company, docs, doc.Agent, "", false)
		})
	})
	m.HandleFunc("POST /secrets/{name}/agents", func(w http.ResponseWriter, r *http.Request) {
		var p SecretGrant
		if !decode(w, r, &p) {
			return
		}
		company, secret := c.companyFrom(r), r.PathValue("name")
		kinds, err := c.Store.SecretKinds(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		kind, ok := kinds[secret]
		if !ok {
			reply(w, nil, fmt.Errorf("%w: no secret %s", storage.ErrNotFound, secret))
			return
		}
		if len(p.Agents) == 0 {
			jsonStatus(w, http.StatusBadRequest, FieldError{Field: "agents", Error: "name at least one agent"})
			return
		}
		use := config.SecretUse{Name: secret, Env: strings.TrimSpace(p.Env), File: kind == storage.SecretFile}
		if use.Env == "" {
			use.Env = secret
		}
		agents := append([]string{}, p.Agents...)
		sort.Strings(agents)
		as := "as " + use.Env
		if use.File {
			as += " (file)"
		}
		c.catalogEdit(w, r, p.Base, "secret "+secret+" given to "+strings.Join(agents, ", ")+" "+as, func(_ *presets.Library, company string, docs map[string]string) (map[string]string, error) {
			out := docs
			for _, n := range agents {
				doc, err := agentDoc(company, out, n)
				if err != nil {
					return nil, err
				}
				uses := []config.SecretUse{}
				for _, u := range doc.Agent.Spec.Secrets {
					if u.Name != secret {
						uses = append(uses, u)
					}
				}
				doc.Agent.Spec.Secrets = append(uses, use)
				if out, err = putAgentDoc(company, out, doc.Agent, "", false); err != nil {
					return nil, err
				}
			}
			return out, nil
		})
	})
}

// agentSecrets lists the agent's secret uses: its own (spec.secrets of its document), those inherited from its
// profile, and its external MCP servers' header secrets, each with whether the company has it with the right kind.
func (c *Core) agentSecrets(ctx context.Context, company, name string) (AgentSecrets, error) {
	cur, err := c.Store.CurrentCatalog(ctx, company)
	if err != nil {
		return AgentSecrets{}, err
	}
	doc, err := agentDoc(company, cur.Docs, name)
	if err != nil {
		return AgentSecrets{}, err
	}
	kinds, err := c.Store.SecretKinds(ctx, company)
	if err != nil {
		return AgentSecrets{}, err
	}
	cat := c.cat(company)
	resolved, ok := cat.Agents[name]
	if !ok {
		resolved = doc.Agent
	}
	profile := doc.Agent.Spec.Extends
	if profile == "" {
		if t, ok := cat.Teams[doc.Agent.Spec.Team]; ok {
			profile = t.Spec.Profile
		}
	}
	own := map[string]bool{}
	for _, u := range doc.Agent.Spec.Secrets {
		own[u.Name] = true
	}
	out := AgentSecrets{Base: cur.Version, Uses: []AgentSecretUse{}}
	add := func(v AgentSecretUse) {
		k, set := kinds[v.Name]
		v.SecretKind = k
		switch {
		case !set:
			v.State = secretMissing
		case k != v.Kind:
			v.State = secretWrongKind
		default:
			v.State = secretSet
		}
		out.Uses = append(out.Uses, v)
	}
	for _, u := range doc.Agent.Spec.Secrets {
		add(AgentSecretUse{Name: u.Name, Env: u.Env, Kind: useKind(u)})
	}
	for _, u := range resolved.Spec.Secrets {
		if !own[u.Name] {
			add(AgentSecretUse{Name: u.Name, Env: u.Env, Kind: useKind(u), Profile: profile})
		}
	}
	for _, n := range resolved.NeededSecrets() {
		if n.Server != "" {
			add(AgentSecretUse{Name: n.Name, Kind: n.Kind, Server: n.Server})
		}
	}
	return out, nil
}

func useKind(u config.SecretUse) string {
	if u.File {
		return config.SecretFile
	}
	return config.SecretString
}

// checkSecretKinds refuses a new or changed use whose secret is set with the other kind; a use the agent already had
// stays (it is marked wrong_kind and its runs wait), so the owner can still change the agent's other uses.
func checkSecretKinds(before, after []config.SecretUse, kinds map[string]string) error {
	had := map[config.SecretUse]bool{}
	for _, u := range before {
		had[u] = true
	}
	for _, u := range after {
		k, ok := kinds[u.Name]
		if had[u] || !ok || k == useKind(u) {
			continue
		}
		if k == storage.SecretFile {
			return fieldErr("secrets", u.Name+" is a file secret: give it as a file (its variable "+u.Env+" then holds the file's path), not as a text variable")
		}
		return fieldErr("secrets", u.Name+" is a text secret: give it as a text variable, not as a file")
	}
	return nil
}

// secretChanges describes how the uses changed, for the catalog change's note: "+NAME as ENV (file), -NAME".
func secretChanges(before, after []config.SecretUse) string {
	old := map[string]config.SecretUse{}
	for _, u := range before {
		old[u.Name] = u
	}
	var parts []string
	seen := map[string]bool{}
	for _, u := range after {
		seen[u.Name] = true
		if p, ok := old[u.Name]; ok && p == u {
			continue
		}
		s := "+" + u.Name + " as " + u.Env
		if u.File {
			s += " (file)"
		}
		parts = append(parts, s)
	}
	for _, u := range before {
		if !seen[u.Name] {
			parts = append(parts, "-"+u.Name)
		}
	}
	if len(parts) == 0 {
		return "unchanged"
	}
	return strings.Join(parts, ", ")
}
