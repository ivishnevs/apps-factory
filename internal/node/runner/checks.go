package runner

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// selfChecks runs the node's self-checks (node-agent D7: git and forge credentials, process start times, the
// stack checks of the catalog's NodeHooks) at start, every interval and when the stack checks change. The
// failing ones go into the heartbeat's health.checks, where Core turns each new failure into one
// node.check_failed{check} event.
type selfChecks struct {
	doctor node.Doctor // Host and Labels; Stack is set by setStack
	kick   chan struct{}

	mu      sync.Mutex
	stack   []protocol.NodeStackCheck
	failing []node.CheckResult
}

func newSelfChecks(h node.Host, labels []string) *selfChecks {
	return &selfChecks{doctor: node.Doctor{Host: h, Labels: labels}, kick: make(chan struct{}, 1), failing: []node.CheckResult{}}
}

// setStack replaces the stack checks and re-runs the checks.
func (c *selfChecks) setStack(stack []protocol.NodeStackCheck) {
	c.mu.Lock()
	c.stack = stack
	c.mu.Unlock()
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// failed is the last pass's failing checks.
func (c *selfChecks) failed() []node.CheckResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]node.CheckResult{}, c.failing...)
}

// blockingChecks prevent all work when they fail (no usable git): the node stops claiming until they pass
// again (node-agent D7). Other failures (git.user, forge.auth, stack checks) are reported only.
var blockingChecks = map[string]bool{"git": true}

// blocked reports whether a failing check prevents all work.
func (c *selfChecks) blocked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.failing {
		if blockingChecks[r.Check] {
			return true
		}
	}
	return false
}

// check runs one pass.
func (c *selfChecks) check(ctx context.Context) {
	c.mu.Lock()
	d := c.doctor
	d.Stack = c.stack
	c.mu.Unlock()
	failing := []node.CheckResult{}
	for _, r := range d.SelfChecks(ctx) {
		if r.Status == node.Fail {
			failing = append(failing, r)
		}
	}
	if ctx.Err() != nil {
		return
	}
	c.mu.Lock()
	was := map[string]bool{}
	for _, r := range c.failing {
		was[r.Check] = true
	}
	c.failing = failing
	c.mu.Unlock()
	for _, r := range failing {
		if !was[r.Check] {
			slog.Error("self-check failed", "check", r.Check, "detail", r.Detail, "hint", r.Hint)
		}
		delete(was, r.Check)
	}
	for check := range was {
		slog.Info("self-check passes again", "check", check)
	}
}

// run checks at once, then every interval and after setStack, until ctx ends.
func (c *selfChecks) run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		c.check(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.kick:
		}
	}
}
