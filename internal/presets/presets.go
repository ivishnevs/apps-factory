// Package presets is the release's preset library (team-builder D1, D2): agent presets (an Agent document's spec,
// Factory skills, secrets it needs and a prompt) and team presets (a team of agent presets with its profile and
// policies). Presets are copied into a company's catalog as ordinary documents, never linked.
package presets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"appfactory/internal/config"
	"appfactory/internal/configfile"
	"appfactory/internal/skills"
)

// Dir is the preset library under the factory root.
const Dir = "presets"

// SecretNeed is a secret an agent preset needs and what for.
type SecretNeed struct {
	Name string `yaml:"name" json:"name"`
	Env  string `yaml:"env" json:"env"`
	File bool   `yaml:"file" json:"file,omitempty"`
	Why  string `yaml:"why" json:"why,omitempty"`
}

// Meta describes a preset in the gallery.
type Meta struct {
	Name        string   `yaml:"name" json:"name"`
	Title       string   `yaml:"title" json:"title"`
	Description string   `yaml:"description" json:"description"`
	Tags        []string `yaml:"tags" json:"tags,omitempty"`
	// Category is the library's field (default-agent-library): software, games, growth, content or business.
	Category string `yaml:"category" json:"category"`
	// Examples are tasks the preset is made for (the library shows them; eval seeds for an agent preset).
	Examples []string `yaml:"examples" json:"examples,omitempty"`
}

// Categories are the library's fields, in display order.
var Categories = []string{"software", "games", "growth", "content", "business"}

// Agent is an agent preset.
type Agent struct {
	APIVersion string `yaml:"apiVersion" json:"-"`
	Kind       string `yaml:"kind" json:"-"`
	Metadata   Meta   `yaml:"metadata" json:"metadata"`
	Spec       struct {
		Role    string         `yaml:"role" json:"role"`
		Agent   map[string]any `yaml:"agent" json:"agent,omitempty"`
		Skills  []string       `yaml:"skills" json:"skills"`
		Secrets []SecretNeed   `yaml:"secrets" json:"secrets"`
	} `yaml:"spec" json:"spec"`
	Prompt  string `yaml:"-" json:"prompt"`
	Version string `yaml:"-" json:"version"`
}

// TeamAgent is an agent of a team preset: its key in the team, its agent preset, its manager (a key or owner) and
// whom it may message (keys or owner).
type TeamAgent struct {
	Key         string   `yaml:"key" json:"key"`
	Preset      string   `yaml:"preset" json:"preset"`
	ReportsTo   string   `yaml:"reports_to" json:"reports_to"`
	CanMessage  []string `yaml:"can_message" json:"can_message"`
	Description string   `yaml:"description" json:"description,omitempty"`
}

// Team is a team preset.
type Team struct {
	APIVersion string `yaml:"apiVersion" json:"-"`
	Kind       string `yaml:"kind" json:"-"`
	Metadata   Meta   `yaml:"metadata" json:"metadata"`
	Spec       struct {
		Team     string                    `yaml:"team" json:"team"`
		Lead     string                    `yaml:"lead" json:"lead"`
		Agents   []TeamAgent               `yaml:"agents" json:"agents"`
		Profile  map[string]any            `yaml:"profile" json:"profile"`
		Policies map[string]map[string]any `yaml:"policies" json:"policies"`
	} `yaml:"spec" json:"spec"`
	Version string `yaml:"-" json:"version"`
	// Skills and Secrets are the union of its agents' (the gallery shows them).
	Skills  []string     `yaml:"-" json:"skills"`
	Secrets []SecretNeed `yaml:"-" json:"secrets"`
}

// Library is the release's presets.
type Library struct {
	Root   string
	Agents map[string]*Agent
	Teams  map[string]*Team
}

