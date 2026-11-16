package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"appfactory/internal/node"
)

// Runtime logins of the node (node-runtime-accounts): `factory-node login claude|codex` logs the node's own
// configuration directory in (the runtime's own login flow, in this terminal) and writes it to node.yaml, so the
// person's terminal login no longer decides the agents' account; `factory-node accounts` shows the recorded
// accounts and `accounts accept RUNTIME` continues with a changed one.

// runtimeLogin runs the runtime's login against the node's directory and records the directory in node.yaml.
func runtimeLogin(runtime string, args []string) (int, error) {
	fs := flag.NewFlagSet("login "+runtime, flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+")")
	copyCurrent := fs.Bool("copy-current", false, "start from this user's current login instead of a new one (Codex only)")
	deviceAuth := fs.Bool("device-auth", false, "sign in with a code entered in any browser, for a machine without one (Codex only)")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2, errors.New("usage: factory-node login claude|codex [-config node.yaml] [-copy-current] [-device-auth]")
	}
	cfgPath := node.ConfigPath(*path)
	c, err := node.LoadConfig(cfgPath)
	if err != nil {
		return 2, err
	}
	home, _ := os.UserHomeDir()
	base := filepath.Join(filepath.Dir(c.StateDir), "runtimes")
	if c.StateDir == "" {
		base = filepath.Join(home, ".app-factory", "runtimes")
	}
	var dir, key string
	var cmd *exec.Cmd
	switch runtime {
	case "claude":
		if *deviceAuth || *copyCurrent {
			return 2, errors.New("-device-auth and -copy-current are for codex: Claude Code's /login already prints a link and asks for the code")
		}
		dir, key = node.ExpandHome(c.Drivers.Claude.ConfigDir), "config_dir"
		if dir == "" {
			dir = filepath.Join(base, "claude")
		}
		bin := c.Drivers.Claude.Executable
		if bin == "" {
			bin = claudeBinary(c)
		}
		cmd = exec.Command(bin, "/login")
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	case "codex":
		dir, key = node.ExpandHome(c.Drivers.Codex.Home), "home"
		if dir == "" {
			dir = filepath.Join(base, "codex")
		}
		bin := node.CodexBinary(c, os.Getenv) // the Codex the node runs (its packaged one), not whatever is on PATH
		if *copyCurrent {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return 1, err
			}
			b, err := os.ReadFile(filepath.Join(home, ".codex", "auth.json"))
			if err != nil {
				return 1, fmt.Errorf("no current Codex login to copy: %w", err)
			}
			if err = os.WriteFile(filepath.Join(dir, "auth.json"), b, 0o600); err != nil {
				return 1, err
			}
			cmd = exec.Command(bin, "login", "status")
		} else if *deviceAuth {
			cmd = exec.Command(bin, "login", "--device-auth")
		} else {
			cmd = exec.Command(bin, "login")
		}
		cmd.Env = append(os.Environ(), "CODEX_HOME="+dir)
	default:
		return 2, errors.New("usage: factory-node login claude|codex")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 1, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	fmt.Printf("Logging the node's %s in (its own login in %s; your own terminal login stays as it is).\n", runtime, dir)
	if err := cmd.Run(); err != nil {
		// Claude Code goes on after /login (onboarding, the folder trust question) and exits non-zero when one of
		// those is declined: the login it saved in dir still counts.
		if runtime != "claude" || !claudeLoggedIn(cmd.Path, dir) {
			return 1, fmt.Errorf("%s login: %w", runtime, err)
		}
		fmt.Printf("Claude Code exited (%v) after the login; the login is saved in %s.\n", err, dir)
	}
	if err := setDriverKey(cfgPath, runtime, key, dir); err != nil {
		return 1, fmt.Errorf("logged in, but node.yaml was not updated (set drivers.%s.%s: %q yourself): %w", runtime, key, dir, err)
	}
	_ = node.AcceptAccount(node.AccountsDir(c.StateDir), runtime) // the new login is the account to remember
	fmt.Printf("node.yaml: drivers.%s.%s = %s. Restart the node to use it (launchctl kickstart -k gui/$(id -u)/com.appfactory.node, or systemctl --user restart factory-node).\n", runtime, key, dir)
	return 0, nil
}

