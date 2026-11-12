package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/node/selfupdate"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
)

// buildNodeBin builds factory-node into bin and links factory-mcp and factory-shim to it, as the package does
// (node-packaging-updates 3.3).
func buildNodeBin(t *testing.T, bin string) {
	t.Helper()
	if out, err := exec.Command("go", "build", "-o", filepath.Join(bin, "factory-node"), "appfactory/cmd/factory-node").CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, name := range []string{"factory-mcp", "factory-shim"} {
		if err := os.Symlink("factory-node", filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// httpBytes serves b at every path.
func httpBytes(b []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(b) })
}

// nodePackage is a node package of version v as scripts/package-node.sh lays it out (the binary is a stand-in).
func nodePackage(t *testing.T, v string) []byte {
	t.Helper()
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	add := func(h *tar.Header, body string) {
		h.Size = int64(len(body))
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		_, _ = tw.Write([]byte(body))
	}
	add(&tar.Header{Name: "VERSION", Mode: 0o644, Typeflag: tar.TypeReg}, v+"\n")
	add(&tar.Header{Name: "bin/factory-node", Mode: 0o755, Typeflag: tar.TypeReg}, "#!/bin/sh\necho "+v+"\n")
	add(&tar.Header{Name: "bin/factory-shim", Typeflag: tar.TypeSymlink, Linkname: "factory-node"}, "")
	add(&tar.Header{Name: "bin/factory-mcp", Typeflag: tar.TypeSymlink, Linkname: "factory-node"}, "")
	_ = tw.Close()
	_ = gz.Close()
	return b.Bytes()
}

// installed is a node home with version v installed as install.sh lays it out (versions/<v>, current, bin).
func installed(t *testing.T, v string) string {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(t.TempDir(), "p.tgz")
	if err := os.WriteFile(p, nodePackage(t, v), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := selfupdate.Unpack(p, home, v); err != nil {
		t.Fatal(err)
	}
	if err := selfupdate.Switch(home, v); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(selfupdate.CurrentLink, "bin"), filepath.Join(home, "bin")); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestNodeUpdateRealCore (node-packaging-updates 2.2, 3.4) updates a node against the real Core. Under the approval
// policy the node behind the release is asked about (node_update_available) and gets nothing; approved, it downloads
// the package Core hands out, and a package that does not match its sha256 fails the attempt (node_update_failed),
// which is not tried again. Retried, it passes the self-test and drains: with a live run it claims nothing new, and
// when the run ends it switches current and stops for the service to restart it. The new version's start registers,
// passes its health check (update.json done), Core records node_updated and the queued run runs on it.
func TestNodeUpdateRealCore(t *testing.T) {
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../../..")
	if err != nil {
		t.Fatal(err)
	}
	downloads := t.TempDir()
	c := &core.Core{Store: s, Agents: agents, Token: "test-token", NodeDownloads: downloads}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	go c.Run(ctx)
	co := storage.DefaultCompany
	const oldV, newV, platform = "2026.11.12.1500", "2026.11.13.0900+abc1234", "linux-arm64"
	release := func(sum string) {
		t.Helper()
		pkg := nodePackage(t, newV)
		if sum == "" {
			h := sha256.Sum256(pkg)
			sum = hex.EncodeToString(h[:])
		}
		name := "factory-node-" + platform + ".tar.gz"
		if err := os.WriteFile(filepath.Join(downloads, name), pkg, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(downloads, "SHA256SUMS"), []byte(sum+"  "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(downloads, "VERSION"), []byte(newV+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	create := func(id string) {
		t.Helper()
		spec := protocol.RunSpec{ID: id, Project: id, Agent: id, Provider: protocol.RuntimeCodex, Workspace: t.TempDir(), Prompt: "work", TimeoutSeconds: 120}
		if err := s.Create(ctx, co, spec); err != nil {
			t.Fatal(err)
		}
	}
	status := func(id string) string {
		r, _ := s.Get(ctx, co, id)
		return r.Status
	}
	home := installed(t, oldV)
	cfg := node.Defaults()
	cfg.Slots, cfg.ConversationSlots, cfg.StateDir = 1, 0, t.TempDir()
	api := enrolled(t, s, cfg.StateDir, server.URL)
	var tested []string
	start := func(v string, boot selfupdate.Boot) (*Updater, chan error) {
		up := &Updater{Home: home, Version: v, Supervised: true, Platform: platform, Boot: boot,
			SelfTest: func(_ context.Context, dir string) error { tested = append(tested, filepath.Base(dir)); return nil },
			Runtimes: func(context.Context, string, string) error { return nil }}
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, Node{Config: cfg, API: api, Drivers: []driver.Driver{perRunSessions{&fake.Driver{}}}, Detected: []string{"linux"}, Version: v, Update: up})
		}()
		return up, done
	}
	up, done := start(oldV, selfupdate.Boot{})
	wait := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case err := <-done:
				t.Fatal("node stopped:", err)
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what)
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	nodeOf := func() storage.Node {
		ns, _ := s.Nodes(ctx, co)
		if len(ns) != 1 {
			return storage.Node{}
		}
		return ns[0]
	}
	feedHas := func(kind, text string) bool {
		for after := int64(0); ; {
			page, err := s.Feed(ctx, co, storage.FeedFilter{}, after, 100)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range page.Items {
				if f.Type == kind && strings.Contains(f.Summary, text) {
					return true
				}
			}
			if !page.HasMore {
				return false
			}
			after = page.NextAfter
		}
	}
	wait("register", func() bool { n := nodeOf(); return n.Epoch == 1 && n.Platform == platform })
	id := nodeOf().NodeID
	create("a")
	wait("a runs", func() bool { return status("a") == "RUNNING" })

	// Approval policy: asked once, nothing offered.
	if err = s.SetNodeUpdatePolicy(ctx, co, protocol.NodeUpdateApprove, "owner", "Ilya"); err != nil {
		t.Fatal(err)
	}
	release(strings.Repeat("0", 64))
	wait("asked", func() bool { return feedHas("node_update_available", newV) })
	time.Sleep(1200 * time.Millisecond)
	if st := up.Status(); st != nil {
		t.Fatalf("an update started without approval: %+v", st)
	}
	// Approved: the package does not match its sha256.
	if _, err = s.ApproveNodeUpdate(ctx, co, id, newV, false, "owner", "Ilya"); err != nil {
		t.Fatal(err)
	}
	wait("failed", func() bool { n := nodeOf(); return n.Update != nil && n.Update.State == protocol.UpdateFailed })
	if n := nodeOf(); !strings.Contains(n.Update.Reason, "sha256") || n.Update.Version != newV || !feedHas("node_update_failed", "sha256") {
		t.Fatalf("%+v", n.Update)
	}
	if _, wait := storage.DecideNodeUpdate(nodeOf(), protocol.NodeUpdateApprove, newV); wait != storage.UpdateWaitFailed {
		t.Fatal("a failed attempt offered again:", wait)
	}
	if _, err = os.Stat(selfupdate.Dir(home, newV)); err == nil {
		t.Fatal("the failed attempt left its version directory")
	}

	// Retried with the right package: self-test, then the drain waits for run a and takes no new run.
	release("")
	if _, err = s.ApproveNodeUpdate(ctx, co, id, newV, false, "owner", "Ilya"); err != nil {
		t.Fatal(err)
	}
	wait("draining", func() bool {
		n := nodeOf()
		return n.Update != nil && n.Update.State == protocol.UpdateDraining && n.Update.Runs == 1
	})
	if len(tested) != 1 || tested[0] != newV {
		t.Fatal("self-test", tested)
	}
	create("b")
	time.Sleep(1500 * time.Millisecond)
	if st := status("b"); st != "QUEUED" {
		t.Fatal("a draining node claimed", st)
	}
	if selfupdate.Current(home) != oldV {
		t.Fatal("switched while a run was live")
	}
	if err = s.Cancel(ctx, co, "a"); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
	case <-ctx.Done():
		t.Fatal("no switch")
	}
	if !errors.Is(err, ErrRestart) || selfupdate.Current(home) != newV {
		t.Fatal(err, selfupdate.Current(home))
	}
	if st, _ := selfupdate.Load(home); st.Phase != selfupdate.PhaseSwitched || st.From != oldV || st.To != newV {
		t.Fatalf("%+v", st)
	}

	// The service starts the new version: pending health check, registered, confirmed.
	boot, err := selfupdate.Check(home, newV, time.Now())
	if err != nil || !boot.Pending {
		t.Fatal(boot, err)
	}
	_, done = start(newV, boot)
	wait("new version registered", func() bool { n := nodeOf(); return n.Version == newV && n.Epoch == 2 })
	wait("confirmed", func() bool { st, _ := selfupdate.Load(home); return st.Phase == selfupdate.PhaseDone })
	wait("b runs on the new version", func() bool { return status("b") == "RUNNING" })
	if !feedHas("node_updated", fmt.Sprintf("runs version %s (was %s)", newV, oldV)) || !feedHas("node_update_started", newV) {
		t.Fatal("feed")
	}
	if n := nodeOf(); n.Update != nil {
		t.Fatalf("an update still reported: %+v", n.Update)
	}
	if offer, _ := storage.DecideNodeUpdate(nodeOf(), protocol.NodeUpdateAuto, newV); offer != nil {
		t.Fatal("the updated node is offered its own version")
	}
	cancel()
	<-done
}

// TestNodeUpdateHealthDeadline (node-packaging-updates 3.4): a new version that has not registered with a usable driver
// by its deadline switches current back to the previous version and restarts.
func TestNodeUpdateHealthDeadline(t *testing.T) {
	home := installed(t, "1.0.1")
	p := filepath.Join(t.TempDir(), "p.tgz")
	if err := os.WriteFile(p, nodePackage(t, "1.0.2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := selfupdate.Unpack(p, home, "1.0.2"); err != nil {
		t.Fatal(err)
	}
	if err := selfupdate.Begin(home, "1.0.1", "1.0.2", 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	restarted := make(chan struct{}, 1)
	up := &Updater{Home: home, Version: "1.0.2", Boot: selfupdate.Boot{Pending: true, Deadline: time.Now().Add(200 * time.Millisecond)}}
	up.restart = func() { restarted <- struct{}{} }
	go up.healthCheck(context.Background(), make(chan struct{}))
	select {
	case <-restarted:
	case <-time.After(10 * time.Second):
		t.Fatal("no rollback")
	}
	st, _ := selfupdate.Load(home)
	if selfupdate.Current(home) != "1.0.1" || st.Phase != selfupdate.PhaseFailed || !strings.Contains(st.Reason, "did not register") {
		t.Fatalf("%s %+v", selfupdate.Current(home), st)
	}
}

// TestNodeUpdateNowAndWithdraw (node-packaging-updates 3.4, owner decision 2): the drain waits for live runs without a
// limit; "update now" stops waiting (the runs are interrupted by the restart); an update withdrawn before the switch
// (a heartbeat answer without it) ends the drain and the node claims again. A node installed before the versioned
// layout or not run by a service refuses updates with the reason.
func TestNodeUpdateNowAndWithdraw(t *testing.T) {
	const oldV, newV = "1.0.1", "1.0.2"
	pkg := nodePackage(t, newV)
	sum := sha256.Sum256(pkg)
	srv := httptest.NewServer(httpBytes(pkg))
	defer srv.Close()
	offer := &protocol.NodeUpdate{Version: newV, URL: "/downloads/node/factory-node-linux-arm64.tar.gz", SHA256: hex.EncodeToString(sum[:])}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	busy := 2
	restarted := make(chan struct{}, 1)
	newUpdater := func() *Updater {
		up := &Updater{Home: installed(t, oldV), Version: oldV, Supervised: true,
			SelfTest: func(context.Context, string) error { return nil }, Runtimes: func(context.Context, string, string) error { return nil }}
		up.start()
		up.base, up.busy, up.restart, up.kick = srv.URL, func() int { return busy }, func() { restarted <- struct{}{} }, func() {}
		return up
	}
	until := func(what string, ok func() bool) {
		t.Helper()
		for !ok() {
			select {
			case <-ctx.Done():
				t.Fatal("timeout waiting for", what)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	state := func(up *Updater) string {
		if s := up.Status(); s != nil {
			return s.State
		}
		return ""
	}

	up := newUpdater()
	up.Offer(ctx, offer, false)
	until("draining", func() bool { return state(up) == protocol.UpdateDraining && up.Draining() })
	if up.Status().Runs != 2 {
		t.Fatalf("%+v", up.Status())
	}
	time.Sleep(1200 * time.Millisecond)
	select {
	case <-restarted:
		t.Fatal("switched with live runs")
	default:
	}
	now := *offer
	now.Now, now.Attempt = true, 99 // the owner's "update now" approves a new attempt of the same version
	up.Offer(ctx, &now, false)
	select {
	case <-restarted:
	case <-ctx.Done():
		t.Fatal("update now did not switch")
	}
	if selfupdate.Current(up.Home) != newV {
		t.Fatal("not switched")
	}

	// Withdrawn by Core while draining: claims again, the unpacked version is removed.
	up = newUpdater()
	up.Offer(ctx, offer, false)
	until("draining", func() bool { return up.Draining() })
	up.Offer(ctx, nil, false) // a poll answer without the update says nothing
	if !up.Draining() {
		t.Fatal("a poll answer withdrew the update")
	}
	up.Offer(ctx, nil, true)
	until("withdrawn", func() bool { return !up.Draining() && up.Status() == nil })
	if _, err := os.Stat(selfupdate.Dir(up.Home, newV)); err == nil {
		t.Fatal("the withdrawn version kept")
	}

	// Refusals with the reason; the same attempt is not tried again.
	for _, c := range []struct {
		up   *Updater
		want string
	}{{&Updater{Version: oldV, Supervised: true}, "reinstall it once"}, {&Updater{Home: t.TempDir(), Version: oldV}, "does not run as a service"}} {
		c.up.start()
		c.up.Offer(ctx, offer, false)
		if st := c.up.Status(); st == nil || st.State != protocol.UpdateFailed || !strings.Contains(st.Reason, c.want) {
			t.Fatalf("%+v", st)
		}
	}
}
