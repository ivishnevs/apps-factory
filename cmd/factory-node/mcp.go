package main

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"appfactory/internal/factorytools"
	"appfactory/internal/node/shim"
)

// mcpMain serves Factory MCP for one run (docs/explicit-exits-contract.md): tool calls go to Core with the run
// credential and ack_seq, exit tools carry the node's git facts. It was cmd/factory-mcp (node-packaging-updates A1).
func mcpMain() int {
	a, err := factorytools.AdapterFromEnv(os.Getenv)
	if err == nil {
		err = factorytools.ServeWith(context.Background(), os.Stdin, os.Stdout, a.List, a.Call, a.Delivered)
		a.Wait() // a queued exit is still delivered after the runtime closed stdin
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

// shimMain enforces command access rules for one run: the executor links each guarded command to factory-shim in a
// directory first on the runtime PATH (internal/node/shim); run as factory-shim, `--decide` decides one shell command
// line for a runtime hook (shim.DecideFlag). It was cmd/factory-shim (node-packaging-updates A1).
func shimMain(args []string) int {
	return shim.Shim{Getenv: os.Getenv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Exec: syscall.Exec, Environ: os.Environ}.Main(context.Background(), args)
}
