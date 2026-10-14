package main

import (
	"appfactory/internal/factorytools"
	"context"
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run serves Factory MCP for one run (docs/explicit-exits-contract.md): tool calls go to Core with the
// run credential and ack_seq, exit tools carry the node's git facts.
func run() error {
	a, err := factorytools.AdapterFromEnv(os.Getenv)
	if err != nil {
		return err
	}
	err = factorytools.ServeWith(context.Background(), os.Stdin, os.Stdout, a.List, a.Call, a.Delivered)
	a.Wait() // a queued exit is still delivered after the runtime closed stdin
	return err
}
