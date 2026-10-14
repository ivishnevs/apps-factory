package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
	// ReportsTo is the agent's manager (hierarchy D1): another agent of the company, or owner (default).
	ReportsTo string `yaml:"reports_to" json:"reports_to,omitempty"`
	Runtime   struct {
		Provider string `yaml:"provider" json:"provider"`
		Model    string `yaml:"model" json:"model"`
		// QuotaKey names the subscription whose limit this agent consumes; agents sharing it pause
		// together when one hits the limit. Empty means the agent's name.
		QuotaKey string `yaml:"quota_key" json:"quota_key,omitempty"`
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
		// MaxConcurrentRunsPerProject caps the agent's live runs in one project (default max_concurrent_runs).
		MaxConcurrentRunsPerProject int `yaml:"max_concurrent_runs_per_project,omitempty" json:"max_concurrent_runs_per_project,omitempty"`
		MaxRetries                  int `yaml:"max_retries" json:"max_retries"`
	} `yaml:"execution" json:"execution"`
	Policies []string `yaml:"policies" json:"policies"`
	// Workspace.Mode: "" or shared runs in the project workspace; worktree runs each task in its own worktree;
	// scratch (no repository) runs in the project workspace through the Local allocator.
	Workspace struct {
		Mode string `yaml:"mode" json:"mode,omitempty"`
	} `yaml:"workspace" json:"workspace,omitzero"`
	// Resources are a run's needs per node resource class (e.g. simulator: 1); Core checks them against the
	// node's node.yaml resources at claim (runtime protocol v2).
	Resources map[string]int `yaml:"resources" json:"resources,omitempty"`
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
	rawPolicies := map[string]policies.Policy{}
	for _, p := range policies.Builtins() {
		rawPolicies[p.Metadata.Name] = p
	}
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
		if err = validWorkspaceMode(a.Spec.Workspace.Mode); err != nil {
			return nil, fmt.Errorf("profile %s: %w", name, err)
		}
		if err = validResources(a.Spec.Resources); err != nil {
			return nil, fmt.Errorf("profile %s: %w", name, err)
		}
		if a.Spec.Execution.MaxRetries < 0 || a.Spec.Execution.MaxConcurrentRuns < 0 || a.Spec.Execution.MaxConcurrentRuns > MaxConcurrentRuns || a.Spec.Execution.MaxConcurrentRunsPerProject < 0 {
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
	if err := validTree(c.Agents); err != nil {
		return nil, err
	}
	deriveCommunication(c.Agents)
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
	if q := a.Spec.Runtime.QuotaKey; q != "" && !namePattern.MatchString(q) {
		return fmt.Errorf("invalid runtime quota_key %q", q)
	}
	if err := ValidateRuntimeOptions(protocol.RuntimeProvider(a.Spec.Runtime.Provider), a.RuntimeOptions()); err != nil {
		return err
	}
	if err := validWorkspaceMode(a.Spec.Workspace.Mode); err != nil {
		return err
	}
	if err := validResources(a.Spec.Resources); err != nil {
		return err
	}
	// trigger-log-scheduling 2.5: caps checked at dispatch and claim; parallel runs of one agent need a node
	// (worktree tasks hold their own workspace slot; shared-workspace runs still go one at a time).
	if e := a.Spec.Execution; e.MaxConcurrentRuns < 1 || e.MaxConcurrentRuns > MaxConcurrentRuns || e.MaxConcurrentRunsPerProject < 0 || e.MaxConcurrentRunsPerProject > e.MaxConcurrentRuns {
		return fmt.Errorf("max_concurrent_runs must be 1..%d and max_concurrent_runs_per_project at most max_concurrent_runs", MaxConcurrentRuns)
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
	guarded := false
	for _, r := range a.Rules {
		fmt.Fprintf(&ctx, "- %s [%s", r.Name, r.Type)
		if r.Effect != "" {
			fmt.Fprintf(&ctx, ", %s", r.Effect)
		}
		if r.Target != "" {
			fmt.Fprintf(&ctx, ", %s", r.Target)
		}
		fmt.Fprintf(&ctx, "] %s Operations: %s", r.Instruction, strings.Join(r.Operations, ", "))
		if len(r.Commands) > 0 {
			fmt.Fprintf(&ctx, "; commands: %s", strings.Join(r.Commands, ", "))
			guarded = true
		}
		ctx.WriteString("\n")
	}
	if guarded {
		ctx.WriteString("Shell commands under deny or approval rules are guarded on PATH: a refused command exits 126 with the rule's instruction. For an approval rule, request factory_owner_request_approval with call {\"name\":\"command\",\"arguments\":{\"argv\":[exact argv, starting with the command name]}} and finish the turn; once APPROVED, exactly that command runs once.\n")
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

// RuntimeOptions are the runtime_options keys each driver reads (runtime-drivers D1). Drivers declare
// their own list (e.g. codex.Options) and test it against this registry; config stays free of driver imports.
var RuntimeOptions = map[protocol.RuntimeProvider][]string{
	protocol.RuntimeCodex:  {"model"},
	protocol.RuntimeClaude: {"model", "effort", "max_turns"},
}

// RuntimeOptions are the run's runtime_options derived from the agent's runtime section.
func (a Agent) RuntimeOptions() map[string]any {
	return map[string]any{"model": a.Spec.Runtime.Model}
}

// ValidateRuntimeOptions rejects an option the provider's driver does not read: never silently dropped.
func ValidateRuntimeOptions(p protocol.RuntimeProvider, opts map[string]any) error {
	known, ok := RuntimeOptions[p]
	if !ok {
		return fmt.Errorf("unsupported runtime provider %q", p)
	}
	for _, k := range sortedKeys(opts) {
		if !slices.Contains(known, k) {
			return fmt.Errorf("runtime option %q is not supported by %s (supported: %s)", k, p, strings.Join(known, ", "))
		}
	}
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

// AuthorizeCommand decides a shell command (argv[0] is the command name) with the agent's access rules.
func (a Agent) AuthorizeCommand(argv []string) policies.CommandDecision {
	return policies.Command(a.Rules, argv)
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

// validWorkspaceMode accepts RunSpec.workspace_mode values; empty is shared.
func validWorkspaceMode(m string) error {
	switch m {
	case "", "shared", "worktree", "scratch":
		return nil
	}
	return fmt.Errorf("invalid workspace mode %q: expected shared, worktree or scratch", m)
}

// validResources: resource classes are lowercase names, needs at least 1 (Core enforces them at claim).
func validResources(r map[string]int) error {
	for _, k := range slices.Sorted(maps.Keys(r)) {
		if !namePattern.MatchString(k) {
			return fmt.Errorf("invalid resource name %q", k)
		}
		if r[k] < 1 {
			return fmt.Errorf("resources.%s: need must be at least 1", k)
		}
	}
	return nil
}

// MaxConcurrentRuns is the highest max_concurrent_runs an agent may declare.
const MaxConcurrentRuns = 8

// Owner is the reserved principal at the top of every chain.
const Owner = "owner"

// Manager returns the agent's reports_to (owner when empty).
func Manager(agents map[string]Agent, name string) string {
	if m := agents[name].Spec.ReportsTo; m != "" {
		return m
	}
	return Owner
}

// Chain lists the agent's managers from its direct manager up, without the owner.
func Chain(agents map[string]Agent, name string) []string {
	var out []string
	for m := Manager(agents, name); m != Owner && len(out) <= len(agents); m = Manager(agents, m) {
		out = append(out, m)
	}
	return out
}

// Subtree lists the agents whose chain contains name (its reports at any depth), sorted.
func Subtree(agents map[string]Agent, name string) []string {
	var out []string
	for _, a := range Names(agents) {
		if slices.Contains(Chain(agents, a), name) {
			out = append(out, a)
		}
	}
	return out
}

// DirectReports lists the agents reporting to name (owner for the top level), sorted.
func DirectReports(agents map[string]Agent, name string) []string {
	var out []string
	for _, a := range Names(agents) {
		if Manager(agents, a) == name {
			out = append(out, a)
		}
	}
	return out
}

// validTree checks reports_to: an existing agent or owner, never itself, no cycles.
func validTree(agents map[string]Agent) error {
	for _, name := range Names(agents) {
		m := Manager(agents, name)
		if m == name {
			return fmt.Errorf("agent %s: reports_to itself", name)
		}
		if _, ok := agents[m]; !ok && m != Owner {
			return fmt.Errorf("agent %s: reports_to unknown agent %s", name, m)
		}
		seen := map[string]bool{name: true}
		for m != Owner {
			if seen[m] {
				return fmt.Errorf("agent %s: reports_to cycle through %s", name, m)
			}
			seen[m] = true
			m = Manager(agents, m)
		}
	}
	return nil
}

// deriveCommunication lets every agent message its chain (owner included) and its direct reports; explicit
// can_message lists may add peers but never remove these (hierarchy D1).
func deriveCommunication(agents map[string]Agent) {
	for _, name := range Names(agents) {
		a := agents[name]
		targets := append([]string{}, a.Spec.Communication.CanMessage...)
		for _, t := range append(append(Chain(agents, name), Owner), DirectReports(agents, name)...) {
			if !slices.Contains(targets, t) {
				targets = append(targets, t)
			}
		}
		a.Spec.Communication.CanMessage = targets
		agents[name] = a
	}
}
