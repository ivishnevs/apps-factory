package config

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Assignment by hierarchy (assign-by-hierarchy): spec.communication.can_assign_tasks says whom an agent may give
// tasks to. Core checks every create, assign and reassign against it, and the agent's roster lists only those
// agents, so routing lives in the catalog, not in prompts.

// Assignment scopes.
const (
	AssignNone          = "none"           // assigns to no one
	AssignDirectReports = "direct_reports" // the agents reporting to it (default for an agent with reports)
	AssignSubtree       = "subtree"        // anyone below it in the reporting tree
	AssignList          = "list"           // the named agents
)

// AssignScope is spec.communication.can_assign_tasks: none, direct_reports, subtree or a list of agent names.
// Unset means direct_reports for an agent with reports and none otherwise; YAML/JSON true and false are short for
// direct_reports and none.
type AssignScope struct {
	Mode   string   `yaml:"-" json:"-"`
	Agents []string `yaml:"-" json:"-"`
}

// Set reports whether the catalog states the scope.
func (s AssignScope) Set() bool { return s.Mode != "" }

func (s *AssignScope) setScalar(v string) error {
	switch v {
	case "true":
		s.Mode = AssignDirectReports
	case "false":
		s.Mode = AssignNone
	case AssignNone, AssignDirectReports, AssignSubtree:
		s.Mode = v
	default:
		return fmt.Errorf("can_assign_tasks: %q (none, direct_reports, subtree or a list of agents)", v)
	}
	s.Agents = nil
	return nil
}

// UnmarshalYAML reads a scope word, a boolean or a list of agent names.
func (s *AssignScope) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!null" {
			*s = AssignScope{}
			return nil
		}
		return s.setScalar(n.Value)
	case yaml.SequenceNode:
		var names []string
		if err := n.Decode(&names); err != nil {
			return fmt.Errorf("can_assign_tasks: a list of agent names: %w", err)
		}
		s.Mode, s.Agents = AssignList, names
		return nil
	}
	return fmt.Errorf("can_assign_tasks: none, direct_reports, subtree or a list of agents")
}

// MarshalYAML writes the form it was read in (a list, or the scope word); unset is null.
func (s AssignScope) MarshalYAML() (any, error) {
	switch s.Mode {
	case "":
		return nil, nil
	case AssignList:
		return s.Agents, nil
	}
	return s.Mode, nil
}

// UnmarshalJSON reads the same shapes from JSON.
func (s *AssignScope) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch x := v.(type) {
	case nil:
		*s = AssignScope{}
	case bool:
		return s.setScalar(fmt.Sprint(x))
	case string:
		return s.setScalar(x)
	case []any:
		names := make([]string, 0, len(x))
		for _, e := range x {
			name, ok := e.(string)
			if !ok {
				return fmt.Errorf("can_assign_tasks: a list of agent names")
			}
			names = append(names, name)
		}
		s.Mode, s.Agents = AssignList, names
	default:
		return fmt.Errorf("can_assign_tasks: none, direct_reports, subtree or a list of agents")
	}
	return nil
}

// MarshalJSON writes the list or the scope word; unset is null.
func (s AssignScope) MarshalJSON() ([]byte, error) {
	v, _ := s.MarshalYAML()
	return json.Marshal(v)
}

// AssignScopeOf is the agent's effective scope: its stated one, else direct_reports when it has reports and none
// otherwise.
func AssignScopeOf(agents map[string]Agent, name string) AssignScope {
	if s := agents[name].Spec.Communication.CanAssignTasks; s.Set() {
		return s
	}
	if len(DirectReports(agents, name)) > 0 {
		return AssignScope{Mode: AssignDirectReports}
	}
	return AssignScope{Mode: AssignNone}
}

// CanAssign reports whether the agent may assign tasks to anyone (its scope is not none).
func CanAssign(agents map[string]Agent, name string) bool {
	return AssignScopeOf(agents, name).Mode != AssignNone
}

