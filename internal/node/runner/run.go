package runner

import (
	"context"
	"crypto/ecdh"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"appfactory/internal/driver"
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"appfactory/internal/node/janitor"
	"appfactory/internal/node/sessions"
	"appfactory/internal/protocol"
	"appfactory/internal/worker"
	"appfactory/internal/workspace"
)

// Drivers builds the enabled drivers of c. getenv supplies CODEX_BIN.
func Drivers(c node.Config, getenv func(string) string) []driver.Driver {
	return buildDrivers(c, getenv, true)
}

// OptionalDrivers builds the drivers node.yaml leaves off that policy.runtimes allows: the settings from the web may
// turn them on (node-packaging-updates B).
func OptionalDrivers(c node.Config, getenv func(string) string) []driver.Driver {
	return buildDrivers(c, getenv, false)
}

func buildDrivers(c node.Config, getenv func(string) string, on bool) []driver.Driver {
	var r []driver.Driver
	allowed := func(rt string) bool { return len(c.Policy.Runtimes) == 0 || slices.Contains(c.Policy.Runtimes, rt) } // connector-trust: policy.runtimes
	if c.Drivers.Codex.On() == on && allowed("codex") {
		bin := c.Drivers.Codex.Binary
		if bin == "" {
			bin = getenv("CODEX_BIN")
		}
		if bin == "" {
			bin = "codex"
		}
		r = append(r, codex.Driver{Binary: bin, Home: node.ExpandHome(c.Drivers.Codex.Home), AccountsDir: node.AccountsDir(c.StateDir), Sandbox: c.Sandbox, FullAccess: c.Policy.FullAccess, Net: node.SystemNetwork()})
	}
	if cc := c.Drivers.Claude; cc.On() == on && allowed("claude") {
		shim := cc.Shim
		if shim == "" && c.BinDir != "" {
			shim = claude.DefaultShimFor(c.BinDir, func(p string) bool { _, err := os.Stat(p); return err == nil })
		}
		r = append(r, claude.Driver{Node: cc.Node, Shim: shim, Executable: cc.Executable, AllowAPIKey: cc.AllowAPIKey, BinDir: c.BinDir, ConfigDir: node.ExpandHome(cc.ConfigDir), AccountsDir: node.AccountsDir(c.StateDir), Sandbox: c.Sandbox, StateDir: c.StateDir, FullAccess: c.Policy.FullAccess, Net: node.SystemNetwork()})
	}
	return r
}

// Main checks the environment (the node's keys from enrollment, FACTORY_URL) and runs the node until ctx ends; Run
// checks the drivers.
func Main(ctx context.Context, c node.Config, getenv func(string) string) error {
	env := func(k, d string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return d
	}
	drivers := Drivers(c, getenv)
	// A node proves itself with its keys and takes its company from enrollment (node-identity D2, D4).
	keys, err := node.LoadKeys(c.StateDir)
	if err != nil {
		return err
	}
	api := worker.API{URL: env("FACTORY_URL", "http://127.0.0.1:8080"), CurrentKey: node.KeyReloader(c.StateDir, keys.Sign)}
	if err = node.CheckCoreURL(api.URL); err != nil {
		return err
	}
	if api.Node, err = node.NodeID(c.StateDir); err != nil {
		return err
	}
	if c.Company, err = node.LoadCompany(c.StateDir); err != nil {
		return err
	}
	if c.Company == "" {
		return node.ErrNoKeys
	}
	slog.Info("signing requests to Core", "fingerprint", keys.Fingerprint())
	return Run(ctx, Node{Config: c, API: api, Drivers: drivers, Optional: OptionalDrivers(c, getenv), Host: node.LocalHost(runtime.GOOS), Box: keys.Box, Net: node.SystemNetwork()})
}

// SkillStoreMaxAge is how long a stored skill stays unused before the GC removes it.
var SkillStoreMaxAge = 14 * 24 * time.Hour

