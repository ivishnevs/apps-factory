// codex-worker is `factory-node run --slots 1` with the Codex driver only, configured from the
// environment as before (CODEX_BIN, FACTORY_WORKER_STATE, NATS_URL, FACTORY_URL, FACTORY_TOKEN).
package main

import (
	"appfactory/internal/node"
	"appfactory/internal/node/runner"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	c := node.Defaults()
	c.Name, c.Slots = "codex-worker", 1 // defaults enable Codex only
	if v := os.Getenv("FACTORY_WORKER_STATE"); v != "" {
		c.StateDir = v
	} else {
		c.StateDir = "data/codex-worker"
	}
	if err := c.Validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runner.Main(ctx, c, os.Getenv)
}
