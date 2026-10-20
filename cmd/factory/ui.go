package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"appfactory/internal/tui"
	"appfactory/internal/worker"
)

const uiUsage = "ui [--demo | --fixture PATH | --record PATH]"

// uiCommand runs the owner's terminal UI against Core (FACTORY_URL, FACTORY_TOKEN), against a recorded fixture
// (--fixture, --demo: no Core needed), or records Core's current state into a fixture (--record).
func uiCommand(ctx context.Context, args []string) error {
	mode, path := "live", ""
	switch {
	case len(args) == 0:
	case len(args) == 1 && args[0] == "--demo":
		mode = "demo"
	case len(args) == 2 && (args[0] == "--fixture" || args[0] == "--record"):
		mode, path = args[0][2:], args[1]
	default:
		return fmt.Errorf("usage: %s", uiUsage)
	}
	var client tui.Client
	now := time.Now
	switch mode {
	case "demo", "fixture":
		f := tui.DemoFixture()
		if mode == "fixture" {
			var err error
			if f, err = tui.LoadFixture(path); err != nil {
				return err
			}
		}
		// The fixture's moment is "now" when the UI starts; time moves on from there.
		start, at := time.Now(), f.Overview.At
		if at.IsZero() {
			at = start
		}
		now = func() time.Time { return at.Add(time.Since(start)) }
		client = &tui.FixtureClient{F: f, Delay: 400 * time.Millisecond}
	default:
		if os.Getenv("FACTORY_TOKEN") == "" {
			return fmt.Errorf("FACTORY_TOKEN required (or factory ui --demo)")
		}
		c := &tui.HTTPClient{API: worker.API{URL: env("FACTORY_URL", "http://127.0.0.1:8080"), Token: os.Getenv("FACTORY_TOKEN")}}
		if mode == "record" {
			f, err := tui.Record(ctx, c)
			if err != nil {
				return err
			}
			if err = tui.SaveFixture(path, f); err != nil {
				return err
			}
			fmt.Printf("recorded %d tasks, %d feed rows, %d runs into %s\n", len(f.Overview.Tasks), len(f.Feed), len(f.Runs), path)
			return nil
		}
		client = c
	}
	m := tui.New(tui.Options{Client: client, Now: now})
	defer m.Close()
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}
