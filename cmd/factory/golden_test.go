package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

var update = flag.Bool("update", false, "rewrite the console golden files")

var randomID = regexp.MustCompile(`[0-9a-f]{32}`)

// TestConsoleGolden (waits-events-cost-cli 5.1): status, log, show and watch on a seeded
// database with a fixed clock; random IDs are masked.
func TestConsoleGolden(t *testing.T) {
	local := time.Local
	time.Local = time.UTC
	defer func() { time.Local = local }()
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC))
	s.Clock = fake
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Agents: agents, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "Calories", Workspace: "/tmp/golden"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "a", ProjectID: "p", Title: "Build screen", Assignee: "demo", Ready: true, RequestID: "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "b", ProjectID: "p", Title: "Test screen", Assignee: "demo", Dependencies: []string{"a"}, Ready: true, RequestID: "b"}); err != nil {
		t.Fatal(err)
	}
	// a: a failed run with usage (retry after the backoff), then a run asking the owner.
	fake.Advance(time.Minute)
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Task(ctx, co, "a")
	lease, err := s.Claim(ctx, co, a.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	fake.Advance(30 * time.Second)
	if err = s.Apply(ctx, co, protocol.Wrap("agent.failed", a.CurrentRunID, "", protocol.Event{RunID: a.CurrentRunID, Lease: lease, Status: "ENDED", Outcome: "failed", Result: "build failed",
		Usage: []protocol.Usage{{Model: "gpt-5", Input: 120_000, Output: 8_000}}})); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PriceRunCosts(ctx, co, config.Prices{"gpt-5": {Input: 1.25, Output: 10}}); err != nil {
		t.Fatal(err)
	}
	fake.Advance(2 * time.Minute)
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Task(ctx, co, "a")
	if _, err = s.Claim(ctx, co, a.CurrentRunID); err != nil {
		t.Fatal(err)
	}
	fake.Advance(10 * time.Second)
	if _, err = s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "a", RunID: a.CurrentRunID, Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Metric or imperial?", Blocking: true, RequestID: "q"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CommentTask(ctx, co, "a", tasks.Comment{Text: "keep it simple", RequestID: "c1"}); err != nil {
		t.Fatal(err)
	}

	for name, args := range map[string][]string{
		"status": {"status", "p"},
		"log":    {"log", "a"},
		"show":   {"show", "a"},
		"watch":  {"watch", "p", "--attention"},
	} {
		got := capture(t, func() error {
			wctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			return consoleCommand(wctx, api, args)
		})
		got = randomID.ReplaceAllString(got, "<id>")
		path := filepath.Join("testdata", "console_"+name+".golden")
		if *update {
			if err = os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err, "(run go test ./cmd/factory -run TestConsoleGolden -update)")
		}
		if got != string(want) {
			t.Errorf("%s differs from %s:\n%s", name, path, got)
		}
	}
	// --json prints JSON (one object per event for watch); its content is the API's, not pinned here.
	for _, args := range [][]string{{"status", "p", "--json"}, {"log", "a", "--json"}, {"show", "a", "--json"}, {"watch", "p", "--json"}} {
		got := capture(t, func() error {
			wctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			return consoleCommand(wctx, api, args)
		})
		dec := json.NewDecoder(strings.NewReader(got))
		for dec.More() {
			var v any
			if err = dec.Decode(&v); err != nil {
				t.Fatal(args, err, got)
			}
		}
		if args[0] == "show" && !strings.Contains(got, `"costs"`) {
			t.Fatal(got)
		}
	}
}
