// factory-node is the node agent (openspec change node-agent): `run` executes runs in N slots,
// `doctor` runs the local self-checks without Core, `launchd` and `systemd` print a supervisor unit for `run`,
// `workspace reset` is reset-workspace of one task on this node for operators.
//
// It is the package's only Go executable (node-packaging-updates A1): `factory-node shim` is the command guard and
// `factory-node mcp` the per-run Factory MCP adapter. The package links factory-shim and factory-mcp to it; run by
// such a name (or as a guarded command through a run's shim directory) it acts as that program.
package main

import (
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"appfactory/internal/node/janitor"
	"appfactory/internal/node/runner"
	pinned "appfactory/internal/node/runtimes"
	"appfactory/internal/node/selfupdate"
	"appfactory/internal/node/sessions"
	"appfactory/internal/node/shim"
	"appfactory/internal/storage"
	"appfactory/internal/workspace"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"
)

const usage = "usage: factory-node run [-config node.yaml] [-slots N] | selftest [-config node.yaml] [-bin-dir DIR] | shim --decide | mcp | login [-config node.yaml] [-name NAME] [-no-browser] | login claude|codex [-config node.yaml] [-copy-current] | accounts [accept claude|codex] | pause [REASON] | resume | activity [-n 50] | uninstall [-keep-state] [-yes] | enroll [-config node.yaml] CODE | rotate-keys [-config node.yaml] | doctor [-config node.yaml] [-security] [-disk-path DIR] [-low-gib N] [-critical-gib N] [-repo URL] [-drivers codex,claude] | launchd [-config node.yaml] [-label LABEL] | systemd [-config node.yaml] [-unit NAME] [-system] [-user USER] [-env-file PATH] | workspace reset -task ID [-project P] [-config node.yaml] [-wait 1m] | runtimes update [claude|codex] [-config node.yaml] [-home DIR] [-prev DIR]"

func main() {
	switch personality(os.Args[0]) {
	case "mcp":
		os.Exit(mcpMain())
	case "shim":
		os.Exit(shimMain(os.Args))
	}
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "factory-node:", err)
	}
	os.Exit(code)
}

// personality is what the executable acts as by the name it was run under: factory-mcp is the MCP adapter, any name
// but factory-node (factory-shim, or a guarded command linked to it in a run's shim directory) the command shim.
func personality(argv0 string) string {
	name := filepath.Base(argv0)
	switch {
	case name == "factory-mcp":
		return "mcp"
	case name == "factory-node" || strings.HasPrefix(name, "factory-node.") || name == "main": // main: go run
		return "node"
	}
	return "shim"
}

func run(args []string) (int, error) {
	if len(args) == 0 {
		return 2, errors.New(usage)
	}
	switch args[0] {
	case "doctor":
		return doctor(args[1:])
	case "enroll":
		return enroll(args[1:])
	case "login":
		if len(args) > 1 && (args[1] == "claude" || args[1] == "codex") {
			return runtimeLogin(args[1], args[2:])
		}
		return login(args[1:])
	case "accounts":
		return accounts(args[1:])
	case "pause":
		return pauseLocal(true, args[1:])
	case "resume":
		return pauseLocal(false, args[1:])
	case "activity":
		return activity(args[1:])
	case "uninstall":
		return uninstall(args[1:])
	case "rotate-keys":
		return rotateKeys(args[1:])
	case "run":
		return runNode(args[1:])
	case "launchd":
		return launchd(args[1:])
	case "systemd":
		return systemd(args[1:])
	case "runtimes":
		return runtimes(args[1:])
	case "selftest":
		return selftest(args[1:])
	case "shim":
		// factory-node shim --decide: the guard's line mode, as factory-shim (shim.Binary) answers it.
		return shimMain(append([]string{shim.Binary}, args[1:]...)), nil
	case "mcp":
		return mcpMain(), nil
	case "workspace":
		if len(args) > 1 && args[1] == "reset" {
			return resetWorkspace(args[2:])
		}
	}
	return 2, errors.New(usage)
}