var name = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func digest(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d\x00", len(p))
		h.Write(p)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// Load reads and checks root/presets: every agent preset has a role and a prompt and names known Factory skills;
// every team preset's agents name known agent presets, unique keys, managers inside the team (or owner), and a lead
// reporting to the owner.
func Load(root string) (*Library, error) {
	l := &Library{Root: root, Agents: map[string]*Agent{}, Teams: map[string]*Team{}}
	dir := filepath.Join(root, Dir)
	entries, err := os.ReadDir(filepath.Join(dir, "agents"))
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		a := &Agent{}
		raw, err := os.ReadFile(filepath.Join(dir, "agents", e.Name(), "preset.yaml"))
		if err != nil {
			return nil, err
		}
		if err = configfile.Decode(raw, a); err != nil {
			return nil, fmt.Errorf("agent preset %s: %w", e.Name(), err)
		}
		prompt, err := os.ReadFile(filepath.Join(dir, "agents", e.Name(), "prompt.md"))
		if err != nil {
			return nil, fmt.Errorf("agent preset %s: %w", e.Name(), err)
		}
		a.Prompt, a.Version = string(prompt), digest(raw, prompt)
		if a.Spec.Skills == nil {
			a.Spec.Skills = []string{}
		}
		if a.Spec.Secrets == nil {
			a.Spec.Secrets = []SecretNeed{}
		}
		switch {
		case a.Kind != "AgentPreset" || a.Metadata.Name != e.Name() || !name.MatchString(e.Name()):
			return nil, fmt.Errorf("agent preset %s: kind AgentPreset named as its directory", e.Name())
		case a.Spec.Role == "" || strings.TrimSpace(a.Prompt) == "":
			return nil, fmt.Errorf("agent preset %s: role and prompt required", e.Name())
		case !slices.Contains(Categories, a.Metadata.Category) || len(a.Metadata.Examples) == 0:
			return nil, fmt.Errorf("agent preset %s: category (one of %s) and example tasks required", e.Name(), strings.Join(Categories, ", "))
		}
		for _, s := range a.Spec.Skills {
			if _, err := os.Stat(filepath.Join(root, "skills", s, "SKILL.md")); err != nil {
				return nil, fmt.Errorf("agent preset %s: skill %s is not a Factory skill", e.Name(), s)
			}
		}
		l.Agents[e.Name()] = a
	}
	files, err := filepath.Glob(filepath.Join(dir, "teams", "*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		t := &Team{}
		raw, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		if err = configfile.Decode(raw, t); err != nil {
			return nil, fmt.Errorf("team preset %s: %w", filepath.Base(f), err)
		}
		n := strings.TrimSuffix(filepath.Base(f), ".yaml")
		if t.Kind != "TeamPreset" || t.Metadata.Name != n || !name.MatchString(t.Spec.Team) {
			return nil, fmt.Errorf("team preset %s: kind TeamPreset named as its file, with a team name", n)
		}
		parts := [][]byte{raw}
		keys := map[string]bool{}
		for _, ta := range t.Spec.Agents {
			a := l.Agents[ta.Preset]
			if a == nil || !name.MatchString(ta.Key) || keys[ta.Key] {
				return nil, fmt.Errorf("team preset %s: agent %q needs a unique key and a known agent preset", n, ta.Key)
			}
			keys[ta.Key] = true
			parts = append(parts, []byte(a.Version))
			for _, s := range a.Spec.Skills {
				if !contains(t.Skills, s) {
					t.Skills = append(t.Skills, s)
				}
			}
			t.Secrets = append(t.Secrets, a.Spec.Secrets...)
		}
		for _, ta := range t.Spec.Agents {
			if ta.ReportsTo != "owner" && !keys[ta.ReportsTo] {
				return nil, fmt.Errorf("team preset %s: %s reports to %q, not an agent of the team", n, ta.Key, ta.ReportsTo)
			}
			if ta.Key == t.Spec.Lead && ta.ReportsTo != "owner" {
				return nil, fmt.Errorf("team preset %s: the lead reports to the owner", n)
			}
		}
		if !slices.Contains(Categories, t.Metadata.Category) {
			return nil, fmt.Errorf("team preset %s: category one of %s required", n, strings.Join(Categories, ", "))
		}
		if !keys[t.Spec.Lead] {
			return nil, fmt.Errorf("team preset %s: lead %q is not an agent of the team", n, t.Spec.Lead)
		}
		sort.Strings(t.Skills)
		if t.Skills == nil {
			t.Skills = []string{}
		}
		if t.Secrets == nil {
			t.Secrets = []SecretNeed{} // the gallery's JSON lists, never null
		}
		t.Version = digest(parts...)
		l.Teams[n] = t
	}
	return l, nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

// TeamNames are the team presets, sorted.
func (l *Library) TeamNames() []string {
	out := make([]string, 0, len(l.Teams))
	for n := range l.Teams {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Base are the documents every new company starts with (quota keys, NodeHooks, an empty skills registry).
func (l *Library) Base(company string) (map[string]string, error) {
	dir, err := config.CompanyDir(company)
	if err != nil {
		return nil, err
	}
	dir = filepath.ToSlash(dir)
	src := filepath.Join(l.Root, Dir, "company")
	docs := map[string]string{}
	err = filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		docs[path.Join(dir, filepath.ToSlash(rel))] = string(b)
		return err
	})
	return docs, err
}

// ErrInput is a copy refused for its input (a name taken, an unknown preset).
var ErrInput = errors.New("invalid input")

// TeamCopy names the agents of a team copied from a preset: names maps keys to agent names (default <team>-<key>).
type TeamCopy struct {
	Preset string            `json:"from_preset"`
	Team   string            `json:"team"`
	Names  map[string]string `json:"names,omitempty"`
}

// CopyTeam adds a team preset to company's documents (D2): the team document, each agent with spec.team and
// metadata.preset, their prompts as company documents, the team-prefixed profile and policies, and the Factory skills
// installed. docs is the current catalog; the result is the next version's documents.
func (l *Library) CopyTeam(company string, in TeamCopy, docs map[string]string) (map[string]string, error) {
	t := l.Teams[in.Preset]
	if t == nil {
		return nil, fmt.Errorf("%w: no team preset %q", ErrInput, in.Preset)
	}
	team := in.Team
	if team == "" {
		team = t.Spec.Team
	}
	if !name.MatchString(team) {
		return nil, fmt.Errorf("%w: team name: lowercase letters, digits and '-'", ErrInput)
	}
	dir, err := config.CompanyDir(company)
	if err != nil {
		return nil, err
	}
	dir = filepath.ToSlash(dir)
	out := clone(docs)
	if _, ok := out[path.Join(dir, "teams", team+".yaml")]; ok {
		return nil, fmt.Errorf("%w: team %s exists", ErrInput, team)
	}
	names := map[string]string{}
	for _, ta := range t.Spec.Agents {
		n := in.Names[ta.Key]
		if n == "" {
			n = team + "-" + ta.Key
		}
		if err = freeAgent(out, dir, n); err != nil {
			return nil, err
		}
		names[ta.Key] = n
	}
	mapName := func(k string) string {
		if k == "owner" {
			return k
		}
		return names[k]
	}
	profile := team + "-agent"
	var policyNames []string
	for pn := range t.Spec.Policies {
		policyNames = append(policyNames, team+"-"+pn)
	}
	sort.Strings(policyNames)
	prof := deepCopy(t.Spec.Profile)
	prof["policies"] = policyNames
	if err = put(out, path.Join(dir, "profiles", profile+".yaml"), "AgentProfile", map[string]any{"name": profile, "description": "Defaults of the agents of team " + team}, prof); err != nil {
		return nil, err
	}
	for pn, spec := range t.Spec.Policies {
		if err = put(out, path.Join(dir, "policies", team+"-"+pn+".yaml"), "Policy", map[string]any{"name": team + "-" + pn}, substitute(deepCopy(spec), mapName).(map[string]any)); err != nil {
			return nil, err
		}
	}
	if err = put(out, path.Join(dir, "teams", team+".yaml"), "Team", map[string]any{"name": team, "description": t.Metadata.Description, "preset": t.Metadata.Name + "@" + t.Version},
		map[string]any{"lead": names[t.Spec.Lead], "profile": profile}); err != nil {
		return nil, err
	}
	for _, ta := range t.Spec.Agents {
		var can []string
		for _, k := range ta.CanMessage {
			can = append(can, mapName(k))
		}
		if err = l.putAgent(out, dir, team, profile, ta.Preset, names[ta.Key], AgentOverrides{ReportsTo: mapName(ta.ReportsTo), CanMessage: can, Description: ta.Description}); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// AgentOverrides change an agent copied from a preset.
type AgentOverrides struct {
	Description string   `json:"description,omitempty"`
	ReportsTo   string   `json:"reports_to,omitempty"`
	CanMessage  []string `json:"can_message,omitempty"`
	Provider    string   `json:"provider,omitempty"`
	Model       string   `json:"model,omitempty"`
	QuotaKey    string   `json:"quota_key,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	// ContextWindow, MaxConcurrentRuns and ConversationMaxRuns (work-and-conversation-runs): runtime.context_window
	// per run kind, execution.max_concurrent_runs (work runs) and execution.conversation.max_concurrent_runs; 0 keeps
	// the preset's.
	ContextWindow       config.ContextWindow `json:"context_window,omitzero"`
	MaxConcurrentRuns   int                  `json:"max_concurrent_runs,omitempty"`
	ConversationMaxRuns int                  `json:"conversation_max_runs,omitempty"`
}

// CopyAgent adds one agent from a preset to team (D3): name defaults to <team>-<preset>, reports_to to the team's
// lead.
func (l *Library) CopyAgent(company, team, preset, agentName string, ov AgentOverrides, docs map[string]string) (map[string]string, string, error) {
	if l.Agents[preset] == nil {
		return nil, "", fmt.Errorf("%w: no agent preset %q", ErrInput, preset)
	}
	dir, err := config.CompanyDir(company)
	if err != nil {
		return nil, "", err
	}
	dir = filepath.ToSlash(dir)
	teamDoc, ok := docs[path.Join(dir, "teams", team+".yaml")]
	if !ok {
		return nil, "", fmt.Errorf("%w: no team %s", ErrInput, team)
	}
	var td config.Team
	if err = yaml.Unmarshal([]byte(teamDoc), &td); err != nil {
		return nil, "", err
	}
	if agentName == "" {
		agentName = team + "-" + preset
	}
	if !name.MatchString(agentName) {
		return nil, "", fmt.Errorf("%w: agent name: lowercase letters, digits and '-'", ErrInput)
	}
	out := clone(docs)
	if err = freeAgent(out, dir, agentName); err != nil {
		return nil, "", err
	}
	if ov.ReportsTo == "" {
		ov.ReportsTo = td.Spec.Lead
	}
	if len(ov.CanMessage) == 0 {
		ov.CanMessage = []string{ov.ReportsTo}
	}
	return out, agentName, l.putAgent(out, dir, team, td.Spec.Profile, preset, agentName, ov)
}

// putAgent writes the agent document, its prompt and its Factory skills.
func (l *Library) putAgent(docs map[string]string, dir, team, profile, preset, agentName string, ov AgentOverrides) error {
	a := l.Agents[preset]
	spec := deepCopy(a.Spec.Agent)
	spec["team"], spec["role"], spec["reports_to"] = team, a.Spec.Role, ov.ReportsTo
	if profile != "" {
		spec["extends"] = profile
	}
	promptPath := "prompts/" + agentName + ".md"
	spec["prompt"] = map[string]any{"file": promptPath}
	comm, _ := spec["communication"].(map[string]any)
	if comm == nil {
		comm = map[string]any{}
	}
	comm["can_message"] = ov.CanMessage
	spec["communication"] = comm
	if len(a.Spec.Skills) > 0 {
		spec["skills"] = map[string]any{"include": a.Spec.Skills}
	}
	if len(a.Spec.Secrets) > 0 {
		var ss []map[string]any
		for _, s := range a.Spec.Secrets {
			ss = append(ss, map[string]any{"name": s.Name, "env": s.Env, "file": s.File})
		}
		spec["secrets"] = ss
	}
	if ov.Provider != "" || ov.Model != "" || ov.QuotaKey != "" {
		rt := map[string]any{}
		// The preset's other runtime fields (its context window) stay (work-and-conversation-runs).
		if prev, _ := spec["runtime"].(map[string]any); prev["context_window"] != nil {
			rt["context_window"] = prev["context_window"]
		}
		if ov.Provider != "" {
			rt["provider"] = ov.Provider
		}
		rt["model"] = cmpOr(ov.Model, "default")
		if ov.QuotaKey != "" {
			rt["quota_key"] = ov.QuotaKey
		}
		spec["runtime"] = rt
	}
	if w := ov.ContextWindow; w != (config.ContextWindow{}) {
		rt, _ := spec["runtime"].(map[string]any)
		if rt == nil {
			rt = map[string]any{}
		}
		cw := map[string]any{}
		if w.Work != "" {
			cw["work"] = w.Work
		}
		if w.Conversation != "" {
			cw["conversation"] = w.Conversation
		}
		rt["context_window"] = cw
		spec["runtime"] = rt
	}
	if ov.MaxConcurrentRuns > 0 || ov.ConversationMaxRuns > 0 {
		ex, _ := spec["execution"].(map[string]any)
		if ex == nil {
			ex = map[string]any{}
		}
		if ov.MaxConcurrentRuns > 0 {
			ex["max_concurrent_runs"] = ov.MaxConcurrentRuns
		}
		if ov.ConversationMaxRuns > 0 {
			conv, _ := ex["conversation"].(map[string]any)
			if conv == nil {
				conv = map[string]any{}
			}
			conv["max_concurrent_runs"] = ov.ConversationMaxRuns
			ex["conversation"] = conv
		}
		spec["execution"] = ex
	}
	desc := cmpOr(ov.Description, a.Metadata.Description)
	if err := put(docs, path.Join(dir, "agents", agentName+".yaml"), "Agent", map[string]any{"name": agentName, "description": desc, "preset": preset + "@" + a.Version}, spec); err != nil {
		return err
	}
	docs[promptPath] = cmpOr(ov.Prompt, a.Prompt)
	return installFactorySkills(docs, dir, a.Spec.Skills)
}

// installFactorySkills adds the Factory skills a preset names to the company's skills.yaml.
func installFactorySkills(docs map[string]string, dir string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	p := path.Join(dir, skills.File)
	var reg struct {
		Skills map[string]skills.Skill `yaml:"skills"`
	}
	if text, ok := docs[p]; ok {
		if err := yaml.Unmarshal([]byte(text), &reg); err != nil {
			return err
		}
	}
	if reg.Skills == nil {
		reg.Skills = map[string]skills.Skill{}
	}
	changed := false
	for _, n := range names {
		if _, ok := reg.Skills[n]; !ok {
			reg.Skills[n] = skills.Skill{Source: skills.BuiltinSource}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	b, err := yaml.Marshal(reg)
	if err != nil {
		return err
	}
	docs[p] = "# Installed skills (skill-registry D5).\n" + string(b)
	return nil
}

func freeAgent(docs map[string]string, dir, n string) error {
	if !name.MatchString(n) {
		return fmt.Errorf("%w: agent name %q: lowercase letters, digits and '-'", ErrInput, n)
	}
	if _, ok := docs[path.Join(dir, "agents", n+".yaml")]; ok {
		return fmt.Errorf("%w: agent %s exists (names are unique per company)", ErrInput, n)
	}
	return nil
}

func put(docs map[string]string, p, kind string, meta map[string]any, spec map[string]any) error {
	b, err := yaml.Marshal(map[string]any{"apiVersion": "factory/v1", "kind": kind, "metadata": meta, "spec": spec})
	if err != nil {
		return err
	}
	docs[p] = string(b)
	return nil
}

// substitute replaces "@key" strings with the named agent.
func substitute(v any, mapName func(string) string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = substitute(e, mapName)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = substitute(e, mapName)
		}
		return x
	case string:
		if key, ok := strings.CutPrefix(x, "@"); ok {
			return mapName(key)
		}
	}
	return v
}

func deepCopy(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	b, _ := yaml.Marshal(m)
	out := map[string]any{}
	_ = yaml.Unmarshal(b, &out)
	return out
}

func clone(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
