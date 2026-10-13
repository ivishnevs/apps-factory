package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"appfactory/internal/configfile"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
	"gopkg.in/yaml.v3"
)

type Metadata struct {
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
}
type Spec struct {
	Extends string `yaml:"extends" json:"extends,omitempty"`
	Role    string `yaml:"role" json:"role,omitempty"`
	Runtime struct {
		Provider string `yaml:"provider" json:"provider"`
		Model    string `yaml:"model" json:"model"`
	} `yaml:"runtime" json:"runtime"`
	Prompt struct {
		File string `yaml:"file" json:"file"`
	} `yaml:"prompt" json:"prompt"`
	Capabilities []string `yaml:"capabilities" json:"capabilities"`
	Skills       struct {
		Include  []string `yaml:"include" json:"include"`
		Discover struct {
			Enabled    bool     `yaml:"enabled" json:"enabled"`
			Categories []string `yaml:"categories" json:"categories"`
		} `yaml:"discover" json:"discover"`
	} `yaml:"skills" json:"skills"`
	Tools struct {
		Mode  string   `yaml:"mode" json:"mode"`
		Allow []string `yaml:"allow" json:"allow"`
		Deny  []string `yaml:"deny" json:"deny"`
	} `yaml:"tools" json:"tools"`
	Approvals struct {
		Mode    string   `yaml:"mode" json:"mode"`
		Require []string `yaml:"require" json:"require"`
	} `yaml:"approvals" json:"approvals"`
	Communication struct {
		CanMessage     []string `yaml:"can_message" json:"can_message"`
		CanCreateTasks bool     `yaml:"can_create_tasks" json:"can_create_tasks"`
		CanAssignTasks bool     `yaml:"can_assign_tasks" json:"can_assign_tasks"`
	} `yaml:"communication" json:"communication"`
	Context struct {
		Include []string `yaml:"include" json:"include"`
	} `yaml:"context" json:"context"`
	Execution struct {
		Timeout           string `yaml:"timeout" json:"timeout"`
		MaxConcurrentRuns int    `yaml:"max_concurrent_runs" json:"max_concurrent_runs"`
		MaxRetries        int    `yaml:"max_retries" json:"max_retries"`
	} `yaml:"execution" json:"execution"`
	Policies []string `yaml:"policies" json:"policies"`
}
type Agent struct {
	APIVersion      string          `yaml:"apiVersion" json:"apiVersion"`
	Kind            string          `yaml:"kind" json:"kind"`
	Metadata        Metadata        `yaml:"metadata" json:"metadata"`
	Spec            Spec            `yaml:"spec" json:"spec"`
	Instructions    string          `yaml:"-" json:"instructions"`
	Timeout         time.Duration   `yaml:"-" json:"-"`
	Rules           []policies.Rule `yaml:"-" json:"rules"`
	AvailableSkills []string        `yaml:"-" json:"available_skills"`
}
type Catalog struct {
	Agents   map[string]Agent
	Profiles map[string]Agent
	Policies map[string]policies.Policy
	Skills   *skills.Registry
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)

