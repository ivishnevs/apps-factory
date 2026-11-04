package core

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"sort"

	"appfactory/internal/config"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
)

// Company secrets (core-secret-store D2): owners and admins set and delete them (by session or owner token), the
// operator token for support; members get names only (sessions.go namesOnly). No route returns a value.

// SecretView is a secret as the routes show it: without its value, with what in the catalog uses it.
type SecretView struct {
	storage.SecretInfo
	UsedBy []string `json:"used_by"`
}

// SecretPut is the body of PUT /secrets/{name}: a string value, or a file's contents in base64.
type SecretPut struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// MaxSecretBody bounds PUT /secrets: 64 KiB in base64 plus the JSON around it.
const maxSecretBody = 96 << 10

func (c *Core) secretRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /secrets", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		list, err := c.Store.Secrets(r.Context(), company)
		if err != nil {
			reply(w, nil, err)
			return
		}
		used := c.secretUsers(company)
		out := make([]SecretView, 0, len(list))
		for _, s := range list {
			out = append(out, SecretView{SecretInfo: s, UsedBy: append([]string{}, used[s.Name]...)})
		}
		reply(w, out, nil)
	})
	// The secrets the catalog needs and whether each is set (setup status, web views of agents and MCP servers).
	m.HandleFunc("GET /secrets/needed", func(w http.ResponseWriter, r *http.Request) {
		company := c.companyFrom(r)
		kinds, err := c.Store.SecretKinds(r.Context(), company)
		reply(w, c.neededSecrets(company, kinds), err)
	})
	m.HandleFunc("PUT /secrets/{name}", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxSecretBody)
		var p SecretPut
		if !decode(w, r, &p) {
			return
		}
		value := []byte(p.Value)
		if p.Kind == storage.SecretFile {
			var err error
			if value, err = base64.StdEncoding.DecodeString(p.Value); err != nil {
				http.Error(w, "a file secret's value is base64", http.StatusBadRequest)
				return
			}
		}
		v, err := c.Store.SetSecret(r.Context(), c.companyFrom(r), r.PathValue("name"), p.Kind, value, author(r))
		if errors.Is(err, secrets.ErrNoMasterKey) {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		reply(w, v, err)
	})
	m.HandleFunc("DELETE /secrets/{name}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]bool{"ok": true}, c.Store.DeleteSecret(r.Context(), c.companyFrom(r), r.PathValue("name"), author(r)))
	})
	// Operator: re-wrap every company key under FACTORY_SECRETS_KEY_NEW (D1).
	m.HandleFunc("POST /secrets/rotate-master", func(w http.ResponseWriter, r *http.Request) {
		if !operator(r) {
			http.Error(w, "the operator rotates the master key", http.StatusForbidden)
			return
		}
		keys, ok := c.Store.Keys.(*secrets.MasterKeys)
		if !ok || !keys.Rotating() {
			http.Error(w, "set "+secrets.NewMasterKeyEnv+" on Core next to "+secrets.MasterKeyEnv+" and restart it first", http.StatusConflict)
			return
		}
		n, err := c.Store.RotateMasterKey(r.Context(), keys)
		reply(w, map[string]any{"company_keys": n, "next": "make " + secrets.NewMasterKeyEnv + " the new " + secrets.MasterKeyEnv + ", restart Core and update the backup"}, err)
	})
}

// secretUsers names what in the company's catalog uses each secret: "agent NAME" (spec.secrets) and "mcp SERVER".
func (c *Core) secretUsers(company string) map[string][]string {
	out := map[string][]string{}
	add := func(name, user string) {
		for _, u := range out[name] {
			if u == user {
				return
			}
		}
		out[name] = append(out[name], user)
	}
	cat := c.cat(company)
	for _, name := range sortedAgents(cat.Agents) {
		for _, n := range cat.Agents[name].NeededSecrets() {
			if n.Server != "" {
				add(n.Name, "mcp "+n.Server)
			}
			add(n.Name, "agent "+name)
		}
	}
	for _, users := range out {
		sort.Strings(users)
	}
	return out
}

// NeededSecret is a secret some agent needs and whether it is set with the right kind.
type NeededSecret struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Set    bool     `json:"set"`
	Wrong  bool     `json:"wrong_kind,omitempty"`
	Agents []string `json:"agents"`
}

func (c *Core) neededSecrets(company string, kinds map[string]string) []NeededSecret {
	by := map[string]*NeededSecret{}
	cat := c.cat(company)
	for _, name := range sortedAgents(cat.Agents) {
		for _, n := range cat.Agents[name].NeededSecrets() {
			v := by[n.Name]
			if v == nil {
				v = &NeededSecret{Name: n.Name, Kind: n.Kind}
				by[n.Name] = v
			}
			if len(v.Agents) == 0 || v.Agents[len(v.Agents)-1] != name {
				v.Agents = append(v.Agents, name)
			}
		}
	}
	out := []NeededSecret{}
	for _, v := range by {
		k, ok := kinds[v.Name]
		v.Set, v.Wrong = ok && k == v.Kind, ok && k != v.Kind
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedAgents(agents map[string]config.Agent) []string {
	out := make([]string, 0, len(agents))
	for n := range agents {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// missingSecret is the first secret agent's runs need that the company has not set with the right kind, as
// "NAME (agent)", or "" (core-secret-store D9: the task waits before dispatch, no run, no retry spent).
func (c *Core) missingSecret(company, agent string, a config.Agent) string {
	need := a.NeededSecrets()
	if len(need) == 0 {
		return ""
	}
	kinds, err := c.Store.SecretKinds(context.Background(), company)
	if err != nil {
		return ""
	}
	for _, n := range need {
		if kinds[n.Name] != n.Kind {
			return n.Name + " (" + agent + ")"
		}
	}
	return ""
}
