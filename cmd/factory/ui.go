package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"appfactory/internal/tui"
	"appfactory/internal/worker"
)

const uiUsage = "ui [--demo | --fixture PATH | --record PATH] [--notify desktop|bell|off] [--claude-config-dir DIR] [--codex-home DIR]"

// uiCommand runs the owner's terminal UI against Core (FACTORY_URL, FACTORY_TOKEN), against a recorded fixture
// (--fixture, --demo: no Core needed), or records Core's current state into a fixture (--record). --notify
// says how a new question, approval or escalation for the owner notifies (a macOS notification by default,
// the terminal bell elsewhere); --claude-config-dir and --codex-home find runs' local session files when the
// UI runs on a node's machine (defaults: $CLAUDE_CONFIG_DIR or ~/.claude, $CODEX_HOME or ~/.codex).
func uiCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	demo := fs.Bool("demo", false, "")
	fixture := fs.String("fixture", "", "")
	record := fs.String("record", "", "")
	notify := fs.String("notify", tui.NotifyDesktop, "")
	claudeDir := fs.String("claude-config-dir", "", "")
	codexHome := fs.String("codex-home", "", "")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		return fmt.Errorf("usage: %s", uiUsage)
	}
	modes := 0
	for _, on := range []bool{*demo, *fixture != "", *record != ""} {
		if on {
			modes++
		}
	}
	switch *notify {
	case tui.NotifyDesktop, tui.NotifyBell, tui.NotifyOff:
	default:
		modes = 2
	}
	if modes > 1 {
		return fmt.Errorf("usage: %s", uiUsage)
	}
	mode, path := "live", ""
	switch {
	case *demo:
		mode = "demo"
	case *fixture != "":
		mode, path = "fixture", *fixture
	case *record != "":
		mode, path = "record", *record
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
	m := tui.New(tui.Options{Client: client, Now: now, Notify: *notify,
		Local: tui.LocalTranscripts{ClaudeDir: *claudeDir, CodexHome: *codexHome, Off: mode != "live"}})
	defer m.Close()
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}
