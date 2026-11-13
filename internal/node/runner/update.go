package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/node/runtimes"
	"appfactory/internal/node/selfupdate"
	"appfactory/internal/protocol"
)

// Version updates, node side (node-packaging-updates design C, tasks 2.2 and 3.4). Core offers the release in its
// poll and heartbeat answers; the node
//  1. downloads the package for its platform into versions/<v>, checks its sha256 and fetches the runtimes the new
//     locks name (reusing the running version's when the pin is the same);
//  2. runs the new version's self-test (`versions/<v>/bin/factory-node selftest`); a failure aborts and is reported;
//  3. drains: claims nothing and waits for its live runs to end, without a limit, unless the owner chose "update now";
//  4. switches current to versions/<v> and stops with ErrRestart: launchd or systemd starts the new version;
//  5. the new version must register with a usable driver within selfupdate.HealthWindow, else it switches back and
//     the previous version reports update_failed (selfupdate.Check, Rollback).

// ErrRestart: the node switched to a new version (or rolled one back) and stops for its service to start it.
var ErrRestart = errors.New("restarting into another node version")

// RestartExitCode is the exit status of a node stopping with ErrRestart; launchd (KeepAlive) and systemd
// (Restart=always) start it again.
const RestartExitCode = 75

// ServiceEnv names the service manager that runs the node (launchd, systemd): the units factory-node launchd and
// systemd print set it. Without it the node cannot rely on being restarted, so it does not update itself.
const ServiceEnv = "FACTORY_NODE_SERVICE"

// Updater updates the node to the release Core offers.
type Updater struct {
	// Home is the node's home of the versioned layout (selfupdate.Locate); empty: the node was installed before it (or
	// runs from a checkout) and cannot switch versions.
	Home string
	// Version is the running version (its directory under versions/).
	Version string
	// Supervised: launchd or systemd restarts the node after the switch (ServiceEnv).
	Supervised bool
	// Config is node.yaml's path for the self-test ("": the default lookup).
	Config string
	// Platform is the package platform, darwin-arm64 ...; Core picks the package by it.
	Platform string
	// Boot is this start's pending health check after a switch (selfupdate.Check).
	Boot selfupdate.Boot
	// Client downloads the package; nil uses a client with a 30-minute timeout.
	Client *http.Client
	// SelfTest and Runtimes override the self-test of a version directory and the fetching of its runtimes (tests).
	SelfTest func(ctx context.Context, dir string) error
	Runtimes func(ctx context.Context, dir, prev string) error
	Now      func() time.Time

	mu       sync.Mutex
	status   protocol.NodeUpdateStatus
	failed   map[string]bool // version|attempt that failed: never tried again
	cancel   context.CancelFunc
	target   string // version|attempt in progress
	draining atomic.Bool
	now      atomic.Bool

	// Wired by Run: base is Core's URL (relative package URLs), busy the live runs, restart stops the node with
	// ErrRestart, kick sends a heartbeat now (it carries the status).
	base    string
	busy    func() int
	restart func()
	kick    func()
}

func (u *Updater) clock() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func key(v string, attempt int64) string { return fmt.Sprintf("%s|%d", v, attempt) }

// Start reads update.json: a version that failed (rolled back, or failed before its switch) is reported and never
// tried again for the same attempt.
func (u *Updater) start() {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.failed == nil {
		u.failed = map[string]bool{}
	}
	if u.Home == "" {
		return
	}
	s, err := selfupdate.Load(u.Home)
	if err != nil {
		slog.Warn("update state unreadable", "error", err)
		return
	}
	if st := s.Status(); st.State != "" {
		u.status = st
		u.failed[key(st.Version, st.Attempt)] = true
	}
}

