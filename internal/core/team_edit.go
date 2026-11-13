package core

import (
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"appfactory/internal/config"
	"appfactory/internal/presets"
	"appfactory/internal/storage"
)

// Team page (team-page-clarity): the team's settings in readable form, and changing its lead, its onboarding agent
// and its settings from the page. Each change is a catalog version the loader validates like any other edit; the
// route is under /catalog, so only owners and admins reach it.

// TeamSettings is the team profile as the page shows it: the defaults every agent of the team starts from.
type TeamSettings struct {
	// Profile is the profile document behind the settings ("" when the team has none yet).
	Profile  string `json:"profile,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	QuotaKey string `json:"quota_key,omitempty"`
	// MaxConcurrentRuns is the profile's work-run limit; 0 leaves it to the role defaults in RoleLimits.
	MaxConcurrentRuns int            `json:"max_concurrent_runs,omitempty"`
	RoleLimits        map[string]int `json:"role_limits"`
	Timeout           string         `json:"timeout,omitempty"`
	// QuotaKeys are the company's declared subscriptions to pick from.
	QuotaKeys []QuotaKeyView `json:"quota_keys"`
	// Overrides are the team's agents that set a different subscription, model, limit or run time themselves.
	Overrides []string `json:"overrides"`
}

// QuotaKeyView is one declared subscription.
type QuotaKeyView struct {
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	Description string `json:"description,omitempty"`
}

// teamSettings resolves team's profile for the page.
func teamSettings(cat *config.Catalog, team string) TeamSettings {
	t := cat.Teams[team]
	s := TeamSettings{Profile: t.Spec.Profile, RoleLimits: map[string]int{}, QuotaKeys: []QuotaKeyView{}, Overrides: []string{}}
	limits := cat.Concurrency
	if limits == nil {
		limits = config.DefaultConcurrency
	}
	for k, v := range limits {
		s.RoleLimits[k] = v
	}
	for _, n := range sortedKeys(cat.QuotaKeys) {
		k := cat.QuotaKeys[n]
		s.QuotaKeys = append(s.QuotaKeys, QuotaKeyView{Name: n, Provider: k.Provider, Description: k.Description})
	}
	p, ok := cat.Profiles[t.Spec.Profile]
	if ok {
		s.Provider, s.Model, s.QuotaKey = p.Spec.Runtime.Provider, p.Spec.Runtime.Model, p.Spec.Runtime.QuotaKey
		s.MaxConcurrentRuns, s.Timeout = p.Spec.Execution.MaxConcurrentRuns, p.Spec.Execution.Timeout
	}
	model := func(m string) string {
		if m == "" {
			return "default"
		}
		return m
	}
	for _, n := range cat.TeamAgents(team) {
		a := cat.Agents[n]
		r, e := a.Spec.Runtime, a.Spec.Execution
		quota := r.QuotaKey
		if quota == "" {
			quota = r.DefaultQuotaKey
		}
		profileQuota := s.QuotaKey
		if profileQuota == "" {
			profileQuota = cat.DefaultQuotaKey(s.Provider)
		}
		limitDiffers := (s.MaxConcurrentRuns == 0 && !a.LimitFromRole) || (s.MaxConcurrentRuns > 0 && e.MaxConcurrentRuns != s.MaxConcurrentRuns)
		if !ok || r.Provider != s.Provider || model(r.Model) != model(s.Model) || quota != profileQuota || limitDiffers || e.Timeout != s.Timeout {
			s.Overrides = append(s.Overrides, n)
		}
	}
	return s
}

// TeamEdit changes a team on top of catalog version Base: its lead, its onboarding agent ("" for the lead) and its
// settings (the team profile). Omitted parts stay as they are.
type TeamEdit struct {
	Base          int64   `json:"base"`
	Lead          string  `json:"lead,omitempty"`
	AuditAssignee *string `json:"audit_assignee,omitempty"`
	// Reviewer sets the team's default reviewer (approval-gates-merge): an agent of the team or role:<role>; "" removes
	// it (managers review their reports' work).
	Reviewer *string           `json:"reviewer,omitempty"`
	Settings *TeamSettingsEdit `json:"settings,omitempty"`
}

// TeamSettingsEdit are the profile fields the page edits; empty removes the field (it inherits again).
type TeamSettingsEdit struct {
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	QuotaKey          string `json:"quota_key"`
	MaxConcurrentRuns int    `json:"max_concurrent_runs"`
	Timeout           string `json:"timeout"`
}

func (c *Core) teamEditRoutes(m *http.ServeMux) {
	m.HandleFunc("PUT /catalog/teams/{name}", func(w http.ResponseWriter, r *http.Request) {
		var p TeamEdit
		if !decode(w, r, &p) {
			return
		}
		team := r.PathValue("name")
		c.catalogEdit(w, r, p.Base, teamEditNote(team, p), func(_ *presets.Library, company string, docs map[string]string) (map[string]string, error) {
			return editTeam(c.cat(company), company, docs, team, p)
		})
	})
}

func teamEditNote(team string, p TeamEdit) string {
	var parts []string
	if p.Lead != "" {
		parts = append(parts, "lead "+p.Lead)
	}
	if p.AuditAssignee != nil {
		parts = append(parts, "onboarding "+firstNonEmpty(*p.AuditAssignee, "the lead"))
	}
	if p.Reviewer != nil {
		parts = append(parts, "default reviewer "+firstNonEmpty(*p.Reviewer, "the managers"))
	}
	if p.Settings != nil {
		parts = append(parts, "settings")
	}
	return "team " + team + ": " + strings.Join(parts, ", ")
}

// yamlDoc is a catalog document read for editing: its fields as a map (other fields are kept when written back).
type yamlDoc struct {
	path string
	doc  map[string]any
}

func readDoc(docs map[string]string, p string) (*yamlDoc, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(docs[p]), &doc); err != nil || doc == nil {
		return nil, fmt.Errorf("%w: %s is not a YAML document", storage.ErrInput, p)
	}
	return &yamlDoc{path: p, doc: doc}, nil
}

// spec is the document's spec map, created when missing; sub walks nested maps the same way.
func (d *yamlDoc) spec() map[string]any { return sub(d.doc, "spec") }

func sub(m map[string]any, key string) map[string]any {
	s, ok := m[key].(map[string]any)
	if !ok {
		s = map[string]any{}
		m[key] = s
	}
	return s
}

// setOrDelete sets key to v, or deletes it when v is the zero value (the field inherits again).
func setOrDelete(m map[string]any, key string, v any) {
	switch x := v.(type) {
	case string:
		if x == "" {
			delete(m, key)
			return
		}
	case int:
		if x == 0 {
			delete(m, key)
			return
		}
	}
	m[key] = v
}

func (d *yamlDoc) write(out map[string]string) error {
	b, err := yaml.Marshal(d.doc)
	if err != nil {
		return err
	}
	out[d.path] = string(b)
	return nil
}

// findDoc is the path of the document of kind in dir/sub with metadata.name name ("" when there is none).
func findDoc(docs map[string]string, dir, subdir, kind, name string) string {
	prefix := path.Join(dir, subdir) + "/"
	for _, p := range sortedKeys(docs) {
		if !strings.HasPrefix(p, prefix) || strings.Contains(strings.TrimPrefix(p, prefix), "/") || !strings.HasSuffix(p, ".yaml") {
			continue
		}
		var head struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
		}
		if yaml.Unmarshal([]byte(docs[p]), &head) == nil && head.Kind == kind && head.Metadata.Name == name {
			return p
		}
	}
	return ""
}

// editTeam applies p to team's documents.
func editTeam(cat *config.Catalog, company string, docs map[string]string, team string, p TeamEdit) (map[string]string, error) {
	t, ok := cat.Teams[team]
	if !ok {
		return nil, fmt.Errorf("%w: no team %s", storage.ErrNotFound, team)
	}
	d, err := config.CompanyDir(company)
	if err != nil {
		return nil, err
	}
	dir := filepath.ToSlash(d)
	teamPath := findDoc(docs, dir, "teams", "Team", team)
	if teamPath == "" {
		return nil, fmt.Errorf("%w: no document of team %s", storage.ErrNotFound, team)
	}
	out := cloneDocs(docs)
	td, err := readDoc(out, teamPath)
	if err != nil {
		return nil, err
	}
	inTeam := func(field, agent string) error {
		if a, ok := cat.Agents[agent]; !ok || a.Spec.Team != team {
			return fieldErr(field, agent+" is not an agent of team "+team)
		}
		return nil
	}
	agentDocs := map[string]*yamlDoc{}
	agentDocOf := func(n string) (*yamlDoc, error) {
		if a, ok := agentDocs[n]; ok {
			return a, nil
		}
		a, err := readDoc(out, path.Join(dir, "agents", n+".yaml"))
		if err == nil {
			agentDocs[n] = a
		}
		return a, err
	}
	lead := t.Spec.Lead
	if p.Lead != "" && p.Lead != lead {
		if err = inTeam("lead", p.Lead); err != nil {
			return nil, err
		}
		// The new lead takes the old lead's place: it reports to the owner, the old lead's direct reports report
		// to it, and the old lead reports to it (only a team's lead reports to the owner).
		old := lead
		lead = p.Lead
		td.spec()["lead"] = lead
		for _, n := range cat.TeamAgents(team) {
			var to string
			switch {
			case n == lead:
				to = config.Owner
			case n == old || config.Manager(cat.Agents, n) == old:
				to = lead
			default:
				continue
			}
			a, err := agentDocOf(n)
			if err != nil {
				return nil, err
			}
			a.spec()["reports_to"] = to
		}
	}
	if p.AuditAssignee != nil {
		v := *p.AuditAssignee
		if v != "" {
			if err = inTeam("audit_assignee", v); err != nil {
				return nil, err
			}
		}
		if v == lead {
			v = "" // the default: the lead, whoever leads later
		}
		setOrDelete(td.spec(), "audit_assignee", v)
	}
	if p.Reviewer != nil {
		v := strings.TrimSpace(*p.Reviewer)
		if role, isRole := strings.CutPrefix(v, "role:"); isRole {
			found := false
			for _, n := range cat.TeamAgents(team) {
				found = found || cat.Agents[n].Spec.Role == role
			}
			if !found {
				return nil, fieldErr("reviewer", "no agent of team "+team+" has role "+role)
			}
		} else if v != "" {
			if err = inTeam("reviewer", v); err != nil {
				return nil, err
			}
		}
		review := sub(td.spec(), "review")
		setOrDelete(review, "reviewer", v)
		if len(review) == 0 {
			delete(td.spec(), "review")
		}
	}
	if s := p.Settings; s != nil {
		if s.Provider != "" && !slices.Contains([]string{"claude", "codex"}, s.Provider) {
			return nil, fieldErr("provider", "claude or codex")
		}
		if s.MaxConcurrentRuns < 0 || s.MaxConcurrentRuns > config.MaxConcurrentRuns {
			return nil, fieldErr("max_concurrent_runs", fmt.Sprintf("0..%d (0: by role)", config.MaxConcurrentRuns))
		}
		if s.Timeout != "" {
			if v, err := time.ParseDuration(s.Timeout); err != nil || v < time.Second || v > 24*time.Hour {
				return nil, fieldErr("timeout", "a duration such as 30m or 2h (at most 24h)")
			}
		}
		name := t.Spec.Profile
		var pd *yamlDoc
		if name == "" {
			// A team without a profile gets one, named as team presets name theirs.
			name = team + "-agent"
			pp := path.Join(dir, "profiles", name+".yaml")
			if _, taken := out[pp]; taken || findDoc(out, dir, "profiles", "AgentProfile", name) != "" {
				return nil, fieldErr("settings", "profile "+name+" exists: make it the team's profile in the YAML editor")
			}
			pd = &yamlDoc{path: pp, doc: map[string]any{"apiVersion": "factory/v1", "kind": "AgentProfile",
				"metadata": map[string]any{"name": name, "description": "Defaults of the agents of team " + team}, "spec": map[string]any{}}}
			td.spec()["profile"] = name
		} else {
			pp := findDoc(out, dir, "profiles", "AgentProfile", name)
			if pp == "" {
				return nil, fmt.Errorf("%w: no document of profile %s", storage.ErrNotFound, name)
			}
			if pd, err = readDoc(out, pp); err != nil {
				return nil, err
			}
		}
		runtime, execution := sub(pd.spec(), "runtime"), sub(pd.spec(), "execution")
		setOrDelete(runtime, "provider", s.Provider)
		setOrDelete(runtime, "model", s.Model)
		setOrDelete(runtime, "quota_key", s.QuotaKey)
		setOrDelete(execution, "max_concurrent_runs", s.MaxConcurrentRuns)
		setOrDelete(execution, "timeout", s.Timeout)
		for k, m := range map[string]map[string]any{"runtime": runtime, "execution": execution} {
			if len(m) == 0 {
				delete(pd.spec(), k)
			}
		}
		if err = pd.write(out); err != nil {
			return nil, err
		}
	}
	if err = td.write(out); err != nil {
		return nil, err
	}
	for _, a := range agentDocs {
		if err = a.write(out); err != nil {
			return nil, err
		}
	}
	return out, nil
}
