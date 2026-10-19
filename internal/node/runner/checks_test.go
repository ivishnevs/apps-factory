package runner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"appfactory/internal/node"
	"appfactory/internal/protocol"
)

// TestSelfChecksBlockOnlyOnGit: a missing git pauses claiming until it is back; a failing stack check is
// reported in health.checks but does not pause the node.
func TestSelfChecksBlockOnlyOnGit(t *testing.T) {
	var gitOK atomic.Bool
	h := node.Host{LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Run: func(_ context.Context, name string, args ...string) (string, string, error) {
			switch {
			case name == "git" && !gitOK.Load():
				return "", "", errors.New("executable file not found")
			case name == "/bin/sh":
				return "", "boom", errors.New("exit status 1")
			}
			return "ok", "", nil
		}}
	c := newSelfChecks(h, nil)
	c.setStack([]protocol.NodeStackCheck{{Name: "simctl", Command: "xcrun simctl list"}})
	ctx := context.Background()
	c.check(ctx)
	if !c.blocked() || len(c.failed()) != 2 || c.failed()[0].Check != "git" || c.failed()[1].Check != "stack.simctl" {
		t.Fatalf("blocked %v failed %+v", c.blocked(), c.failed())
	}
	gitOK.Store(true)
	c.check(ctx)
	if c.blocked() || len(c.failed()) != 1 {
		t.Fatalf("blocked %v failed %+v", c.blocked(), c.failed())
	}
}
