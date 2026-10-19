package runner

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

// TestNodeHealthAgainstCore runs a v2 node session against the real Core (no NATS needed): the catalog's
// NodeHooks arrive in the register answer and again only when they change; a fake disk-usage provider going
// low then critical gives one node.low_disk, one node.disk_critical and Core's pause; a failing driver probe and
// a failing stack check give one node.check_failed each; a stalled heartbeat is reported once; four restarts in
// 15 minutes give exactly one node.restart_loop.
func TestNodeHealthAgainstCore(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	hooks := protocol.NodeHooks{OnTaskClosed: []string{"true"},
		Checks: []protocol.NodeStackCheck{{Name: "simctl", Command: "xcrun simctl list", Hint: "sudo xcodebuild -runFirstLaunch", Labels: []string{"macos"}},
			{Name: "adb", Command: "adb devices", Labels: []string{"android"}}}}
	c := &core.Core{Store: s, Token: "test-token", NodeHooks: map[string]protocol.NodeHooks{"ios": hooks}}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "test-token"}

	dir := t.TempDir()
	var free atomic.Uint64
	free.Store(30 * node.GiB)
	n := v2Node(t, api, dir)
	n.Config.Hooks = "ios"
	n.Config.Disk.LowGiB, n.Config.Disk.CriticalGiB = 20, 5
	var ran atomic.Int32
	n.Host.FreeBytes = func(string) (uint64, error) { return free.Load(), nil }
	n.Host.Run = func(_ context.Context, name string, args ...string) (string, string, error) {
		if name == "/bin/sh" && args[1] == "xcrun simctl list" {
			ran.Add(1)
			return "", "xcrun: error: unable to find utility \"simctl\"", errors.New("exit status 72")
		}
		if name == "git" || name == "ps" { // git and process start times are fine
			return "ok", "", nil
		}
		return "", "", errors.New("unexpected command " + name)
	}
	n.Host.LookPath = func(string) (string, error) { return "", errors.New("not found") }
	n.Drivers = []driver.Driver{&fake.Driver{Checks: []node.CheckResult{{Check: "codex.auth", Status: node.Fail, Detail: "not logged in"}}}}
	sess, err := newSession(ctx, n, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sess.hooks == nil || sess.hooks.Name != "ios" || sess.hooks.Hash != hooks.Hash() || len(sess.hooks.Spec.Checks) != 2 {
		t.Fatalf("register answer hooks: %+v", sess.hooks)
	}
	applied := 0
	sess.onHooks = func(protocol.NodeHooksDelivery) { applied++ }
	checks := newSelfChecks(n.Host, n.Config.Labels)
	checks.setStack(sess.hooks.Spec.Checks)
	checks.check(ctx)
	sess.checks = checks.failed
	if ran.Load() != 1 {
		t.Fatalf("stack check ran %d times (android check must be skipped)", ran.Load())
	}

	events := func(kind string) []storage.FeedItem {
		t.Helper()
		page, err := s.Feed(ctx, storage.DefaultCompany, storage.FeedFilter{}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []storage.FeedItem
		for _, item := range page.Items {
			if item.Type == kind {
				out = append(out, item)
			}
		}
		return out
	}
	for _, gib := range []uint64{30, 10, 10, 3, 3} {
		free.Store(gib * node.GiB)
		if err = sess.beat(ctx); err != nil {
			t.Fatal(err)
		}
		if paused := !sess.claimable(); paused != (gib == 3) {
			t.Fatalf("free %d GiB: paused %v", gib, paused)
		}
	}
	if low, crit := events("node.low_disk"), events("node.disk_critical"); len(low) != 1 || !low[0].Attention || len(crit) != 1 {
		t.Fatalf("low %+v critical %+v", low, crit)
	}
	// One alert per failure: the failing driver probe is driver_unsupported (with its check and detail), not a
	// node.check_failed; the failing stack check is node.check_failed.
	failed := events("node.check_failed")
	if len(failed) != 1 || failed[0].Ref != "stack.simctl" || !strings.Contains(failed[0].Summary, "unable to find utility") {
		t.Fatalf("%+v", failed)
	}
	if d := events("driver_unsupported"); len(d) != 1 || !d[0].Attention || !strings.Contains(d[0].Summary, "probe check failed: codex.auth: not logged in") {
		t.Fatalf("%+v", d)
	}
	if applied != 0 {
		t.Fatal("unchanged hooks applied again")
	}
	// A changed catalog document reaches the node with the next heartbeat.
	hooks.Prune = []protocol.NodePrune{{Command: "rm -rf ~/Library/Developer/Xcode/DerivedData", OlderThan: "72h"}}
	c.NodeHooks["ios"] = hooks
	if err = sess.beat(ctx); err != nil || applied != 1 || sess.hooks.Hash != hooks.Hash() || len(sess.hooks.Spec.Prune) != 1 {
		t.Fatal(err, applied, sess.hooks)
	}
	if err = sess.beat(ctx); err != nil || applied != 1 {
		t.Fatal("hooks delivered again", err, applied)
	}
	// Heartbeats failed for more than StallReport: reported once on reconnect.
	sess.lastBeat = time.Now().Add(-StallReport - time.Minute)
	for range 2 {
		if err = sess.beat(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := events("node.heartbeat_stalled"); len(got) != 1 || !got[0].Attention {
		t.Fatalf("%+v", got)
	}
	// Four restarts within 15 minutes: exactly one restart-loop alert.
	for range 4 {
		if _, err = newSession(ctx, n, nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := events("node.restart_loop"); len(got) != 1 || !got[0].Attention {
		t.Fatalf("%+v", got)
	}
	if got := events("node.registered"); len(got) != 5 {
		t.Fatalf("%d registrations", len(got))
	}
}
