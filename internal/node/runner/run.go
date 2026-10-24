package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/driver"
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"appfactory/internal/node/janitor"
	"appfactory/internal/node/sessions"
	"appfactory/internal/protocol"
	"appfactory/internal/skills"
	"appfactory/internal/worker"
	"appfactory/internal/workspace"
	"github.com/nats-io/nats.go"
)

// Drivers builds the enabled drivers of c. getenv supplies CODEX_BIN.
func Drivers(c node.Config, getenv func(string) string) []driver.Driver {
	var r []driver.Driver
	if c.Drivers.Codex.On() {
		bin := c.Drivers.Codex.Binary
		if bin == "" {
			bin = getenv("CODEX_BIN")
		}
		if bin == "" {
			bin = "codex"
		}
		r = append(r, codex.Driver{Binary: bin})
	}
	if cc := c.Drivers.Claude; cc.On() {
		shim := cc.Shim
		if shim == "" && c.BinDir != "" {
			shim = filepath.Join(c.BinDir, "..", "workers", "claude", "dist", "src", "shim.js")
		}
		r = append(r, claude.Driver{Node: cc.Node, Shim: shim, Executable: cc.Executable, AllowAPIKey: cc.AllowAPIKey, BinDir: c.BinDir})
	}
	return r
}

// Main checks the environment (the node token: FACTORY_NODE_TOKEN, token_file or FACTORY_TOKEN; FACTORY_URL; NATS_URL when node.yaml has no nats.url), connects and runs the node until
// ctx ends; Run checks the drivers. codex-worker is Main with one slot and the Codex driver.
func Main(ctx context.Context, c node.Config, getenv func(string) string) error {
	env := func(k, d string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return d
	}
	drivers := Drivers(c, getenv)
	token, source, err := c.Token(getenv)
	if err != nil {
		return err
	}
	if token == "" {
		return fmt.Errorf("node token required: %s, node.yaml token_file, or the shared FACTORY_TOKEN", node.TokenEnv)
	}
	slog.Info("authenticating to Core", "with", source)
	natsURL := c.NATS.ResolveURL(getenv)
	slog.Info("connecting to NATS", "url", bus.Redact(natsURL), "auth", c.NATS.Auth(natsURL), "tls", c.NATS.TLS.Enabled())
	b, err := bus.ConnectWith(natsURL, c.NATS.Options)
	if err != nil {
		return fmt.Errorf("nats %s: %w", bus.Redact(natsURL), err)
	}
	defer b.NC.Close()
	return Run(ctx, Node{Config: c, Bus: b, API: worker.API{URL: env("FACTORY_URL", "http://127.0.0.1:8080"), Token: token}, Drivers: drivers, Host: node.LocalHost(runtime.GOOS)})
}

// SkillsRoot is the factory checkout whose company catalog's skills.yaml (configs/skills.yaml for the default
// company, configs/<company>/skills.yaml for another) is the node's skills registry.
func SkillsRoot(c node.Config) string {
	switch {
	case c.SkillsRoot != "":
		return c.SkillsRoot
	case c.BinDir != "":
		return filepath.Join(c.BinDir, "..")
	}
	return "."
}

// LoadSkills loads the node's skills registry, the skills.yaml of its company's catalog under SkillsRoot (the
// same file Core pins hashes from); nil (logged) when there is none, so runs start without materialized skills.
func LoadSkills(c node.Config) (*skills.Registry, error) {
	root, err := filepath.Abs(SkillsRoot(c))
	if err != nil {
		return nil, err
	}
	rel, err := config.CompanyDir(c.Company)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, rel)
	if _, err = os.Stat(filepath.Join(dir, skills.File)); errors.Is(err, os.ErrNotExist) {
		slog.Warn("no skills registry; runs get no materialized skills", "skills_root", root, "catalog", rel)
		return nil, nil
	}
	r, err := skills.Load(dir)
	if err != nil {
		return nil, fmt.Errorf("skills registry %s: %w", filepath.Join(dir, skills.File), err)
	}
	return r, nil
}