// Node is a connected node process.
type Node struct {
	Config  node.Config
	API     worker.API
	Drivers []driver.Driver
	Host    node.Host
	// Box is the node's encryption key: it opens the company secrets Core seals to the node.
	Box *ecdh.PrivateKey
	// Watchdog overrides the watchdog settings; nil uses the defaults.
	Watchdog *node.Watchdog
	// Optional are drivers node.yaml leaves off that the settings from the web may turn on (node-packaging-updates B).
	Optional []driver.Driver
	// Detected are the labels the node detected (node.DetectLabels; Run detects them when nil); Version the
	// package's (node.PackageVersion when empty).
	Detected []string
	Version  string
	// Net runs the self-checks' network check of the forge (net.forge, setup-assistant 4.1); nil skips it.
	Net *node.Network

	health *driverHealth  // set by Run
	remote *remoteConfig  // set by Run
	gate   *node.DiskGate // set by Run
}

// enabled reports whether c turns the named driver on.
func enabled(c node.Config, name string) bool {
	if name == "claude" {
		return c.Drivers.Claude.On()
	}
	return c.Drivers.Codex.On()
}

// ErrNoDriver: no enabled driver passed its version check and probe at start.
var ErrNoDriver = errors.New("no usable runtime driver")

// Run checks the drivers (at least one must be usable at start), kills runtimes left by a previous process,
// then runs the slots, the periodic driver checks, the disk gate and the watchdog. A driver that fails a later
// check stops claiming until it passes again; with none usable the node claims nothing.
func Run(ctx context.Context, n Node) error {
	c := n.Config
	if c.Probes.Interval <= 0 || c.Probes.VersionInterval <= 0 {
		c.Probes = node.Defaults().Probes
	}
	// Settings from the web (node-packaging-updates B): remote-config.json over node.yaml.
	nodeYAML := c
	n.remote = newRemoteConfig(nodeYAML)
	c = n.remote.effective()
	n.Config = c // the session registers what is in effect
	all := append(append([]driver.Driver{}, n.Drivers...), n.Optional...)
	listed := map[string]bool{}
	for _, d := range n.Drivers {
		listed[string(d.Name())] = true
	}
	// driverOn: a driver runs when the settings from the web turned it on, else when node.yaml did (n.Drivers).
	driverOn := func(eff node.Config, d driver.Driver) bool {
		name := string(d.Name())
		if e := enabled(eff, name); e != enabled(nodeYAML, name) {
			return e
		}
		return listed[name]
	}
	n.health = newDriverHealth(all, c.Probes)
	for _, d := range all {
		if !driverOn(c, d) {
			n.health.turnOff(string(d.Name()))
		}
	}
	n.health.check(ctx, true)
	if !n.health.anyUsable() {
		return fmt.Errorf("%w: %+v", ErrNoDriver, n.health.infos())
	}
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return err
	}
	if err := worker.KillOrphans(c.StateDir); err != nil {
		return err
	}
	// File secrets of runs a previous process ran are gone with them (core-secret-store D5).
	if err := worker.RemoveSecretLeftovers(c.StateDir); err != nil {
		return err
	}
	mcp, shim := "", ""
	if c.BinDir != "" {
		mcp, shim = filepath.Join(c.BinDir, "factory-mcp"), filepath.Join(c.BinDir, "factory-shim")
	}
	var trees *workspace.Worktrees
	if c.Worktrees.Enabled {
		var err error
		if trees, err = workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: c.Worktrees.Root, ScanCommand: c.Worktrees.ScanCommand}); err != nil {
			return err
		}
	}
	// Skills pinned on runs come from Core by hash into the node's store (skill-registry D8); unused ones age out.
	skillStore := filepath.Join(c.StateDir, "skills")
	if n, err := worker.GCSkillStore(skillStore, SkillStoreMaxAge, nil); err != nil {
		slog.Warn("skill store GC", "error", err)
	} else if n > 0 {
		slog.Info("skill store GC", "removed", n)
	}
	var err error
	store := sessions.Open(c.StateDir)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go func(ctx context.Context) {
		t := time.NewTicker(24 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				_, _ = worker.GCSkillStore(skillStore, SkillStoreMaxAge, nil) // a live run's skills were used recently
			}
		}
	}(ctx)
	company := c.Company // a v2 node checks it against its token's company in the register answer below
	if company == "" {
		company = node.DefaultCompany
	}
	jan := &janitor.Janitor{Config: c.Janitor, Worktrees: trees, Company: company, Sessions: store}
	// One disk gate for claims, health and the janitor; the settings from the web change its watermarks.
	n.gate = node.NewDiskGate(n.Host, c.Disk)
	if n.Host.FreeBytes != nil {
		jan.LowDisk = func() bool {
			low, _ := n.gate.Watermarks()
			free, err := n.Host.FreeBytes(c.Disk.Path)
			return err == nil && free < low
		}
	}
	// What the node reports about itself (node-packaging-updates): its package version and detected labels.
	if n.Version == "" {
		n.Version = node.PackageVersion(c.BinDir)
	}
	if n.Detected == nil {
		n.Detected = node.DetectLabels(ctx, n.Host, runtime.GOARCH)
		slog.Info("labels detected", "labels", n.Detected, "version", n.Version)
	}
	// The v2 heartbeat's cleanup list goes to jan.Cleanup.
	go jan.Run(ctx)
	runStopped := func(spec protocol.RunSpec, worktree string) {
		hookCtx, done := context.WithTimeout(context.Background(), 10*time.Minute)
		defer done()
		jan.RunStopped(hookCtx, spec.ID, spec.TaskID, worktree)
	}
	var s *session
	var checks *selfChecks
	if s, err = newSession(ctx, n, trees); err != nil {
		return err
	}
	if s.company != "" && s.company != company {
		// The node would fetch commands no Core of its key's company ever sends.
		return fmt.Errorf("this node enrolled in company %s, but Core says its key belongs to company %s: enroll it again", company, s.company)
	}
	s.cleanup = jan.Cleanup
	s.reset = func(ctx context.Context, task string) error {
		resetCtx, cancel := context.WithTimeout(ctx, time.Minute) // a run holding the worktree: retried later
		defer cancel()
		_, err := jan.ResetWorkspace(resetCtx, task, "")
		return err
	}
	// The catalog's NodeHooks document (register answer, then heartbeat answers when it changes): janitor
	// hooks under node.yaml's, stack checks for the self-checks, a copy in state_dir for the doctor.
	checks = newSelfChecks(n.Host, union(c.Labels, n.Detected))
	checks.doctor.Net = n.Net
	checks.doctor.Scanner = scanner(c.Worktrees)
	s.checks = checks.failed
	s.onHooks = func(d protocol.NodeHooksDelivery) {
		jan.SetCatalog(d.Spec)
		checks.setStack(d.Spec.Checks)
		if err := node.SaveHooks(c.StateDir, d); err != nil {
			slog.Warn("keeping the NodeHooks document for the doctor", "error", err)
		}
	}
	if s.hooks != nil {
		s.onHooks(*s.hooks)
	}
	go checks.run(ctx, c.Probes.Interval)
	// Run events are spooled under state_dir/runs and sent in order (http-node-transport D5); spools a previous
	// process left are sent first.
	events := &worker.Events{API: n.API}
	if c.StateDir != "" {
		events.Dir = filepath.Join(c.StateDir, "runs")
	}
	go events.Run(ctx, time.Second)
	pool := &Pool{Slots: c.Slots, ConversationSlots: c.ConversationSlots, StateDir: c.StateDir}
	names := []string{}
	for _, d := range all {
		w := &worker.Worker{Events: events, API: n.API, Company: company, Driver: d, MCPBinary: mcp, ShimBinary: shim, StateDir: c.StateDir, Worktrees: trees, NodeID: c.Name, RunStopped: runStopped,
			SkillStore: skillStore, Sessions: store, EnvAllow: c.RuntimeEnv, ProjectsRoot: c.ProjectsRoot, Box: n.Box}
		if c.StateDir != "" {
			w.BlobCache = filepath.Join(c.StateDir, "blobs") // project data by digest, shared by the slots
		}
		// node-readiness: a run checks the project's required tools as the readiness probe does.
		w.ToolRun = n.Host.Run
		w.AuthFailed = func(err error) { n.health.authFailed(string(d.Name()), err) }
		w.RunSucceeded = func() { n.health.runSucceeded(string(d.Name())) }
		w.NetworkFailed = n.health.recheck
		w.RuntimeActivity = func() { n.health.activity(string(d.Name())) }
		policy := c.Policy
		w.LocalPolicy = func(spec protocol.RunSpec) string { return policy.Refuse(spec.Project, string(spec.Provider)) }
		if s != nil {
			w.NodeID, w.Inventory = s.inv.NodeID, s.inv
		}
		name := string(d.Name())
		pool.Sources = append(pool.Sources, &Source{Name: name, Process: w.ProcessRun,
			Claimable: func() bool { return n.health.usable(name) }})
		if driverOn(c, d) {
			names = append(names, name)
		}
	}
	usable := n.health.anyUsable // false: the whole node pauses
	pool.Claimable = usable
	if n.Host.FreeBytes != nil {
		// disk-pause-conversations: below the critical watermark only conversations, below the floor nothing.
		gate := n.gate
		pool.Claimable = func() bool { return usable() && gate.ConversationClaimable() }
		pool.WorkClaimable = gate.Claimable
	}
	// Settings from the web take effect live: slots resize the pool, drivers turn on or off with their own probe, the
	// disk gate takes the watermarks, and the heartbeat carries the capacity and the report at once.
	n.remote.apply = func(eff node.Config) {
		pool.Resize(eff.Slots, eff.ConversationSlots)
		n.gate.Set(eff.Disk)
		checks.setLabels(union(eff.Labels, n.Detected))
		if s != nil {
			s.setCapacity(capacity{slots: eff.Slots, conversation: eff.ConversationSlots, labels: eff.Labels, detected: n.Detected})
		}
		for _, d := range all {
			n.health.setEnabled(ctx, string(d.Name()), driverOn(eff, d))
		}
		if s != nil {
			s.driversChanged()
		}
	}
	if s != nil {
		n.health.changed = s.driversChanged
	}
	go n.health.run(ctx)
	wd := node.Watchdog{}
	if n.Watchdog != nil {
		wd = *n.Watchdog
	}
	wd.Slots, wd.Groups = pool, groups(c.StateDir)
	go wd.Run(ctx)
	var failed error
	done := make(chan struct{})
	if s != nil {
		local, localWork := pool.Claimable, pool.WorkClaimable
		pool.Claimable = func() bool { return s.claimable() && local() && !checks.blocked() }
		pool.WorkClaimable = func() bool { return s.workClaimable() && (localWork == nil || localWork()) }
		s.busy = func() int { return busySlots(pool) }
		go func() {
			defer close(done)
			if failed = s.run(ctx); failed != nil {
				slog.Error("node stopping", "error", failed)
				stop()
			}
		}()
	} else {
		close(done)
	}
	if s != nil {
		go s.poll(ctx, pool)
	}
	slog.Info("node started", "node", c.Name, "slots", c.Slots, "conversation_slots", c.ConversationSlots, "drivers", names, "state_dir", c.StateDir, "worktrees", c.Worktrees.Root, "poll_wait_seconds", c.Poll.WaitSeconds)
	err = pool.Run(ctx)
	stop()
	<-done
	if failed != nil {
		return failed
	}
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return err
}

func busySlots(p *Pool) int {
	n := 0
	for _, s := range p.Status() {
		if s.Busy {
			n++
		}
	}
	return n
}

// groups is the executor's pidfile view for the watchdog.
type groups string

func (g groups) Tracked() ([]node.TrackedGroup, error) {
	t, err := worker.TrackedGroups(string(g))
	r := make([]node.TrackedGroup, len(t))
	for i, x := range t {
		r[i] = node.TrackedGroup{Run: x.Run, Group: x.Group}
	}
	return r, err
}
func (g groups) Alive(group int) bool { return worker.GroupAlive(group) }
func (g groups) Reap(t node.TrackedGroup) error {
	all, err := worker.TrackedGroups(string(g))
	for _, x := range all {
		if x.Run == t.Run && x.Group == t.Group {
			return x.Reap()
		}
	}
	return err
}

// scanner is the worktrees' secret scanner the self-checks look for; nil when worktrees are off.
func scanner(w node.Worktrees) []string {
	if !w.Enabled {
		return nil
	}
	if len(w.ScanCommand) > 0 {
		return w.ScanCommand
	}
	return workspace.DefaultScanCommand
}
