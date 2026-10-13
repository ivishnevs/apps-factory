// factory-node is the node agent (openspec change node-agent): `run` executes runs in N slots,
// `doctor` runs the local self-checks without Core, `launchd` prints a launchd agent for `run`.
package main

import (
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"appfactory/internal/node/runner"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

const usage = "usage: factory-node run [-config node.yaml] [-slots N] | doctor [-disk-path DIR] [-low-gib N] [-critical-gib N] [-repo URL] [-drivers codex,claude] | launchd [-config node.yaml] [-label LABEL]"

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "factory-node:", err)
	}
	os.Exit(code)
}

func run(args []string) (int, error) {
	if len(args) == 0 {
		return 2, errors.New(usage)
	}
	switch args[0] {
	case "doctor":
		return doctor(args[1:])
	case "run":
		return runNode(args[1:])
	case "launchd":
		return launchd(args[1:])
	}
	return 2, errors.New(usage)
}

func runNode(args []string) (int, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+", else built-in defaults)")
	slots := fs.Int("slots", 0, "override slots")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	if *slots != 0 {
		c.Slots = *slots
		if err = c.Validate(); err != nil {
			return 2, err
		}
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err = runner.Main(ctx, c, os.Getenv); err != nil {
		slog.Error("node stopped", "error", err)
		return 1, nil
	}
	return 0, nil
}

func launchd(args []string) (int, error) {
	fs := flag.NewFlagSet("launchd", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml passed to run (default $"+node.ConfigEnv+")")
	label := fs.String("label", "com.appfactory.node", "launchd label")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return 1, err
	}
	l := node.Launchd{Label: *label, Executable: exe, WorkingDir: wd, Env: map[string]string{}}
	if p := node.ConfigPath(*path); p != "" {
		if l.Config, err = filepath.Abs(p); err != nil {
			return 1, err
		}
		if _, err = node.LoadConfig(l.Config); err != nil {
			return 2, err
		}
	}
	home, _ := os.UserHomeDir()
	l.Log = filepath.Join(home, "Library", "Logs", "factory-node.log")
	// launchd starts agents with a minimal PATH; runtimes need node, codex and git.
	for _, k := range []string{"PATH", "NATS_URL", "FACTORY_URL", "FACTORY_TOKEN", "CODEX_BIN", "CLAUDE_CODE_EXECUTABLE"} {
		if v := os.Getenv(k); v != "" {
			l.Env[k] = v
		}
	}
	if l.Env["FACTORY_TOKEN"] == "" {
		l.Env["FACTORY_TOKEN"] = "REPLACE_WITH_FACTORY_TOKEN"
	}
	if err = l.Render(os.Stdout); err != nil {
		return 1, err
	}
	return 0, nil
}

func doctor(args []string) (int, error) {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	disk := fs.String("disk-path", home, "workspace root whose free space is checked")
	low := fs.Float64("low-gib", 20, "disk.low_watermark in GiB")
	critical := fs.Float64("critical-gib", 5, "disk.critical_watermark in GiB")
	repo := fs.String("repo", "", "repository URL checked with git ls-remote (optional)")
	drivers := fs.String("drivers", "", "comma list of drivers to require (default: every installed one)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	h := node.LocalHost(runtime.GOOS)
	d := node.Doctor{Host: h, DiskPath: *disk, Low: uint64(*low * node.GiB), Critical: uint64(*critical * node.GiB), Repo: *repo}
	// Each driver owns its probe (runtime-drivers D4).
	d.Runtimes = []node.Runtime{{Name: "codex", Probe: codex.Driver{Binary: os.Getenv("CODEX_BIN"), Host: &h}},
		{Name: "claude", Probe: claude.Driver{Host: &h, BinDir: d.BinDir()}}}
	known := []string{}
	for _, rt := range d.Runtimes {
		known = append(known, rt.Name)
	}
	for _, v := range strings.Split(*drivers, ",") {
		if v = strings.TrimSpace(v); v != "" {
			if !slices.Contains(known, v) {
				return 2, fmt.Errorf("unknown driver %q (known: %s)", v, strings.Join(known, ", "))
			}
			d.Drivers = append(d.Drivers, v)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	results := d.Run(ctx)
	if err := node.Render(os.Stdout, results); err != nil {
		return 1, err
	}
	if node.Failed(results) {
		return 1, nil
	}
	return 0, nil
}