// AssignTargets lists the agents name may assign tasks to (itself excluded), sorted.
func AssignTargets(agents map[string]Agent, name string) []string {
	s := AssignScopeOf(agents, name)
	var out []string
	switch s.Mode {
	case AssignDirectReports:
		out = DirectReports(agents, name)
	case AssignSubtree:
		out = Subtree(agents, name)
	case AssignList:
		for _, a := range s.Agents {
			if _, ok := agents[a]; ok && a != name && !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
		slices.Sort(out)
	}
	return out
}

// MayAssign reports whether actor may give a task to assignee: the owner (or no agent actor) to anyone, an agent to
// itself or within its scope.
func MayAssign(agents map[string]Agent, actor, assignee string) bool {
	if actor == "" || actor == Owner || actor == assignee {
		return true
	}
	return slices.Contains(AssignTargets(agents, actor), assignee)
}

// AssignRefusal explains why actor may not give a task to assignee and whom to give it to instead: the closest
// manager of assignee that actor may assign to ("" when actor may assign it).
func AssignRefusal(agents map[string]Agent, actor, assignee string) string {
	if MayAssign(agents, actor, assignee) {
		return ""
	}
	if !CanAssign(agents, actor) {
		return fmt.Sprintf("%s may not assign tasks (can_assign_tasks: none); ask your manager", actor)
	}
	for _, m := range Chain(agents, assignee) {
		if MayAssign(agents, actor, m) && m != actor {
			return fmt.Sprintf("%s reports to %s: give the task to %s, who passes the work on", assignee, Manager(agents, assignee), m)
		}
	}
	return fmt.Sprintf("%s is outside your assignment scope; you may assign to %s", assignee, strings.Join(AssignTargets(agents, actor), ", "))
}

// validAssignScopes checks listed agents: each exists, is another agent and is on the same team.
func validAssignScopes(agents map[string]Agent) error {
	for _, name := range Names(agents) {
		a := agents[name]
		s := a.Spec.Communication.CanAssignTasks
		if s.Mode != AssignList {
			continue
		}
		for _, t := range s.Agents {
			o, ok := agents[t]
			switch {
			case !ok:
				return fmt.Errorf("agent %s: can_assign_tasks names unknown agent %s", name, t)
			case t == name:
				return fmt.Errorf("agent %s: can_assign_tasks names itself", name)
			case a.Spec.Team != "" && o.Spec.Team != a.Spec.Team:
				return fmt.Errorf("agent %s: can_assign_tasks names %s of team %s", name, t, o.Spec.Team)
			}
		}
	}
	return nil
}

// AssignWarnings lists scopes that give nothing: direct_reports or subtree stated on an agent without reports.
func AssignWarnings(c *Catalog) []string {
	var out []string
	for _, name := range Names(c.Agents) {
		s := c.Agents[name].Spec.Communication.CanAssignTasks
		if (s.Mode == AssignDirectReports || s.Mode == AssignSubtree) && len(DirectReports(c.Agents, name)) == 0 {
			out = append(out, fmt.Sprintf("agent %s: can_assign_tasks is %s but no agent reports to it", name, s.Mode))
		}
	}
	return out
}

// hierarchyInstructions is the part of an agent's instructions that needs the whole catalog: its communication
// permissions with the assignment scope and, for agents that create or assign tasks, the roster of the agents it may
// assign to, each with what its own team covers.
func hierarchyInstructions(c *Catalog, name string) string {
	a := c.Agents[name]
	targets := AssignTargets(c.Agents, name)
	var b strings.Builder
	scope := AssignScopeOf(c.Agents, name)
	desc := scope.Mode
	if scope.Mode == AssignList {
		desc = "the listed agents"
	}
	fmt.Fprintf(&b, "Communication: can_message=%v; can_create_tasks=%t; can_assign_tasks=%s", a.Spec.Communication.CanMessage, a.Spec.Communication.CanCreateTasks, desc)
	if len(targets) > 0 {
		fmt.Fprintf(&b, " (you may assign to: %s)", strings.Join(targets, ", "))
	}
	b.WriteString(". Core enforces these permissions for Factory messaging and task tools; a task for an agent outside your assignment scope is refused with the agent to give it to.\n")
	if !a.Spec.Communication.CanCreateTasks && len(targets) == 0 {
		return b.String()
	}
	if len(targets) > 0 {
		// The roster (agent-capabilities 2.3, assign-by-hierarchy): only whom the agent may assign to, with each
		// one's own team, so work for deeper agents goes through their manager.
		b.WriteString("Agents you assign work to (pick by what each does and needs; when one agent's work needs another's output, create both tasks and link them with dependencies):\n")
		for _, t := range targets {
			o := c.Agents[t]
			fmt.Fprintf(&b, "- %s (%s)", t, o.Spec.Role)
			if o.Metadata.Description != "" {
				fmt.Fprintf(&b, ": %s", o.Metadata.Description)
			}
			if len(o.Spec.Capabilities) > 0 {
				fmt.Fprintf(&b, ". %s", strings.Join(o.Spec.Capabilities, "; "))
			}
			if team := Subtree(c.Agents, t); len(team) > 0 {
				fmt.Fprintf(&b, ". Its team (give their work to %s): %s", t, strings.Join(team, ", "))
			}
			b.WriteString("\n")
		}
	}
	// dependency-wait-escalations 3.2: a planned wait is a dependency, never a manual block.
	b.WriteString("Waiting: a task with open dependencies waits and starts by itself when they are done; never block it to hold it (that is refused, and only the owner clears a block). To make a task wait for work, add that work as a dependency. A stalled escalation about a planned wait: answer keep_waiting; drop_dependency removes the dependency.\n")
	return b.String()
}
