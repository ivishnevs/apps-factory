// factory-shim enforces command access rules for one run: the executor links each guarded command to
// it in a directory first on the runtime PATH (internal/node/shim).
package main

import (
	"context"
	"os"
	"syscall"

	"appfactory/internal/node/shim"
)

func main() {
	os.Exit(shim.Shim{Getenv: os.Getenv, Stderr: os.Stderr, Exec: syscall.Exec, Environ: os.Environ}.Main(context.Background(), os.Args))
}