// Status is the update report of register and heartbeat; nil when there is nothing to report.
func (u *Updater) Status() *protocol.NodeUpdateStatus {
	if u == nil {
		return nil
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.status.State == "" {
		return nil
	}
	s := u.status
	if s.State == protocol.UpdateDraining && u.busy != nil {
		s.Runs = u.busy()
	}
	return &s
}

// Draining: the node claims nothing while it waits for its runs to end before the switch.
func (u *Updater) Draining() bool { return u != nil && u.draining.Load() }

func (u *Updater) set(s protocol.NodeUpdateStatus) {
	if s.State != "" {
		s.At = u.clock().Unix()
	}
	u.mu.Lock()
	u.status = s
	u.mu.Unlock()
	if u.kick != nil {
		u.kick()
	}
}

// Offer handles the update of a poll or heartbeat answer (nil: none). A heartbeat answer without one ends an attempt
// that has not switched yet (the owner withdrew the approval, another release came): the node claims again.
func (u *Updater) Offer(ctx context.Context, up *protocol.NodeUpdate, heartbeat bool) {
	if u == nil {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if up == nil {
		if heartbeat && u.cancel != nil && u.status.State != protocol.UpdateSwitching {
			slog.Info("update withdrawn by Core; claiming again", "version", u.status.Version)
			u.cancel()
		}
		return
	}
	if up.Version == u.Version || !protocol.ValidNodeVersion(up.Version) {
		return
	}
	k := key(up.Version, up.Attempt)
	if u.failed[k] {
		return
	}
	if u.cancel != nil {
		// "Update now" of the version in progress (an approval may carry a new attempt of it).
		if strings.HasPrefix(u.target, up.Version+"|") && up.Now && !u.now.Swap(true) {
			slog.Info("update now: the drain stops waiting for live runs", "version", up.Version)
		}
		return // one update at a time
	}
	fail := func(reason string) {
		u.failed[k] = true
		u.status = protocol.NodeUpdateStatus{State: protocol.UpdateFailed, Version: up.Version, From: u.Version, Attempt: up.Attempt, Reason: reason, At: u.clock().Unix()}
		slog.Warn("node update refused", "version", up.Version, "reason", reason)
		if u.kick != nil {
			go u.kick()
		}
	}
	switch {
	case u.Home == "":
		fail("this node was installed before updates from the web: reinstall it once with the install command on the Nodes page")
		return
	case !u.Supervised:
		fail("this node does not run as a service (launchd or systemd), so nothing would start the new version: reinstall it with the install command, or update by hand")
		return
	}
	actx, cancel := context.WithCancel(ctx)
	u.cancel, u.target = cancel, k
	u.now.Store(up.Now)
	go u.run(actx, *up)
}

// run is one update attempt; it ends with the switch (ErrRestart), a failure, or ctx (withdrawn, the node stops).
func (u *Updater) run(ctx context.Context, up protocol.NodeUpdate) {
	status := protocol.NodeUpdateStatus{Version: up.Version, From: u.Version, Attempt: up.Attempt}
	step := func(state string) {
		status.State = state
		u.set(status)
		slog.Info("node update", "state", state, "version", up.Version)
	}
	dir := selfupdate.Dir(u.Home, up.Version)
	err := func() error {
		step(protocol.UpdateDownloading)
		if err := u.prepare(ctx, up); err != nil {
			return err
		}
		step(protocol.UpdateTesting)
		if err := u.selfTest(ctx, dir); err != nil {
			return err
		}
		step(protocol.UpdateDraining)
		u.draining.Store(true)
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			n := 0
			if u.busy != nil {
				n = u.busy()
			}
			if n == 0 {
				break
			}
			if u.now.Load() {
				slog.Info("update now: interrupting live runs; they are queued again", "runs", n)
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
			}
		}
		step(protocol.UpdateSwitching)
		return selfupdate.Begin(u.Home, u.Version, up.Version, up.Attempt, u.clock())
	}()
	u.mu.Lock()
	u.cancel, u.target = nil, ""
	u.mu.Unlock()
	switch {
	case err == nil:
		slog.Info("switched to the new version; restarting", "from", u.Version, "to", up.Version)
		if u.restart != nil {
			u.restart()
		}
		return
	case selfupdate.Current(u.Home) != up.Version:
		_ = os.RemoveAll(dir) // what this attempt unpacked
	}
	u.draining.Store(false)
	if ctx.Err() != nil {
		u.set(protocol.NodeUpdateStatus{})
		return
	}
	reason := err.Error()
	u.mu.Lock()
	u.failed[key(up.Version, up.Attempt)] = true
	u.mu.Unlock()
	status.State, status.Reason = protocol.UpdateFailed, reason
	if serr := selfupdate.Save(u.Home, selfupdate.State{Phase: selfupdate.PhaseFailed, From: u.Version, To: up.Version, Attempt: up.Attempt, Reason: reason, FailedAt: u.clock().Unix()}); serr != nil {
		slog.Warn("keeping the failed update in update.json", "error", serr)
	}
	slog.Error("node update failed", "version", up.Version, "reason", reason)
	u.set(status)
}

// prepare downloads and verifies the package, unpacks it into versions/<v> and fetches its runtimes.
func (u *Updater) prepare(ctx context.Context, up protocol.NodeUpdate) error {
	src, err := u.packageURL(up.URL)
	if err != nil {
		return err
	}
	if len(up.SHA256) != 64 {
		return errors.New("Core sent no sha256 for the package")
	}
	if err = os.MkdirAll(filepath.Join(u.Home, selfupdate.VersionsDir), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Join(u.Home, selfupdate.VersionsDir), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		tmp.Close()
		return err
	}
	client := u.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("downloading the package: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		tmp.Close()
		return fmt.Errorf("downloading the package: %s", resp.Status)
	}
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(tmp, h), resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading the package: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, up.SHA256) {
		return fmt.Errorf("the package does not match its sha256 (got %s)", got)
	}
	dir, err := selfupdate.Unpack(tmp.Name(), u.Home, up.Version)
	if err != nil {
		return err
	}
	fetch := u.Runtimes
	if fetch == nil {
		fetch = FetchRuntimes
	}
	if err = fetch(ctx, dir, selfupdate.Dir(u.Home, u.Version)); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		// The Claude shim's executable carries its script; sign it for this Mac (ad hoc), as install.sh does.
		if shimExe := filepath.Join(dir, "bin", "factory-claude"); fileExists(shimExe) {
			if out, err := exec.CommandContext(ctx, "codesign", "--sign", "-", "--force", shimExe).CombinedOutput(); err != nil {
				slog.Warn("could not sign factory-claude", "error", err, "output", strings.TrimSpace(string(out)))
			}
		}
	}
	return nil
}

