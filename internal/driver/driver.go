// Package driver is the runtime boundary of the run executor: a Driver starts one runtime
// session per run and reports normalized events. Claims, leases, heartbeats, credentials and
// event publication stay in the executor (internal/worker).
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os/exec"

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
	// Status is the run's terminal status (COMPLETED, FAILED, CANCELLED; UNKNOWN in records from before reliable-recovery); "" when the node
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
	// ExitGuard, when set, reports whether the run already ended with a factory_exit_* call; a driver
	// with capability ExitGuard does not end a completed turn without one: it asks the runtime to exit,
	// up to ExitGuardRetries times (explicit-exits-authority D5).
	ExitGuard        func() bool
	ExitGuardRetries int
	// ExitFile is the MCP adapter's exit file behind ExitGuard (drivers whose guard runs in the runtime).
	ExitFile string
}

// ExitGuardPrompt is the follow-up turn of a runtime that ended its turn without an exit.
const ExitGuardPrompt = "Factory: your turn ended without a factory_exit_* call. End the run now with exactly one exit: factory_exit_submit (deliver for review), factory_exit_done (finished; merge first), factory_exit_handoff, factory_exit_ask, factory_exit_block or factory_exit_wait. Do not start new work."

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

// Result is terminal evidence. Status is COMPLETED, FAILED or CANCELLED; Err means there is no
// terminal evidence: the outcome is unknown unless OutcomeOf(Err) names it.
type Result struct {
	Status, Text string
	// Outcome refines a FAILED result when the runtime said why (OutcomeMaxTurns, OutcomePausedOut).
	Outcome string
	Err     error
	// Auth is the runtime's authentication failure seen during the run, also when the result has terminal
	// evidence (a FAILED turn ending paused_out); nil otherwise. The node disables the driver on it.
	Auth error
}

// Run outcomes a driver may name (reliable-recovery D1; docs/runtime-protocol.md, Event.outcome).
const (
	OutcomeFailed    = "failed"
	OutcomeMaxTurns  = "max_turns"
	OutcomePausedOut = "paused_out"
)

// OutcomeError is an error whose run outcome is known although the runtime gave no terminal
// evidence (an auth failure is paused_out, a broken runtime stream failed).
type OutcomeError interface {
	error
	RunOutcome() string
}

// OutcomeOf is the known outcome of a runtime error, "" when it is unknown: the error's own
// RunOutcome, else paused_out when the runtime executable is missing or cannot be executed.
func OutcomeOf(err error) string {
	var o OutcomeError
	if errors.As(err, &o) {
		return o.RunOutcome()
	}
	var missing *exec.Error
	var path *fs.PathError
	if errors.As(err, &missing) || errors.As(err, &path) && path.Op == "fork/exec" {
		return OutcomePausedOut
	}
	return ""
}

// AuthFailure is a runtime error caused by the runtime's own authentication (logged out, expired or revoked
// login, refused API key). The run ends paused_out, and a node disables the driver until its next probe passes
// (runtime-drivers 3.2).
type AuthFailure interface {
	error
	AuthFailed() bool
}

// IsAuthFailure reports whether err is, or wraps, an AuthFailure.
func IsAuthFailure(err error) bool {
	var a AuthFailure
	return errors.As(err, &a) && a.AuthFailed()
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
