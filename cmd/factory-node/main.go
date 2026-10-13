// factory-node is the node agent (openspec change node-agent). Only `doctor` exists so far: it runs the
// local self-checks and works without Core.
package main

import (
	"appfactory/internal/driver/codex"
	"appfactory/internal/node"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"
)

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "factory-node:", err)
	}
	os.Exit(code)
}

func run(args []string) (int, error) {
	if len(args) == 0 || args[0] != "doctor" {
		return 2, fmt.Errorf("usage: factory-node doctor [-disk-path DIR] [-low-gib N] [-critical-gib N] [-repo URL] [-drivers codex,claude]")
	}
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	disk := fs.String("disk-path", home, "workspace root whose free space is checked")
	low := fs.Float64("low-gib", 20, "disk.low_watermark in GiB")
	critical := fs.Float64("critical-gib", 5, "disk.critical_watermark in GiB")
	repo := fs.String("repo", "", "repository URL checked with git ls-remote (optional)")
	drivers := fs.String("drivers", "", "comma list of drivers to require (default: every installed one)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2, err
	}
	h := node.LocalHost(runtime.GOOS)
	d := node.Doctor{Host: h, DiskPath: *disk, Low: uint64(*low * node.GiB), Critical: uint64(*critical * node.GiB), Repo: *repo}
	// Each driver owns its probe; the Claude probe moves into internal/driver/claude with that driver.
	d.Runtimes = []node.Runtime{{Name: "codex", Probe: codex.Driver{Binary: os.Getenv("CODEX_BIN"), Host: &h}},
		{Name: "claude", Probe: node.ClaudeProbe{Host: h, BinDir: d.BinDir()}}}
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