// packageURL resolves the package's URL against Core's (Core sends a path of its own downloads).
func (u *Updater) packageURL(raw string) (string, error) {
	ref, err := url.Parse(raw)
	if err != nil || raw == "" {
		return "", fmt.Errorf("invalid package URL %q", raw)
	}
	if !ref.IsAbs() {
		base, err := url.Parse(strings.TrimRight(u.base, "/") + "/")
		if err != nil || u.base == "" {
			return "", fmt.Errorf("no Core URL to download %s from", raw)
		}
		ref = base.ResolveReference(&url.URL{Path: strings.TrimPrefix(ref.Path, "/")})
	}
	if ref.Scheme != "https" && ref.Scheme != "http" {
		return "", fmt.Errorf("invalid package URL %q", raw)
	}
	return ref.String(), nil
}

// FetchRuntimes installs the runtimes the new version's locks pin. A runtime the running version has must install;
// one it lacks (no pin for this machine, never installed) stays off as before. The running version calls it after
// unpacking, and the new version's selftest calls it again with its own code: a lock may pin a file the running
// version does not know (codex-code-mode-host), which only the new version installs.
func FetchRuntimes(ctx context.Context, dir, prev string) error {
	in := runtimes.Installer{}
	for _, rt := range []struct {
		bin     string
		install func() (string, error)
	}{
		{runtimes.ClaudeBin, func() (string, error) { return in.Claude(ctx, dir, prev, runtimes.Libc()) }},
		{runtimes.CodexBin, func() (string, error) { return in.Codex(ctx, dir, prev, runtimes.Platform()) }},
	} {
		msg, err := rt.install()
		if err == nil {
			slog.Info("runtime for the new version", "runtime", rt.bin, "detail", msg)
			continue
		}
		if runtimes.Binary(prev, rt.bin) != "" {
			return fmt.Errorf("fetching %s for the new version: %w", rt.bin, err)
		}
		slog.Info("runtime not installed for the new version", "runtime", rt.bin, "reason", err)
	}
	return nil
}

// selfTest runs the new version's `selftest` from its own directory.
func (u *Updater) selfTest(ctx context.Context, dir string) error {
	if u.SelfTest != nil {
		return u.SelfTest(ctx, dir)
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	args := []string{"selftest", "-json", "-bin-dir", filepath.Join(dir, "bin")}
	if u.Config != "" {
		args = append(args, "-config", u.Config)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, filepath.Join(dir, "bin", "factory-node"), args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err == nil {
		return nil
	}
	var results []node.CheckResult
	if json.Unmarshal(stdout.Bytes(), &results) == nil {
		var bad []string
		for _, r := range results {
			if r.Status == node.Fail {
				bad = append(bad, r.Check+": "+r.Detail)
			}
		}
		if len(bad) > 0 {
			return errors.New("self-test failed: " + strings.Join(bad, "; "))
		}
	}
	tail := strings.TrimSpace(stderr.String())
	if len(tail) > 500 {
		tail = tail[len(tail)-500:]
	}
	return fmt.Errorf("self-test failed: %v %s", err, tail)
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

// healthCheck confirms a pending new version once it registered with a usable driver, and rolls it back when that
// did not happen by the deadline (selfupdate.HealthWindow after the switch).
func (u *Updater) healthCheck(ctx context.Context, healthy <-chan struct{}) {
	if u == nil || !u.Boot.Pending || u.Home == "" {
		return
	}
	t := time.NewTimer(time.Until(u.Boot.Deadline))
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-healthy:
		if err := selfupdate.Confirm(u.Home, u.Version); err != nil {
			slog.Error("confirming the new version", "error", err)
			return
		}
		slog.Info("new version healthy", "version", u.Version)
	case <-t.C:
		b, err := selfupdate.Rollback(u.Home, fmt.Sprintf("version %s did not register and pass its checks within %s", u.Version, selfupdate.HealthWindow), u.clock())
		if err != nil {
			slog.Error("rolling back", "error", err)
			return
		}
		slog.Error("new version unhealthy; rolled back", "reason", b.Reason)
		if u.restart != nil {
			u.restart()
		}
	}
}