// enroll registers this node with an owner's code and waits for approval (node-identity D3).
func enroll(args []string) (int, error) {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+", else built-in defaults)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() != 1 {
		return 2, errors.New("usage: factory-node enroll [-config node.yaml] CODE")
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	base := os.Getenv("FACTORY_URL")
	if base == "" {
		return 2, errors.New("FACTORY_URL required: the address of Core")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err = (node.Enrollment{URL: base, Code: fs.Arg(0), StateDir: c.StateDir, Out: os.Stdout}).Run(ctx); err != nil {
		return 1, err
	}
	return 0, nil
}

// login connects this node by signing in on the web (node-install D5).
func login(args []string) (int, error) {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+", else built-in defaults)")
	name := fs.String("name", "", "the node's name (default node.yaml name, else the host name)")
	noBrowser := fs.Bool("no-browser", false, "print the approval address without opening a browser")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2, errors.New("usage: factory-node login [-config node.yaml] [-name NAME] [-no-browser]")
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	base := os.Getenv("FACTORY_URL")
	if base == "" {
		return 2, errors.New("FACTORY_URL required: the address of Core")
	}
	if *name == "" {
		*name = c.Name
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	conn := node.Connect{URL: base, Name: *name, StateDir: c.StateDir, Out: os.Stdout}
	if !*noBrowser {
		conn.Open = node.OpenBrowser
	}
	if _, err = conn.Run(ctx); err != nil {
		return 1, err
	}
	return 0, nil
}

// rotateKeys proposes new keys and waits for an owner (node-identity D5).
func rotateKeys(args []string) (int, error) {
	fs := flag.NewFlagSet("rotate-keys", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+", else built-in defaults)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	base := os.Getenv("FACTORY_URL")
	if base == "" {
		return 2, errors.New("FACTORY_URL required: the address of Core")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if _, err = (node.Rotation{URL: base, StateDir: c.StateDir, Out: os.Stdout}).Run(ctx); err != nil {
		return 1, err
	}
	return 0, nil
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
	up, code := updater(c, node.ConfigPath(*path))
	if code != 0 {
		return code, nil
	}
	err = runner.Main(ctx, c, os.Getenv, up)
	if errors.Is(err, runner.ErrRestart) {
		slog.Info("node restarting into another version")
		return runner.RestartExitCode, nil
	}
	if err != nil {
		slog.Error("node stopped", "error", err)
		return 1, nil
	}
	return 0, nil
}

// updater sets up version updates (node-packaging-updates 3.4) for a node installed in the versioned layout: first of
// all, a start after a switch counts toward the new version's health check, and a version that keeps failing is rolled
// back here (the exit code then makes the service start the previous one).
func updater(c node.Config, config string) (*runner.Updater, int) {
	up := &runner.Updater{Supervised: os.Getenv(runner.ServiceEnv) != "", Platform: pinned.Platform()}
	if config != "" {
		if abs, err := filepath.Abs(config); err == nil {
			up.Config = abs
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return up, 0
	}
	home, v, ok := selfupdate.Locate(exe)
	if !ok {
		up.Version = node.PackageVersion(c.BinDir)
		return up, 0 // installed before the versioned layout: updates are refused with the reason
	}
	up.Home, up.Version = home, v
	boot, err := selfupdate.Check(home, v, time.Now())
	if err != nil {
		slog.Error("update state", "error", err)
	}
	if boot.RolledBack {
		slog.Error("new version unhealthy; rolled back", "reason", boot.Reason)
		return nil, runner.RestartExitCode
	}
	up.Boot = boot
	return up, 0
}

func launchd(args []string) (int, error) {
	fs := flag.NewFlagSet("launchd", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml passed to run (default $"+node.ConfigEnv+")")
	label := fs.String("label", "com.appfactory.node", "launchd label")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	exe, err := serviceExecutable()
	if err != nil {
		return 1, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return 1, err
	}
	l := node.Launchd{Label: *label, Executable: exe, WorkingDir: wd, Env: map[string]string{runner.ServiceEnv: "launchd"}}
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
	for _, k := range []string{"PATH", "FACTORY_URL", "CODEX_BIN", "CLAUDE_CODE_EXECUTABLE"} {
		if v := os.Getenv(k); v != "" {
			l.Env[k] = v
		}
	}
	if err = l.Render(os.Stdout); err != nil {
		return 1, err
	}
	return 0, nil
}

// systemd prints a systemd service for `run`. Settings are not inlined: FACTORY_URL goes into the 0600 environment
// file the unit names.
func systemd(args []string) (int, error) {
	fs := flag.NewFlagSet("systemd", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml passed to run (default $"+node.ConfigEnv+")")
	name := fs.String("unit", "factory-node", "unit name (the file is <name>.service)")
	system := fs.Bool("system", false, "system unit run as -user (default: a user unit)")
	usr := fs.String("user", "", "user of a system unit (default: the current user)")
	envFile := fs.String("env-file", "", "secrets file, KEY=value lines, chmod 600 (default ~/.config/factory-node/env, /etc/factory-node/env with -system)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if fs.NArg() > 0 || *name == "" || strings.ContainsAny(*name, "/ ") {
		return 2, errors.New("usage: factory-node systemd [-config node.yaml] [-unit NAME] [-system] [-user USER] [-env-file PATH]")
	}
	*name = strings.TrimSuffix(*name, ".service")
	exe, err := serviceExecutable()
	if err != nil {
		return 1, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return 1, err
	}
	s := node.Systemd{Description: "App Factory node agent (" + *name + ")", Executable: exe, WorkingDir: wd, System: *system, User: *usr, Env: map[string]string{runner.ServiceEnv: "systemd"}}
	if p := node.ConfigPath(*path); p != "" {
		if s.Config, err = filepath.Abs(p); err != nil {
			return 1, err
		}
		if _, err = node.LoadConfig(s.Config); err != nil {
			return 2, err
		}
	}
	home, _ := os.UserHomeDir()
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(home, ".config")
	}
	unitDir := filepath.Join(config, "systemd", "user")
	s.EnvFile = filepath.Join(config, "factory-node", "env")
	if *system {
		unitDir, s.EnvFile = "/etc/systemd/system", "/etc/factory-node/env"
		if s.User == "" {
			u, err := user.Current()
			if err != nil {
				return 1, err
			}
			s.User = u.Username
		}
	}
	if *envFile != "" {
		if s.EnvFile, err = filepath.Abs(*envFile); err != nil {
			return 1, err
		}
	}
	// systemd starts services with a minimal PATH; runtimes need node, codex and git.
	for _, k := range []string{"PATH", "FACTORY_URL", "CODEX_BIN", "CLAUDE_CODE_EXECUTABLE"} {
		if v := os.Getenv(k); v != "" {
			s.Env[k] = v
		}
	}
	if err = s.Render(os.Stdout); err != nil {
		return 1, err
	}
	ctl := "systemctl --user"
	if *system {
		ctl = "sudo systemctl"
	}
	fmt.Fprintf(os.Stderr, "save as %s/%s.service; put FACTORY_URL=... in %s (chmod 600) and enroll the node first with factory-node enroll CODE; then %s daemon-reload && %s enable --now %s\n",
		unitDir, *name, s.EnvFile, ctl, ctl, *name)
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
	path := fs.String("config", "", "node.yaml of the node (default $"+node.ConfigEnv+")")
	security := fs.Bool("security", false, "only what agents may touch here and what protects the node (connector-trust)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *security {
		c, err := node.LoadConfig(node.ConfigPath(*path))
		if err != nil {
			return 2, err
		}
		results := node.SecurityChecks(c)
		if err := node.Render(os.Stdout, results); err != nil {
			return 1, err
		}
		if node.Failed(results) {
			return 1, nil
		}
		return 0, nil
	}
	h := node.LocalHost(runtime.GOOS)
	d := node.Doctor{Host: h, DiskPath: *disk, Low: uint64(*low * node.GiB), Critical: uint64(*critical * node.GiB), Repo: *repo, Net: node.SystemNetwork()}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	d.Labels, d.StateDir = c.Labels, c.StateDir
	if c.Worktrees.Enabled {
		d.Scanner = c.Worktrees.ScanCommand
		if len(d.Scanner) == 0 {
			d.Scanner = workspace.DefaultScanCommand
		}
	}
	// Stack checks of the catalog's NodeHooks document, as Core last delivered it to this node.
	if hooks, ok, err := node.LoadHooks(c.StateDir); err != nil {
		fmt.Fprintf(os.Stderr, "stack checks skipped: %s: %v\n", filepath.Join(c.StateDir, node.HooksFile), err)
	} else if ok {
		d.Stack = hooks.Spec.Checks
	}
	// Each driver owns its probe (runtime-drivers D4).
	// The probes see the drivers as node.yaml sets them, like `run` (runner.Drivers).
	binDir := c.BinDir
	if binDir == "" {
		binDir = d.BinDir()
	}
	d.Runtimes = driverProbes(c, h, d.Net, binDir)
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

// serviceExecutable is what launchd or systemd runs: in the versioned layout <home>/bin/factory-node, which follows
// current (node-packaging-updates 3.4), not the version that printed the unit.
func serviceExecutable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if home, _, ok := selfupdate.Locate(exe); ok {
		return filepath.Join(home, "bin", "factory-node"), nil
	}
	return exe, nil
}

// driverProbes are the drivers' probes as node.yaml sets them, like `run` (runner.Drivers), for a node with bin
// directory binDir (runtime-drivers D4: each driver owns its probe).
func driverProbes(c node.Config, h node.Host, net *node.Network, binDir string) []node.Runtime {
	c.BinDir = binDir
	cc := c.Drivers.Claude
	shim := cc.Shim
	if shim == "" && binDir != "" {
		shim = claude.DefaultShimFor(binDir, func(p string) bool { _, err := os.Stat(p); return err == nil })
	}
	accounts := node.AccountsDir(c.StateDir)
	return []node.Runtime{{Name: "codex", Probe: codex.Driver{Binary: node.CodexBinary(c, os.Getenv), Host: &h, Home: node.ExpandHome(c.Drivers.Codex.Home), AccountsDir: accounts, Net: net}},
		{Name: "claude", Probe: claude.Driver{Host: &h, BinDir: binDir, Node: cc.Node, Shim: shim, Executable: cc.Executable, AllowAPIKey: cc.AllowAPIKey, ConfigDir: node.ExpandHome(cc.ConfigDir), AccountsDir: accounts, Sandbox: c.Sandbox, Net: net}}}
}

// resetWorkspace seals, removes and recreates a task's worktree from the remote branch (or the default
// branch) and forgets the task's session records, so its next run starts a fresh session. The local state
// is kept on refs/factory/reset/<task>. It can run next to `factory-node run`: a running run of the task
// makes it wait up to -wait, then fail.
func resetWorkspace(args []string) (int, error) {
	fs := flag.NewFlagSet("workspace reset", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+", else built-in defaults)")
	task := fs.String("task", "", "task ID (required)")
	project := fs.String("project", "", "project ID (default: every project with a worktree of the task)")
	wait := fs.Duration("wait", time.Minute, "how long to wait for a run that holds the worktree")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if *task == "" || fs.NArg() > 0 {
		return 2, errors.New("usage: factory-node workspace reset -task ID [-project P] [-config node.yaml] [-wait 1m]")
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	j := &janitor.Janitor{Company: storage.DefaultCompany, Sessions: sessions.Open(c.StateDir)}
	if c.Worktrees.Enabled {
		if j.Worktrees, err = workspace.NewWorktrees(workspace.WorktreesConfig{Enabled: true, Root: c.Worktrees.Root, ScanCommand: c.Worktrees.ScanCommand}); err != nil {
			return 1, err
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *wait)
	defer cancel()
	res, err := j.ResetWorkspace(ctx, *task, *project)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if e := enc.Encode(res); e != nil {
		return 1, e
	}
	if errors.Is(err, workspace.ErrBusy) {
		return 1, fmt.Errorf("%w; retry when the task's run has ended", err)
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}
