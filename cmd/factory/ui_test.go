package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/tui"
)

func TestUICommandArguments(t *testing.T) {
	ctx := context.Background()
	for _, args := range [][]string{{"--bogus"}, {"--fixture"}, {"--demo", "x"}, {"--demo", "--fixture", "f.json"}, {"--demo", "--notify", "loud"}} {
		if err := uiCommand(ctx, args); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Fatal(args, err)
		}
	}
	t.Setenv("FACTORY_TOKEN", "")
	if err := uiCommand(ctx, nil); err == nil || !strings.Contains(err.Error(), "FACTORY_TOKEN required") {
		t.Fatal(err)
	}
	if err := uiCommand(ctx, []string{"--fixture", filepath.Join(t.TempDir(), "none.json")}); err == nil {
		t.Fatal("missing fixture accepted")
	}
}

// TestUIRecord: factory ui --record writes a fixture of Core's state that --fixture loads.
func TestUIRecord(t *testing.T) {
	demo := tui.DemoFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rec-token" {
			http.Error(w, "unauthorized", 401)
			return
		}
		var v any
		switch r.URL.Path {
		case "/health":
			v = demo.Overview.Health
		case "/status":
			v = demo.Overview.Status[:0]
		case "/agents", "/tasks", "/pauses", "/nodes", "/inbox", "/approvals":
			v = []any{}
		case "/feed":
			v = map[string]any{"items": demo.Feed[:3], "next_after": 3, "has_more": false}
		default:
			v = map[string]any{}
		}
		_ = json.NewEncoder(w).Encode(v)
	}))
	defer srv.Close()
	t.Setenv("FACTORY_URL", srv.URL)
	t.Setenv("FACTORY_TOKEN", "rec-token")
	path := filepath.Join(t.TempDir(), "rec.json")
	if err := uiCommand(context.Background(), []string{"--record", path}); err != nil {
		t.Fatal(err)
	}
	f, err := tui.LoadFixture(path)
	if err != nil || len(f.Feed) != 3 || f.Overview.Health.Company != "Company 1" {
		t.Fatal(err, f)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "rec-token") {
		t.Fatal("token recorded")
	}
}
