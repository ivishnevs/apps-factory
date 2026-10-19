// factory-node is the node agent (openspec change node-agent): `run` executes runs in N slots,
// `doctor` runs the local self-checks without Core, `launchd` and `systemd` print a supervisor unit for `run`,
// `workspace reset` is reset-workspace of one task on this node for operators.
package main

import (
	"appfactory/internal/driver/claude"
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"appfactory/internal/node/janitor"
	"appfactory/internal/node/runner"
	"appfactory/internal/node/sessions"
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

const usage = "usage: factory-node run [-config node.yaml] [-slots N] | doctor [-config node.yaml] [-disk-path DIR] [-low-gib N] [-critical-gib N] [-repo URL] [-drivers codex,claude] | launchd [-config node.yaml] [-label LABEL] | systemd [-config node.yaml] [-unit NAME] [-system] [-user USER] [-env-file PATH] | workspace reset -task ID [-project P] [-config node.yaml] [-wait 1m]"

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
	case "systemd":
		return systemd(args[1:])
	case "workspace":
		if len(args) > 1 && args[1] == "reset" {
			return resetWorkspace(args[2:])
		}
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
	for _, k := range []string{"PATH", "NATS_URL", "FACTORY_URL", "FACTORY_TOKEN", node.TokenEnv, "CODEX_BIN", "CLAUDE_CODE_EXECUTABLE"} {
		if v := os.Getenv(k); v != "" {
			l.Env[k] = v
		}
	}
	if l.Env["FACTORY_TOKEN"] == "" && l.Env[node.TokenEnv] == "" {
		l.Env["FACTORY_TOKEN"] = "REPLACE_WITH_FACTORY_TOKEN"
	}
	if err = l.Render(os.Stdout); err != nil {
		return 1, err
	}
	return 0, nil
}

// systemd prints a systemd service for `run`. Secrets are not inlined: FACTORY_TOKEN and NATS_URL go into the
// 0600 environment file the unit names.
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
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return 1, err
	}
	s := node.Systemd{Description: "App Factory node agent (" + *name + ")", Executable: exe, WorkingDir: wd, System: *system, User: *usr, Env: map[string]string{}}
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
	fmt.Fprintf(os.Stderr, "save as %s/%s.service; put FACTORY_NODE_TOKEN=... (factory nodes token create; or the shared FACTORY_TOKEN=...) and NATS_URL=... in %s (chmod 600); then %s daemon-reload && %s enable --now %s\n",
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
	path := fs.String("config", "", "node.yaml whose nats section the NATS check uses (default $"+node.ConfigEnv+"; else NATS_URL alone)")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	h := node.LocalHost(runtime.GOOS)
	d := node.Doctor{Host: h, DiskPath: *disk, Low: uint64(*low * node.GiB), Critical: uint64(*critical * node.GiB), Repo: *repo}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	d.NATS, d.Labels = c.NATS, c.Labels
	if d.Token, d.TokenSource, err = c.Token(os.Getenv); err != nil {
		return 2, err
	}
	// Stack checks of the catalog's NodeHooks document, as Core last delivered it to this node.
	if hooks, ok, err := node.LoadHooks(c.StateDir); err != nil {
		fmt.Fprintf(os.Stderr, "stack checks skipped: %s: %v\n", filepath.Join(c.StateDir, node.HooksFile), err)
	} else if ok {
		d.Stack = hooks.Spec.Checks
	}
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
