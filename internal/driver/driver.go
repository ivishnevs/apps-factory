// Package driver is the runtime boundary of the run executor: a Driver starts one runtime
// session per run and reports normalized events. Claims, leases, heartbeats, credentials and
// event publication stay in the executor (internal/worker).
package driver

import (
	"context"
	"encoding/json"

	"appfactory/internal/node"
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
}

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
)

type Event struct {
	Kind   EventKind
	Method string
	Data   json.RawMessage
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
