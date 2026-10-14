// factory-shim enforces command access rules for one run: the executor links each guarded command to
// it in a directory first on the runtime PATH (internal/node/shim). Run by its own name,
// `factory-shim --decide` decides one shell command line for a runtime hook (shim.DecideFlag).
package main

import (
	"context"
	"os"
	"syscall"

	"appfactory/internal/node/shim"
)

func main() {
	os.Exit(shim.Shim{Getenv: os.Getenv, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Exec: syscall.Exec, Environ: os.Environ}.Main(context.Background(), os.Args))
}