// Node is a connected node process.
type Node struct {
	Config  node.Config
	Bus     *bus.Bus
	API     worker.API
	Drivers []driver.Driver
	Host    node.Host
	// FetchWait bounds one fetch; default 1 s as in codex-worker.
	FetchWait time.Duration
	// Watchdog overrides the watchdog settings; nil uses the defaults.
	Watchdog *node.Watchdog

	health *driverHealth // set by Run
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
	n.health = newDriverHealth(n.Drivers, c.Probes)
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
	wait := n.FetchWait
	if wait <= 0 {
		wait = time.Second
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
	registry, err := LoadSkills(c)
	if err != nil {
		return err
	}
	store := sessions.Open(c.StateDir)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	company := c.Company // a v2 node checks it against its token's company in the register answer below
	if company == "" {
		company = node.DefaultCompany
	}
	jan := &janitor.Janitor{Config: c.Janitor, Worktrees: trees, Company: company, Sessions: store}
	if n.Host.FreeBytes != nil {
		low := uint64(c.Disk.LowGiB * node.GiB)
		jan.LowDisk = func() bool { free, err := n.Host.FreeBytes(c.Disk.Path); return err == nil && free < low }
	}
	// The v2 heartbeat's cleanup list goes to jan.Cleanup.
	go jan.Run(ctx)
	runStopped := func(spec protocol.RunSpec, worktree string) {
		hookCtx, done := context.WithTimeout(context.Background(), 10*time.Minute)
		defer done()
		jan.RunStopped(hookCtx, spec.ID, spec.TaskID, worktree)
	}
	var s *session // protocol v2
	var checks *selfChecks
	if c.ProtocolVersion == 2 {
		var err error
		if s, err = newSession(ctx, n, trees); err != nil {
			return err
		}
		if s.company != "" && s.company != company {
			// The node would fetch commands no Core of its token's company ever sends.
			return fmt.Errorf("node.yaml company %s, but the node token belongs to company %s: set company: %s", company, s.company, s.company)
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
		checks = newSelfChecks(n.Host, c.Labels)
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
		// Core's urgent stops arrive on the node's control subject, outside the command stream (a plain
		// subscription: missed while disconnected, then the heartbeat answer stops the run).
		control, err := n.Bus.NC.Subscribe(protocol.NodeControlSubject(company, s.inv.NodeID), func(m *nats.Msg) { s.control(m.Data) })
		if err != nil {
			return err
		}
		defer control.Unsubscribe()
	}
	pool := &Pool{Slots: c.Slots}
	names := []string{}
	for _, d := range n.Drivers {
		w := &worker.Worker{Bus: n.Bus, API: n.API, Company: company, Driver: d, MCPBinary: mcp, ShimBinary: shim, StateDir: c.StateDir, Worktrees: trees, NodeID: c.Name, RunStopped: runStopped,
			Skills: registry, Sessions: store, EnvAllow: c.RuntimeEnv, SecretsFile: c.SecretsFile}
		if c.StateDir != "" {
			w.BlobCache = filepath.Join(c.StateDir, "blobs") // project data by digest, shared by the slots
		}
		w.AuthFailed = func(err error) { n.health.authFailed(string(d.Name()), err) }
		w.RunSucceeded = func() { n.health.runSucceeded(string(d.Name())) }
		w.RuntimeActivity = func() { n.health.activity(string(d.Name())) }
		if s != nil {
			w.NodeID, w.Inventory = s.inv.NodeID, s.inv
		}
		sub, err := w.Subscribe()
		if err != nil {
			return err
		}
		name := string(d.Name())
		pool.Sources = append(pool.Sources, &Source{Name: name, Process: w.Process,
			Fetch:     func() ([]*nats.Msg, error) { return sub.Fetch(1, nats.MaxWait(wait)) },
			Claimable: func() bool { return n.health.usable(name) }})
		names = append(names, name)
	}
	usable := n.health.anyUsable // false: the whole node pauses
	pool.Claimable = usable
	if n.Host.FreeBytes != nil {
		disk := node.NewDiskGate(n.Host, c.Disk).Claimable
		pool.Claimable = func() bool { return usable() && disk() }
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
		local := pool.Claimable
		pool.Claimable = func() bool { return s.claimable() && local() && !checks.blocked() }
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
	slog.Info("node started", "node", c.Name, "slots", c.Slots, "drivers", names, "state_dir", c.StateDir, "worktrees", c.Worktrees.Root, "protocol_version", c.ProtocolVersion)
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
