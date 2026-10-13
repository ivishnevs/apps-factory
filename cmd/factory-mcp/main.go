package main

import (
	"appfactory/internal/factorytools"
	"appfactory/internal/worker"
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
func run() error {
	token := os.Getenv("FACTORY_RUN_TOKEN")
	base := os.Getenv("FACTORY_URL")
	if token == "" || base == "" {
		return fmt.Errorf("FACTORY_RUN_TOKEN and FACTORY_URL required")
	}
	api := worker.API{URL: base, Token: token}
	return factorytools.Serve(context.Background(), os.Stdin, os.Stdout, func(ctx context.Context) (any, error) {
		var v any
		err := api.Do(ctx, "GET", "/agent/tools", nil, &v)
		return v, err
	}, func(ctx context.Context, p factorytools.Call) (any, error) {
		var v any
		err := api.Do(ctx, "POST", "/agent/call", p, &v)
		return v, err
	})
}