func Load(root string) (map[string]Agent, error) {
	c, err := LoadCatalog(root)
	if err != nil {
		return nil, err
	}
	return c.Agents, nil
}
func LoadCatalog(root string) (*Catalog, error) {
	c := &Catalog{Agents: map[string]Agent{}, Profiles: map[string]Agent{}}
	var err error
	c.Skills, err = skills.Load(root)
	if err != nil {
		return nil, err
	}
	rawPolicies := map[string]policies.Policy{"system": policies.System()}
	paths, err := filepath.Glob(filepath.Join(root, "configs/policies/*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		var v policies.Policy
		if err = configfile.Read(p, &v); err != nil {
			return nil, err
		}
		if !namePattern.MatchString(v.Metadata.Name) {
			return nil, fmt.Errorf("invalid policy name")
		}
		if _, ok := rawPolicies[v.Metadata.Name]; ok {
			return nil, fmt.Errorf("duplicate/reserved policy %s", v.Metadata.Name)
		}
		rawPolicies[v.Metadata.Name] = v
	}
	c.Policies, err = policies.Resolve(rawPolicies)
	if err != nil {
		return nil, err
	}
	profiles, err := documents(root, "profiles", "AgentProfile")
	if err != nil {
		return nil, err
	}
	agents, err := documents(root, "agents", "Agent")
	if err != nil {
		return nil, err
	}
	resolved := map[string]map[string]any{}
	visiting := map[string]bool{}
	var profile func(string) (map[string]any, error)
	profile = func(name string) (map[string]any, error) {
		if m, ok := resolved[name]; ok {
			return m, nil
		}
		m, ok := profiles[name]
		if !ok {
			return nil, fmt.Errorf("unknown profile %s", name)
		}
		if visiting[name] {
			return nil, fmt.Errorf("profile cycle at %s", name)
		}
		visiting[name] = true
		spec := m["spec"].(map[string]any)
		if parent, ok := spec["extends"].(string); ok && parent != "" {
			p, err := profile(parent)
			if err != nil {
				return nil, err
			}
			m = mergeDocument(p, m)
		}
		visiting[name] = false
		resolved[name] = m
		return m, nil
	}
	for _, name := range sortedKeys(profiles) {
		m, err := profile(name)
		if err != nil {
			return nil, err
		}
		a, err := decodeAgent(m)
		if err != nil {
			return nil, err
		}
		switch a.Spec.Tools.Mode {
		case "", "allowlist", "denylist", "unrestricted":
		default:
			return nil, fmt.Errorf("profile %s: invalid tools mode", name)
		}
		switch a.Spec.Approvals.Mode {
		case "", "never", "selective", "always", "inherit":
		default:
			return nil, fmt.Errorf("profile %s: invalid approvals mode", name)
		}
		for _, patterns := range [][]string{a.Spec.Tools.Allow, a.Spec.Tools.Deny, a.Spec.Approvals.Require} {
			for _, pattern := range patterns {
				if pattern == "" {
					return nil, fmt.Errorf("profile %s: empty operation pattern", name)
				}
				if err = validPattern(pattern); err != nil {
					return nil, err
				}
			}
		}
		if a.Spec.Execution.Timeout != "" {
			d, err := time.ParseDuration(a.Spec.Execution.Timeout)
			if err != nil || d < time.Second || d > 24*time.Hour {
				return nil, fmt.Errorf("profile %s: invalid timeout", name)
			}
		}
		if a.Spec.Execution.MaxRetries < 0 || a.Spec.Execution.MaxConcurrentRuns < 0 || a.Spec.Execution.MaxConcurrentRuns > 1 {
			return nil, fmt.Errorf("profile %s: unsupported execution limits", name)
		}
		for _, skill := range a.Spec.Skills.Include {
			if _, ok := c.Skills.Skills[skill]; !ok {
				return nil, fmt.Errorf("profile %s: unknown skill %s", name, skill)
			}
		}
		for _, policy := range a.Spec.Policies {
			if _, ok := c.Policies[policy]; !ok {
				return nil, fmt.Errorf("profile %s: unknown policy %s", name, policy)
			}
		}
		c.Profiles[name] = a
	}
	for _, name := range sortedKeys(agents) {
		m := agents[name]
		s := m["spec"].(map[string]any)
		if parent, ok := s["extends"].(string); ok && parent != "" {
			p, err := profile(parent)
			if err != nil {
				return nil, err
			}
			m = mergeDocument(p, m)
		}
		a, err := decodeAgent(m)
		if err != nil {
			return nil, err
		}
		if err = c.finish(root, &a); err != nil {
			return nil, fmt.Errorf("agent %s: %w", name, err)
		}
		c.Agents[name] = a
	}
	if len(c.Agents) == 0 {
		return nil, fmt.Errorf("no agents in %s/configs/agents", root)
	}
	for name, a := range c.Agents {
		if name == "owner" {
			return nil, fmt.Errorf("agent name owner is reserved")
		}
		for _, target := range a.Spec.Communication.CanMessage {
			if _, ok := c.Agents[target]; !ok && target != "owner" {
				return nil, fmt.Errorf("agent %s: unknown communication target %s", name, target)
			}
		}
	}
	return c, nil
}
func documents(root, dir, kind string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	paths, err := filepath.Glob(filepath.Join(root, "configs", dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		var a Agent
		if err = configfile.Read(p, &a); err != nil {
			return nil, err
		}
		if a.APIVersion != "factory/v1" || a.Kind != kind || !namePattern.MatchString(a.Metadata.Name) {
			return nil, fmt.Errorf("%s: invalid version, kind or name", p)
		}
		if _, ok := out[a.Metadata.Name]; ok {
			return nil, fmt.Errorf("duplicate %s %s", kind, a.Metadata.Name)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		if err = yaml.Unmarshal(b, &m); err != nil {
			return nil, err
		}
		if err := nonNull(m); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		if _, ok := m["spec"].(map[string]any); !ok {
			return nil, fmt.Errorf("%s: spec mapping required", p)
		}
		out[a.Metadata.Name] = m
	}
	return out, nil
}
func mergeDocument(parent, child map[string]any) map[string]any {
	return map[string]any{"apiVersion": child["apiVersion"], "kind": child["kind"], "metadata": child["metadata"], "spec": merge(parent["spec"].(map[string]any), child["spec"].(map[string]any), "")}
}
func merge(parent, child map[string]any, prefix string) map[string]any {
	out := map[string]any{}
	for k, v := range parent {
		out[k] = v
	}
	for k, v := range child {
		key := prefix + k
		if key == "approvals.mode" && v == "inherit" {
			if _, ok := out[k]; ok {
				continue
			}
		}
		if a, ok := v.(map[string]any); ok {
			if b, ok := out[k].(map[string]any); ok {
				out[k] = merge(b, a, key+".")
				continue
			}
		}
		if key == "skills.include" || key == "policies" || key == "tools.deny" || key == "approvals.require" {
			if a, ok := v.([]any); ok {
				b, _ := out[k].([]any)
				set := map[any]bool{}
				xs := []any{}
				for _, x := range append(append([]any{}, b...), a...) {
					if text, ok := x.(string); ok && !set[text] {
						set[text] = true
						xs = append(xs, text)
					}
				}
				out[k] = xs
				continue
			}
		}
		out[k] = v
	}
	return out
}
func decodeAgent(m map[string]any) (Agent, error) {
	var a Agent
	b, err := yaml.Marshal(m)
	if err != nil {
		return a, err
	}
	err = configfile.Decode(b, &a)
	return a, err
}
func (c *Catalog) finish(root string, a *Agent) error {
	if !protocol.RuntimeProvider(a.Spec.Runtime.Provider).Valid() {
		return fmt.Errorf("unsupported runtime provider %q: expected codex or claude", a.Spec.Runtime.Provider)
	}
	if a.Spec.Runtime.Model == "" {
		a.Spec.Runtime.Model = "default"
	}
	if a.Spec.Execution.MaxConcurrentRuns != 1 {
		return fmt.Errorf("current runtime supports max_concurrent_runs: 1")
	}
	if a.Spec.Execution.MaxRetries < 0 {
		return fmt.Errorf("negative max_retries")
	}
	var err error
	a.Timeout, err = time.ParseDuration(a.Spec.Execution.Timeout)
	if err != nil || a.Timeout < time.Second || a.Timeout > 24*time.Hour {
		return fmt.Errorf("timeout must be 1s..24h")
	}
	switch a.Spec.Tools.Mode {
	case "":
		a.Spec.Tools.Mode = "denylist"
	case "denylist", "allowlist", "unrestricted":
	default:
		return fmt.Errorf("invalid tools mode")
	}
	switch a.Spec.Approvals.Mode {
	case "", "inherit":
		a.Spec.Approvals.Mode = "selective"
	case "never", "selective", "always":
	default:
		return fmt.Errorf("invalid approvals mode")
	}
	for _, patterns := range [][]string{a.Spec.Tools.Allow, a.Spec.Tools.Deny, a.Spec.Approvals.Require} {
		for _, pattern := range patterns {
			if pattern == "" {
				return fmt.Errorf("empty operation pattern")
			}
			if err = validPattern(pattern); err != nil {
				return err
			}
		}
	}
	for _, n := range a.Spec.Skills.Include {
		if _, ok := c.Skills.Skills[n]; !ok {
			return fmt.Errorf("unknown skill %s", n)
		}
	}
	a.AvailableSkills = c.Skills.Available(a.Spec.Skills.Include, a.Spec.Skills.Discover.Enabled, a.Spec.Skills.Discover.Categories)
	selected := map[string]bool{}
	for _, n := range a.Spec.Policies {
		if _, ok := c.Policies[n]; !ok {
			return fmt.Errorf("unknown policy %s", n)
		}
		selected[n] = true
	}
	for n, p := range c.Policies {
		if p.Spec.Mandatory {
			selected[n] = true
		}
	}
	a.Spec.Policies = nil
	for n := range selected {
		a.Spec.Policies = append(a.Spec.Policies, n)
	}
	sort.Strings(a.Spec.Policies)
	seenRules := map[string]bool{}
	for _, n := range a.Spec.Policies {
		for _, r := range c.Policies[n].Spec.Rules {
			b, _ := yaml.Marshal(r)
			key := string(b)
			if policies.Applies(r, a.Spec.Role, a.Metadata.Name) && !seenRules[key] {
				seenRules[key] = true
				a.Rules = append(a.Rules, r)
			}
		}
	}
	p, err := configfile.Path(root, a.Spec.Prompt.File)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	var ctx strings.Builder
	ctx.Write(b)
	ctx.WriteString("\n\nFactory policies (trusted-agent instructions; full host access is not isolated):\n")
	for _, r := range a.Rules {
		fmt.Fprintf(&ctx, "- %s [%s", r.Name, r.Type)
		if r.Effect != "" {
			fmt.Fprintf(&ctx, ", %s", r.Effect)
		}
		if r.Target != "" {
			fmt.Fprintf(&ctx, ", %s", r.Target)
		}
		fmt.Fprintf(&ctx, "] %s Operations: %s\n", r.Instruction, strings.Join(r.Operations, ", "))
	}
	fmt.Fprintf(&ctx, "Factory tools: mode=%s; allow=%v; deny=%v. Factory approvals: mode=%s; require=%v. The policy evaluator marks protected operations as requiring parameter-bound approval. For supported mutation tools, request an exact call through factory_owner_request_approval and finish the turn when blocking. Only APPROVED authorizes retry with identical arguments; deny takes precedence. Release, purchase, production-secret and project-deletion executors are not implemented yet.\n", a.Spec.Tools.Mode, a.Spec.Tools.Allow, a.Spec.Tools.Deny, a.Spec.Approvals.Mode, a.Spec.Approvals.Require)
	fmt.Fprintf(&ctx, "Communication: can_message=%v; can_create_tasks=%t; can_assign_tasks=%t. Core enforces these permissions for Factory messaging and task tools.\n", a.Spec.Communication.CanMessage, a.Spec.Communication.CanCreateTasks, a.Spec.Communication.CanAssignTasks)
	if len(a.AvailableSkills) > 0 {
		ctx.WriteString("Available skills (metadata only; read on demand with factory skills read AGENT SKILL [RESOURCE]):\n")
		for _, s := range c.Skills.Entries(a.AvailableSkills, "", "") {
			fmt.Fprintf(&ctx, "- %s: %s (categories: %s)\n", s.Name, s.Description, strings.Join(s.Categories, ", "))
		}
	}
	a.Instructions = ctx.String()
	return nil
}
func (a Agent) Authorize(op string) policies.Decision {
	d := policies.Decision{Effect: "allow", Reasons: []string{}}
	add := func(effect, reason string) {
		rank := map[string]int{"allow": 0, "approval": 1, "deny": 2}
		if rank[effect] > rank[d.Effect] {
			d.Effect = effect
		}
		d.Reasons = append(d.Reasons, reason)
	}
	if a.Spec.Tools.Mode == "allowlist" && !policies.Match(a.Spec.Tools.Allow, op) {
		add("deny", "not in tool allowlist")
	}
	if policies.Match(a.Spec.Tools.Deny, op) {
		add("deny", "tool denylist")
	}
	for _, r := range a.Rules {
		if r.Type == "access" && policies.Match(r.Operations, op) {
			add(r.Effect, "policy rule "+r.Name)
		}
	}
	if a.Spec.Approvals.Mode == "always" || policies.Match(a.Spec.Approvals.Require, op) {
		add("approval", "agent approval settings")
	}
	return d
}
func Names(agents map[string]Agent) []string { return sortedKeys(agents) }
func sortedKeys[T any](m map[string]T) []string {
	xs := []string{}
	for n := range m {
		xs = append(xs, n)
	}
	sort.Strings(xs)
	return xs
}

func nonNull(v any) error {
	switch x := v.(type) {
	case nil:
		return fmt.Errorf("null configuration values are not allowed; omit the field or use an explicit empty list")
	case map[string]any:
		for _, child := range x {
			if err := nonNull(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range x {
			if err := nonNull(child); err != nil {
				return err
			}
		}
	}
	return nil
}

// DecisionAuthority requires explicit delegation; the ordinary default allow is insufficient.
func (a Agent) DecisionAuthority(category string) []string {
	if !namePattern.MatchString(category) {
		return nil
	}
	op := "factory.decision.decide." + category
	if a.Authorize("factory.decision.decide").Effect != "allow" || a.Authorize(op).Effect != "allow" {
		return nil
	}
	rules := []string{}
	for _, r := range a.Rules {
		if r.Type == "access" && r.Effect == "allow" && policies.Applies(r, a.Spec.Role, a.Metadata.Name) && policies.Match(r.Operations, op) {
			rules = append(rules, r.Name)
		}
	}
	return rules
}
