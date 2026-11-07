package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"appfactory/internal/node"
)

// What the person on the machine controls without Core (connector-trust): pause and resume this node at once,
// read what ran here, and remove the node.

func localConfig(name string, args []string) (node.Config, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+")")
	if err := fs.Parse(args); err != nil {
		return node.Config{}, fs, err
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	return c, fs, err
}

// pauseLocal: factory-node pause [REASON] | resume. Live runs finish; no new run starts while paused.
func pauseLocal(on bool, args []string) (int, error) {
	c, fs, err := localConfig("pause", args)
	if err != nil {
		return 2, err
	}
	if err = node.SetLocalPause(c.StateDir, on, strings.Join(fs.Args(), " ")); err != nil {
		return 1, err
	}
	if on {
		fmt.Println("Paused: this node takes no new runs (runs in progress finish). Resume with factory-node resume.")
	} else {
		fmt.Println("Resumed: this node takes runs again.")
	}
	return 0, nil
}

// activity: factory-node activity [-n 50] — the runs this machine took, from its local log.
func activity(args []string) (int, error) {
	fs := flag.NewFlagSet("activity", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml")
	n := fs.Int("n", 50, "lines")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	list, err := node.ReadActivity(c.StateDir, *n)
	if err != nil {
		return 1, err
	}
	if len(list) == 0 {
		fmt.Println("No activity recorded yet.")
	}
	for _, a := range list {
		fmt.Printf("%s  %-12s %s %s\n", a.At.Local().Format("2006-01-02 15:04:05"), a.Event, a.Run, a.Runtime)
	}
	if node.LocallyPaused(c.StateDir) {
		fmt.Println("(this node is paused locally)")
	}
	return 0, nil
}

// uninstall: factory-node uninstall [-keep-state] — stops the service and removes the node's files. The company
// keeps the node until an owner removes it on the Nodes page (its keys are deleted here, so it cannot connect).
func uninstall(args []string) (int, error) {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml")
	keep := fs.Bool("keep-state", false, "keep state_dir (worktrees, logs) and node.yaml")
	yes := fs.Bool("yes", false, "do not ask")
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	cfgPath := node.ConfigPath(*path)
	c, err := node.LoadConfig(cfgPath)
	if err != nil {
		return 2, err
	}
	home := filepath.Dir(c.StateDir)
	if c.StateDir == "" || filepath.Base(home) != ".app-factory" {
		return 2, fmt.Errorf("the node's files are not under ~/.app-factory (state_dir %q): remove them by hand", c.StateDir)
	}
	if !*yes {
		fmt.Printf("This stops the node service and deletes %s%s. Type yes to continue: ", home, map[bool]string{true: " except state and node.yaml", false: ""}[*keep])
		var answer string
		_, _ = fmt.Scanln(&answer)
		if answer != "yes" {
			return 1, errors.New("cancelled")
		}
	}
	switch runtime.GOOS {
	case "darwin":
		uid := fmt.Sprint(os.Getuid())
		_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/com.appfactory.node").Run()
		h, _ := os.UserHomeDir()
		_ = os.Remove(filepath.Join(h, "Library", "LaunchAgents", "com.appfactory.node.plist"))
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "factory-node").Run()
		conf := os.Getenv("XDG_CONFIG_HOME")
		if conf == "" {
			h, _ := os.UserHomeDir()
			conf = filepath.Join(h, ".config")
		}
		_ = os.Remove(filepath.Join(conf, "systemd", "user", "factory-node.service"))
	}
	for _, d := range []string{"bin", "workers", "codex", "runtimes"} {
		_ = os.RemoveAll(filepath.Join(home, d))
	}
	if !*keep {
		_ = os.RemoveAll(c.StateDir)
		_ = os.Remove(cfgPath)
	}
	fmt.Println("The node is removed from this machine. Remove it from the company on the Nodes page too.")
	return 0, nil
}
