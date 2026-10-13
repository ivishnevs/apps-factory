// factory-workspace lets non-Go Workers reuse the canonical workspace writer lock and
// OpenSpec preflight. It prints one JSON line, then holds the lock until stdin closes,
// so a crashed Worker releases the workspace together with its pipe.
package main

import (
	"appfactory/internal/openspec"
	"appfactory/internal/workspace"
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
)

type reply struct {
	Path  string `json:"path,omitempty"`
	Kind  string `json:"kind,omitempty"`
	Error string `json:"error,omitempty"`
}

func main() {
	change := flag.String("openspec-change", "", "active OpenSpec change to preflight")
	flag.Parse()
	if flag.NArg() != 1 {
		exit(reply{Kind: "usage", Error: "usage: factory-workspace [-openspec-change NAME] WORKSPACE"}, 2)
	}
	allocation, err := (workspace.Local{}).Acquire(context.Background(), flag.Arg(0))
	if err != nil {
		exit(reply{Kind: "workspace", Error: err.Error()}, 1)
	}
	defer allocation.Release()
	if *change != "" {
		if _, err = (openspec.Access{}).CheckChange(allocation.Path, *change); err != nil {
			allocation.Release()
			exit(reply{Kind: "openspec", Error: err.Error()}, 1)
		}
	}
	if err = json.NewEncoder(os.Stdout).Encode(reply{Path: allocation.Path}); err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}
func exit(r reply, code int) {
	_ = json.NewEncoder(os.Stdout).Encode(r)
	os.Exit(code)
}
