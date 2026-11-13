package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"appfactory/internal/node"
	"appfactory/internal/node/runner"
	"appfactory/internal/node/selfupdate"
)

// selftest is the check a new version passes before the node switches to it (node-packaging-updates C, step 2): run
// from versions/<v>/bin, it proves the binary starts on this machine, reads node.yaml (with the settings from the
// web), reaches Core with the node's keys and runs the probes of the enabled drivers against the runtimes of its own
// package directory, without a model call. -json prints the results for the updater; exit 1 when one fails.
// In the versioned layout it first installs its package's runtimes with its own code (runner.FetchRuntimes, reusing
// the current version's): the running version fetched them already, but its code may not know every file the new
// locks pin, as the old updaters do not know codex-code-mode-host.
func selftest(args []string) (int, error) {
	fs := flag.NewFlagSet("selftest", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+")")
	binFlag := fs.String("bin-dir", "", "the bin directory of the version under test (default: this executable's)")
	asJSON := fs.Bool("json", false, "print the results as JSON")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2, errors.New("usage: factory-node selftest [-config node.yaml] [-bin-dir DIR] [-json]")
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	binDir := *binFlag
	if binDir == "" {
		exe, err := os.Executable()
		if err != nil {
			return 1, err
		}
		binDir = filepath.Dir(exe)
	}
	if binDir, err = filepath.Abs(binDir); err != nil {
		return 1, err
	}
	// The drivers the node runs: node.yaml under the settings from the web it applied.
	if f, err := node.LoadRemote(c.StateDir); err == nil && f.Version > 0 {
		c, _ = node.ApplyRemote(c, f.Version, f.Config)
	}
	h := node.LocalHost(runtime.GOOS)
	getenv := h.Getenv
	h.Getenv = func(k string) string {
		if k == "FACTORY_BIN_DIR" {
			return binDir
		}
		return getenv(k)
	}
	d := node.Doctor{Host: h, StateDir: c.StateDir, Runtimes: driverProbes(c, h, nil, binDir)}
	if c.Drivers.Codex.On() {
		d.Drivers = append(d.Drivers, "codex")
	}
	if c.Drivers.Claude.On() {
		d.Drivers = append(d.Drivers, "claude")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var results []node.CheckResult
	if home, v, ok := selfupdate.Locate(filepath.Join(binDir, "factory-node")); ok {
		prev := ""
		if cur := selfupdate.Current(home); cur != "" && cur != v {
			prev = selfupdate.Dir(home, cur)
		}
		if err := runner.FetchRuntimes(ctx, selfupdate.Dir(home, v), prev); err != nil {
			results = append(results, node.CheckResult{Check: "runtimes", Status: node.Fail, Detail: err.Error(),
				Hint: "factory-node runtimes update -home " + selfupdate.Dir(home, v)})
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	results = append(results, d.SelfTest(ctx)...)
	if *asJSON {
		err = json.NewEncoder(os.Stdout).Encode(results)
	} else {
		err = node.Render(os.Stdout, results)
	}
	if err != nil {
		return 1, err
	}
	if node.Failed(results) {
		return 1, nil
	}
	return 0, nil
}
