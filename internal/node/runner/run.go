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
	"appfactory/internal/driver"
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"appfactory/internal/worker"
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
		r = append(r, claude.Driver{Node: cc.Node, Shim: shim, Executable: cc.Executable, AllowAPIKey: cc.AllowAPIKey})
	}
	return r
}

// Main checks the drivers and the environment (FACTORY_TOKEN, FACTORY_URL, NATS_URL), connects and
// runs the node until ctx ends. codex-worker is Main with one slot and the Codex driver.
func Main(ctx context.Context, c node.Config, getenv func(string) string) error {
	env := func(k, d string) string {
		if v := getenv(k); v != "" {
			return v
		}
		return d
	}
	drivers := Drivers(c, getenv)
	for _, d := range drivers {
		if _, err := d.CheckVersion(ctx); err != nil {
			return fmt.Errorf("%s: %w", d.Name(), err)
		}
	}
	if getenv("FACTORY_TOKEN") == "" {
		return fmt.Errorf("FACTORY_TOKEN required")
	}
	b, err := bus.Connect(env("NATS_URL", "nats://127.0.0.1:4222"))
	if err != nil {
		return err
	}
	defer b.NC.Close()
	return Run(ctx, Node{Config: c, Bus: b, API: worker.API{URL: env("FACTORY_URL", "http://127.0.0.1:8080"), Token: getenv("FACTORY_TOKEN")}, Drivers: drivers, Host: node.LocalHost(runtime.GOOS)})
}

// Node is a connected node process.
type Node struct {
	Config  node.Config
	Bus     *bus.Bus
	API     worker.API
	Drivers []driver.Driver
	Host    node.Host
	// FetchWait bounds one fetch; default 1 s as in the standalone worker.
	FetchWait time.Duration
	// Watchdog overrides the watchdog settings; nil uses the defaults.
	Watchdog *node.Watchdog
}

// Run kills runtimes left by a previous process, then runs the slots, the disk gate and the watchdog.
func Run(ctx context.Context, n Node) error {
	c := n.Config
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
	mcp := ""
	if c.BinDir != "" {
		mcp = filepath.Join(c.BinDir, "factory-mcp")
	}
	pool := &Pool{Slots: c.Slots}
	names := []string{}
	for _, d := range n.Drivers {
		w := &worker.Worker{Bus: n.Bus, API: n.API, Driver: d, MCPBinary: mcp, StateDir: c.StateDir}
		sub, err := w.Subscribe()
		if err != nil {
			return err
		}
		pool.Sources = append(pool.Sources, &Source{Name: string(d.Name()), Process: w.Process,
			Fetch: func() ([]*nats.Msg, error) { return sub.Fetch(1, nats.MaxWait(wait)) }})
		names = append(names, string(d.Name()))
	}
	if n.Host.FreeBytes != nil {
		pool.Claimable = node.NewDiskGate(n.Host, c.Disk).Claimable
	}
	wd := node.Watchdog{}
	if n.Watchdog != nil {
		wd = *n.Watchdog
	}
	wd.Slots, wd.Groups = pool, groups(c.StateDir)
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	go wd.Run(ctx)
	slog.Info("node started", "node", c.Name, "slots", c.Slots, "drivers", names, "state_dir", c.StateDir)
	err := pool.Run(ctx)
	if errors.Is(err, context.Canceled) {
		err = nil
	}
	return err
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