// claudeLoggedIn reports whether Claude Code in dir has the subscription login /login saves (`claude auth status`:
// loggedIn with authMethod claude.ai), not a token from the environment, which runs do not use.
func claudeLoggedIn(bin, dir string) bool {
	cmd := exec.Command(bin, "auth", "status")
	cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+dir)
	out, _ := cmd.Output() // exit 1 when logged out; the JSON says it either way
	var st struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	return json.Unmarshal(out, &st) == nil && st.LoggedIn && st.AuthMethod == "claude.ai"
}

// claudeBinary is the Claude Code binary the shim drives: the SDK-bundled one next to the node.
func claudeBinary(c node.Config) string {
	binDir := c.BinDir
	if binDir == "" {
		if exe, err := os.Executable(); err == nil {
			binDir = filepath.Dir(exe)
		}
	}
	// The package's Claude Code (node-packaging-updates), else the one of a node built from the repository.
	if exe := filepath.Join(binDir, "..", "claude-code", "claude"); fileExists(exe) {
		return exe
	}
	matches, _ := filepath.Glob(filepath.Join(binDir, "..", "workers", "claude", "node_modules", "@anthropic-ai", "claude-agent-sdk-*", "claude"))
	if len(matches) > 0 {
		return matches[0]
	}
	return "claude"
}

// setDriverKey writes drivers.<runtime>.<key>: value into node.yaml: in the runtime's flow mapping
// (`  claude: {enabled: true, …}`) or block, else a new block under drivers.
func setDriverKey(path, runtime, key, value string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	entry := fmt.Sprintf("%s: %q", key, value)
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if !strings.HasPrefix(t, runtime+":") || !strings.HasPrefix(l, "  ") || strings.HasPrefix(l, "   ") {
			continue
		}
		if open, close := strings.Index(l, "{"), strings.LastIndex(l, "}"); open >= 0 && close > open {
			inner := strings.TrimSpace(l[open+1 : close])
			if strings.Contains(inner, entry) {
				return nil // a login again into the node's own directory (another account): nothing to change
			}
			if strings.Contains(inner, key+":") {
				return fmt.Errorf("drivers.%s.%s is already set", runtime, key)
			}
			if inner != "" {
				inner += ", "
			}
			lines[i] = l[:open+1] + inner + entry + l[close:]
		} else {
			for _, b := range lines[i+1:] {
				if !strings.HasPrefix(b, "   ") {
					break
				}
				switch bt := strings.TrimSpace(b); {
				case bt == entry:
					return nil
				case strings.HasPrefix(bt, key+":"):
					return fmt.Errorf("drivers.%s.%s is already set", runtime, key)
				}
			}
			lines = append(lines[:i+1], append([]string{"    " + entry}, lines[i+1:]...)...)
		}
		return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
	}
	return fmt.Errorf("drivers.%s not found in %s", runtime, path)
}

// accounts shows the recorded runtime accounts, or accepts a changed one.
func accounts(args []string) (int, error) {
	fs := flag.NewFlagSet("accounts", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+")")
	usage := errors.New("usage: factory-node accounts [-config node.yaml] [accept claude|codex]")
	// accept RUNTIME may come before the flags too (accounts accept codex -config node.yaml).
	var accept string
	if len(args) >= 2 && args[0] == "accept" {
		accept, args = args[1], args[2:]
	}
	if err := fs.Parse(args); err != nil {
		return 2, err
	}
	if accept == "" && fs.NArg() == 2 && fs.Arg(0) == "accept" {
		accept = fs.Arg(1)
	} else if fs.NArg() != 0 {
		return 2, usage
	}
	if accept != "" && accept != "claude" && accept != "codex" {
		return 2, usage
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	dir := node.AccountsDir(c.StateDir)
	if accept != "" {
		if err := node.AcceptAccount(dir, accept); err != nil {
			return 1, err
		}
		fmt.Printf("%s: the next check records the account it is logged in to now and runs %s agents again (within the probe interval, or restart the node).\n", accept, accept)
		return 0, nil
	}
	for _, rt := range []string{"claude", "codex"} {
		if a, ok := node.LoadAccount(dir, rt); ok {
			fmt.Printf("%-7s %s %s (%s)\n", rt, a.Plan, a.Org, a.Hash)
		} else {
			fmt.Printf("%-7s not recorded yet\n", rt)
		}
	}
	return 0, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
