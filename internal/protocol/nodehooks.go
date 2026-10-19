package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// NodeHooks is the spec of a company catalog NodeHooks document (node-agent D4, D7) as Core delivers it to
// nodes: janitor hooks with the shape of node.yaml janitor, and stack checks the node runs with its
// self-checks and `factory-node doctor`. Hooks and checks are trusted template data run with /bin/sh -c.
type NodeHooks struct {
	// OnRunStop runs after every run with FACTORY_RUN_ID, FACTORY_TASK_ID and FACTORY_WORKTREE.
	OnRunStop []string `yaml:"on_run_stop" json:"on_run_stop,omitempty"`
	// OnTaskClosed runs once per closed task before its worktree is removed (FACTORY_TASK_ID, FACTORY_WORKTREE).
	OnTaskClosed []string `yaml:"on_task_closed" json:"on_task_closed,omitempty"`
	// Prune are cache cleanups; each gets FACTORY_OLDER_THAN_MINUTES (0 below the low disk watermark).
	Prune []NodePrune `yaml:"prune" json:"prune,omitempty"`
	// Checks are stack checks (e.g. `xcrun simctl list`): a check passes when its command exits 0.
	Checks []NodeStackCheck `yaml:"checks" json:"checks,omitempty"`
}

// NodePrune is one prune command; OlderThan is a Go duration ("72h"), empty is 0.
type NodePrune struct {
	Command   string `yaml:"command" json:"command"`
	OlderThan string `yaml:"older_than" json:"older_than,omitempty"`
}

// NodeStackCheck is one stack check. A node runs it only when it has every label in Labels; a failure is
// the self-check `stack.<name>` (node.check_failed{check}).
type NodeStackCheck struct {
	Name    string   `yaml:"name" json:"name"`
	Command string   `yaml:"command" json:"command"`
	Hint    string   `yaml:"hint" json:"hint,omitempty"`
	Labels  []string `yaml:"labels" json:"labels,omitempty"`
}

// Empty reports whether the document defines nothing.
func (h NodeHooks) Empty() bool {
	return len(h.OnRunStop) == 0 && len(h.OnTaskClosed) == 0 && len(h.Prune) == 0 && len(h.Checks) == 0
}

// Hash identifies the content: a node sends the hash it applied and gets the hooks again when it differs.
func (h NodeHooks) Hash() string {
	b, _ := json.Marshal(h)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// NodeHooksDelivery is the `hooks` object of the register and heartbeat answers (runtime protocol v2 §3).
// Missing: the catalog has no NodeHooks document of that name (Spec is empty; the node keeps node.yaml's).
type NodeHooksDelivery struct {
	Name    string    `json:"name"`
	Hash    string    `json:"hash"`
	Missing bool      `json:"missing,omitempty"`
	Spec    NodeHooks `json:"spec"`
}
