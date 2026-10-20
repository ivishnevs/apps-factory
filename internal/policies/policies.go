package policies

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"time"
)

type Rule struct {
	Name        string   `yaml:"name" json:"name"`
	Type        string   `yaml:"type" json:"type"`
	Operations  []string `yaml:"operations" json:"operations,omitempty"`
	Commands    []string `yaml:"commands" json:"commands,omitempty"`
	Effect      string   `yaml:"effect" json:"effect,omitempty"`
	Target      string   `yaml:"target" json:"target,omitempty"`
	Instruction string   `yaml:"instruction" json:"instruction"`
	// Mode of a recovery rule (reliable-recovery D4): inspect parks an unclear end of the agent's runs for
	// inspection instead of retrying it.
	Mode string `yaml:"mode" json:"mode,omitempty"`
	// Wait rules (waits-events-cost-cli D1): the shortest wait (15m..30m, default 15m) and how many
	// consecutive waits without progress a task may make (default 12).
	MinWaitInterval string `yaml:"min_wait_interval" json:"min_wait_interval,omitempty"`
	MaxWaits        int    `yaml:"max_waits" json:"max_waits,omitempty"`
	// Mandatory reports of a reporting rule (D2): on these exits a report (of one of ReportKinds, any kind
	// when empty) is expected from the run. Never blocking: a missing one is an event and a reminder.
	Exits       []string `yaml:"exits" json:"exits,omitempty"`
	ReportKinds []string `yaml:"report_kinds" json:"report_kinds,omitempty"`
	Roles       []string `yaml:"roles" json:"roles,omitempty"`
	Agents      []string `yaml:"agents" json:"agents,omitempty"`
}
type Policy struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`
	Metadata   struct {
		Name string `yaml:"name" json:"name"`
	} `yaml:"metadata" json:"metadata"`
	Spec struct {
		Extends   string `yaml:"extends" json:"extends,omitempty"`
		Mandatory bool   `yaml:"mandatory" json:"mandatory"`
		Rules     []Rule `yaml:"rules" json:"rules"`
	} `yaml:"spec" json:"spec"`
}
type Decision struct {
	Effect  string   `json:"effect"`
	Reasons []string `json:"reasons"`
}

func System() Policy {
	var p Policy
	p.APIVersion = "factory/v1"
	p.Kind = "Policy"
	p.Metadata.Name = "system"
	p.Spec.Mandatory = true
	p.Spec.Rules = []Rule{{Name: "protected-actions", Type: "access", Operations: []string{"factory.project.delete", "factory.release.publish", "factory.secrets.access_production", "external.purchase", "factory.config.*", "factory.decision.record"}, Effect: "approval", Instruction: "Ask the owner before deleting projects, publishing apps, accessing production credentials, paid external operations, changing factory configuration, or recording an approved project decision. Approval must identify the action and its parameters."}}
	return p
}
func Validate(p Policy) error {
	if p.APIVersion != "factory/v1" || p.Kind != "Policy" {
		return fmt.Errorf("invalid policy version/kind")
	}
	names := map[string]bool{}
	for _, r := range p.Spec.Rules {
		if r.Name == "" || names[r.Name] || strings.TrimSpace(r.Instruction) == "" {
			return fmt.Errorf("policy %s: unique rule name and instruction required", p.Metadata.Name)
		}
		names[r.Name] = true
		if r.Mode != "" && r.Type != "recovery" {
			return fmt.Errorf("rule %s: mode only applies to recovery rules", r.Name)
		}
		if (r.MinWaitInterval != "" || r.MaxWaits != 0) && r.Type != "wait" {
			return fmt.Errorf("rule %s: min_wait_interval and max_waits only apply to wait rules", r.Name)
		}
		if len(r.Exits)+len(r.ReportKinds) > 0 && r.Type != "reporting" {
			return fmt.Errorf("rule %s: exits and report_kinds only apply to reporting rules", r.Name)
		}
		switch r.Type {
		case "access":
			if len(r.Operations)+len(r.Commands) == 0 || (r.Effect != "allow" && r.Effect != "deny" && r.Effect != "approval") || r.Target != "" {
				return fmt.Errorf("invalid access rule %s", r.Name)
			}
		case "escalation":
			if len(r.Commands) > 0 {
				return fmt.Errorf("commands only apply to access rules")
			}
			if r.Target != "self" && r.Target != "manager" && r.Target != "owner" {
				return fmt.Errorf("invalid escalation target")
			}
			if r.Effect != "" {
				return fmt.Errorf("effect only applies to access rules")
			}
		case "reporting":
			if r.Effect != "" || r.Target != "" || len(r.Commands) > 0 {
				return fmt.Errorf("reporting uses instructions, not effect/target")
			}
			for _, e := range r.Exits {
				if !contains([]string{"submit", "handoff", "done", "ask", "block", "wait"}, e) {
					return fmt.Errorf("reporting rule %s: unknown exit %q", r.Name, e)
				}
			}
		case "wait":
			if r.MinWaitInterval != "" {
				d, err := time.ParseDuration(r.MinWaitInterval)
				if err != nil || d < 15*time.Minute || d > 30*time.Minute {
					return fmt.Errorf("wait rule %s: min_wait_interval between 15m and 30m", r.Name)
				}
			}
			if r.MaxWaits < 0 || r.MaxWaits > 100 {
				return fmt.Errorf("wait rule %s: max_waits 1..100", r.Name)
			}
			if r.Effect != "" || r.Target != "" || len(r.Commands)+len(r.Operations) > 0 {
				return fmt.Errorf("wait rule %s: only min_wait_interval, max_waits, roles and agents apply", r.Name)
			}
		case "recovery":
			if r.Mode != "inspect" {
				return fmt.Errorf("recovery rule %s: mode inspect required", r.Name)
			}
			if r.Effect != "" || r.Target != "" || len(r.Commands)+len(r.Operations) > 0 {
				return fmt.Errorf("recovery rule %s: only mode, roles and agents apply", r.Name)
			}
		default:
			return fmt.Errorf("invalid policy rule type %q", r.Type)
		}
		for _, op := range r.Operations {
			if op == "" {
				return fmt.Errorf("empty operation pattern")
			}
			if _, err := path.Match(op, "test"); err != nil {
				return err
			}
		}
		for _, c := range r.Commands {
			if err := validCommand(c); err != nil {
				return fmt.Errorf("policy %s rule %s: %w", p.Metadata.Name, r.Name, err)
			}
		}
	}
	return nil
}
func Applies(r Rule, role, agent string) bool {
	return (len(r.Roles) == 0 || contains(r.Roles, role)) && (len(r.Agents) == 0 || contains(r.Agents, agent))
}
func contains(xs []string, s string) bool {
	for _, v := range xs {
		if v == s {
			return true
		}
	}
	return false
}
func Match(patterns []string, op string) bool {
	for _, p := range patterns {
		if ok, _ := path.Match(p, op); ok {
			return true
		}
	}
	return false
}
func Resolve(raw map[string]Policy) (map[string]Policy, error) {
	out := map[string]Policy{}
	visiting := map[string]bool{}
	var resolve func(string) (Policy, error)
	resolve = func(name string) (Policy, error) {
		if p, ok := out[name]; ok {
			return p, nil
		}
		p, ok := raw[name]
		if !ok {
			return p, fmt.Errorf("missing policy %s", name)
		}
		if visiting[name] {
			return p, fmt.Errorf("policy cycle at %s", name)
		}
		visiting[name] = true
		if p.Spec.Extends != "" {
			parent, err := resolve(p.Spec.Extends)
			if err != nil {
				return p, err
			}
			p.Spec.Rules = append(append([]Rule{}, parent.Spec.Rules...), p.Spec.Rules...)
			p.Spec.Mandatory = p.Spec.Mandatory || parent.Spec.Mandatory
		}
		if err := Validate(p); err != nil {
			return p, err
		}
		visiting[name] = false
		out[name] = p
		return p, nil
	}
	names := []string{}
	for n := range raw {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, err := resolve(n); err != nil {
			return nil, err
		}
	}
	return out, nil
}
