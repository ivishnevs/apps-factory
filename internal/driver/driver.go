// Package driver is the runtime boundary of the run executor: a Driver starts one runtime
// session per run and reports normalized events. Claims, leases, heartbeats, credentials and
// event publication stay in the executor (internal/worker).
package driver

import (
	"context"
	"encoding/json"

	"appfactory/internal/node"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
)

type Driver interface {
	Name() protocol.RuntimeProvider
	Capabilities() Capabilities
	// CheckVersion fails when the installed runtime is outside the supported range.
	CheckVersion(ctx context.Context) (string, error)
	// Probe checks binary, supported version and authentication without a model call;
	// Installed tells whether the runtime is present at all (node.DriverProbe).
	Probe(ctx context.Context) []node.CheckResult
	Installed() bool
	// Launch prepares a session up to its ID without sending the prompt.
	Launch(ctx context.Context, l Launch) (Session, error)
	// ResumePolicy decides whether a run resumes the session of its (task, agent) key (design D1):
	// prev is what this node recorded about that session's last run, sameNode whether this node ran it
	// (false also after reset-workspace, which forgets the node's record). When it returns false the
	// executor starts a fresh session whose prompt says that the context comes from Core.
	ResumePolicy(prev Outcome, sameNode bool) bool
}

// Outcome is the last run of a session as this node recorded it.
type Outcome struct {
	// Status is the run's terminal status (COMPLETED, FAILED, CANCELLED, UNKNOWN); "" when the node
	// stopped before it was known.
	Status string
	// Started: the prompt reached the runtime, so the runtime persisted the session (a session launched
	// but never started has no transcript or rollout to resume).
	Started bool
}

// ResumeSameNode is the common policy: resume only a started session that this node ran.
func ResumeSameNode(prev Outcome, sameNode bool) bool { return sameNode && prev.Started }

type Capabilities struct {
	Resume, Browser, MCP, Cost, Activity, ToolPolicy, ExitGuard bool
}

type Launch struct {
	Spec protocol.RunSpec
	Cwd  string
	// Env is the full runtime environment; nil inherits the executor's environment.
	Env []string
	// Instructions are the developer instructions, including Factory MCP instructions.
	Instructions string
	MCP          *MCPConfig
	// Guards are the agent's access rules from the run's config snapshot; drivers with tool_policy
	// apply their command rules inside the runtime (the PATH shims stay in place).
	Guards []policies.Rule
	// Skills are the run's offered skills, copied by the executor under SkillsRoot (SkillsRoot/<name>)
	// and verified against the hashes pinned on the run; drivers expose them where their runtime reads
	// skills (Claude: <cwd>/.claude/skills; Codex: skills/extraRoots/set).
	Skills     []Skill
	SkillsRoot string
}

// Skill is one materialized skill: Hash is the verified content hash (skills.Hash) of SkillsRoot/<Name>.
type Skill struct {
	Name, Hash string
}

// MCPConfig is the Factory MCP stdio server; EnvVars name variables passed through from Env.
type MCPConfig struct {
	Command string
	EnvVars []string
}

type EventKind string

const (
	// EventActivity carries one raw runtime notification or request for audit and progress.
	EventActivity EventKind = "activity"
	// EventUsage carries Usage: cumulative tokens of one model in this run (the last per model
	// wins), with its cost when the runtime prices it per model.
	EventUsage EventKind = "usage"
	// EventCost carries Cost: the run's cumulative cost as the runtime reports it.
	EventCost EventKind = "cost"
	// EventQuota carries Quota: the runtime hit its subscription limit.
	EventQuota EventKind = "quota"
)

// Event is one normalized event; Method and Data keep the raw runtime message for audit.
type Event struct {
	Kind   EventKind
	Method string
	Data   json.RawMessage
	Usage  *protocol.Usage
	Cost   *Cost
	Quota  *protocol.Quota
}

// Cost is a runtime-reported price in USD; Source names it (e.g. "claude_estimate").
type Cost struct {
	USD    float64
	Source string
}

// Result is terminal evidence. Status is COMPLETED, FAILED or CANCELLED; Err means the
// outcome is unknown.
type Result struct {
	Status, Text string
	Err          error
}

type Session interface {
	ID() string
	// Start sends the prompt. Events is closed after the last event; Done then yields the result.
	Start(ctx context.Context) error
	Events() <-chan Event
	Done() <-chan Result
	// Interrupt requests cancellation; terminal evidence still arrives through Done.
	Interrupt(ctx context.Context) error
	// ProcessGroup is the runtime's process group, 0 when it has none; Close kills it.
	ProcessGroup() int
	Close()
}
