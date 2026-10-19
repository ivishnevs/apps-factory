package config

import (
	"fmt"
	"path/filepath"
	"regexp"
	"time"

	"appfactory/internal/configfile"
	"appfactory/internal/protocol"
)

// DefaultNodeHooks is the NodeHooks document a node gets when node.yaml names none.
const DefaultNodeHooks = "default"

// NodeHooksDocument is a configs/node-hooks/<name>.yaml document (node-agent D4, D7).
type NodeHooksDocument struct {
	APIVersion string             `yaml:"apiVersion"`
	Kind       string             `yaml:"kind"`
	Metadata   Metadata           `yaml:"metadata"`
	Spec       protocol.NodeHooks `yaml:"spec"`
}

func loadNodeHooks(root string) (map[string]protocol.NodeHooks, error) {
	out := map[string]protocol.NodeHooks{}
	paths, err := filepath.Glob(filepath.Join(root, "configs", "node-hooks", "*.yaml"))
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		var d NodeHooksDocument
		if err = configfile.Read(p, &d); err != nil {
			return nil, err
		}
		if d.APIVersion != "factory/v1" || d.Kind != "NodeHooks" || !namePattern.MatchString(d.Metadata.Name) {
			return nil, fmt.Errorf("%s: invalid version, kind or name", p)
		}
		if _, ok := out[d.Metadata.Name]; ok {
			return nil, fmt.Errorf("duplicate NodeHooks %s", d.Metadata.Name)
		}
		if err = ValidateNodeHooks(d.Spec); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		out[d.Metadata.Name] = d.Spec
	}
	return out, nil
}

// nodeLabel is a node label as node.yaml accepts it.
var nodeLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)

// ValidateNodeHooks rejects empty commands, bad durations and duplicate or invalid check names.
func ValidateNodeHooks(h protocol.NodeHooks) error {
	for _, c := range append(append([]string{}, h.OnRunStop...), h.OnTaskClosed...) {
		if c == "" {
			return fmt.Errorf("empty hook command")
		}
	}
	for _, p := range h.Prune {
		if p.Command == "" {
			return fmt.Errorf("prune: command required")
		}
		if p.OlderThan != "" {
			if d, err := time.ParseDuration(p.OlderThan); err != nil || d < 0 {
				return fmt.Errorf("prune %q: older_than must be a duration of at least 0", p.Command)
			}
		}
	}
	seen := map[string]bool{}
	for _, c := range h.Checks {
		if !namePattern.MatchString(c.Name) || c.Command == "" {
			return fmt.Errorf("check %q: lowercase name and command required", c.Name)
		}
		if seen[c.Name] {
			return fmt.Errorf("duplicate check %s", c.Name)
		}
		seen[c.Name] = true
		for _, l := range c.Labels {
			if !nodeLabel.MatchString(l) {
				return fmt.Errorf("check %s: invalid label %q", c.Name, l)
			}
		}
	}
	return nil
}

// NodeHooksFor is the delivery of the named document (DefaultNodeHooks when name is empty).
func (c *Catalog) NodeHooksFor(name string) protocol.NodeHooksDelivery {
	return DeliverNodeHooks(c.NodeHooks, name)
}

// DeliverNodeHooks looks name up in docs (DefaultNodeHooks when empty). A name without a document is
// delivered Missing with an empty spec, so the node runs node.yaml's janitor alone.
func DeliverNodeHooks(docs map[string]protocol.NodeHooks, name string) protocol.NodeHooksDelivery {
	if name == "" {
		name = DefaultNodeHooks
	}
	spec, ok := docs[name]
	return protocol.NodeHooksDelivery{Name: name, Hash: spec.Hash(), Missing: !ok, Spec: spec}
}
