package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"appfactory/internal/node"
	pinned "appfactory/internal/node/runtimes"
)

// `factory-node runtimes update [claude|codex]` (node-packaging-updates 2.4, 3.3) installs the runtimes the package
// pins: Claude Code from claude-code/claude.lock (sha512) and the standalone Codex from codex/codex.lock (sha256), into
// the package directory, on request (install.sh runs it; a node update fetches the new version's runtimes the same
// way before it switches). Logins are untouched. -home names the package directory (default: the running one).
func runtimes(args []string) (int, error) {
	const use = "usage: factory-node runtimes update [claude|codex] [-config node.yaml] [-home DIR] [-prev DIR]"
	if len(args) == 0 || args[0] != "update" {
		return 2, errors.New(use)
	}
	args = args[1:]
	which := []string{"claude", "codex"}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		if args[0] != "claude" && args[0] != "codex" {
			return 2, errors.New(use)
		}
		which, args = args[:1], args[1:]
	}
	fs := flag.NewFlagSet("runtimes update", flag.ContinueOnError)
	path := fs.String("config", "", "node.yaml (default $"+node.ConfigEnv+")")
	homeFlag := fs.String("home", "", "the package directory (default: the one this executable is in)")
	prev := fs.String("prev", "", "a package directory whose runtimes are reused when they have the same pin")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return 2, errors.New(use)
	}
	c, err := node.LoadConfig(node.ConfigPath(*path))
	if err != nil {
		return 2, err
	}
	home := *homeFlag
	if home == "" {
		home = packageHome(c)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	in := pinned.Installer{}
	failed := false
	for _, rt := range which {
		var msg string
		switch rt {
		case "claude":
			msg, err = in.Claude(ctx, home, *prev, pinned.Libc())
		case "codex":
			if own := ownCodex(home, c.Drivers.Codex.Binary); own != "" {
				msg, err = "", fmt.Errorf("Codex is your own install (%s): the node keeps using it; remove drivers.codex.binary from node.yaml to use the pinned one", own)
				break
			}
			msg, err = in.Codex(ctx, home, *prev, pinned.Platform())
		}
		if err != nil {
			failed = true
			fmt.Printf("%s: %v\n", rt, err)
			continue
		}
		fmt.Printf("%s: %s\n", rt, msg)
	}
	if failed {
		return 1, nil
	}
	fmt.Println("Restart the node to use them: they are checked again at start (factory-node doctor).")
	return 0, nil
}

// ownCodex is drivers.codex.binary when it is a Codex outside the node's package (the person's own install).
func ownCodex(home, binary string) string {
	if binary == "" {
		return ""
	}
	b := node.ExpandHome(binary)
	for _, base := range []string{home, filepath.Dir(filepath.Dir(home))} { // versions/<v>, and the node's home
		if strings.HasPrefix(b, base+string(filepath.Separator)) {
			return ""
		}
	}
	return binary
}

// packageHome is the installed package's directory (the parent of bin), resolved through the links of the versioned
// layout (<home>/bin -> current/bin -> versions/<v>/bin).
func packageHome(c node.Config) string {
	binDir := c.BinDir
	if binDir == "" {
		if exe, err := os.Executable(); err == nil {
			binDir = filepath.Dir(exe)
		}
	}
	if real, err := filepath.EvalSymlinks(binDir); err == nil {
		binDir = real
	}
	return filepath.Dir(binDir)
}
