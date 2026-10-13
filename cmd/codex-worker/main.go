package main

import (
	"appfactory/internal/bus"
	"appfactory/internal/worker"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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
	binary := env("CODEX_BIN", "codex")
	version, err := exec.Command(binary, "--version").Output()
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(version)) != "codex-cli 0.162.1" {
		return fmt.Errorf("unsupported Codex %q: expected 0.162.1; regenerate and verify schema before upgrade", strings.TrimSpace(string(version)))
	}
	if os.Getenv("FACTORY_TOKEN") == "" {
		return fmt.Errorf("FACTORY_TOKEN required")
	}
	b, err := bus.Connect(env("NATS_URL", "nats://127.0.0.1:4222"))
	if err != nil {
		return err
	}
	defer b.NC.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	w := worker.Worker{Bus: b, API: worker.API{URL: env("FACTORY_URL", "http://127.0.0.1:8080"), Token: os.Getenv("FACTORY_TOKEN")}, Binary: binary}
	return w.Run(ctx)
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
