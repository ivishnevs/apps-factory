package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"appfactory/internal/worker"
)

// TestProjectsCreateArguments (project-creation): the workspace is optional, --repo-mode, --name and --audit
// reach POST /projects; a workspace without --repo-mode keeps today's behaviour (no onboarding).
func TestProjectsCreateArguments(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	for _, c := range []struct {
		args []string
		want map[string]any
	}{
		{[]string{"projects", "create", "advent-gift", "A gift"}, map[string]any{"id": "advent-gift", "name": "advent-gift", "description": "A gift"}},
		{[]string{"projects", "create", "advent-gift", "--repo-mode", "github", "--name", "Advent gift"}, map[string]any{"name": "Advent gift", "repo_mode": "github"}},
		{[]string{"projects", "create", "kick", "/Users/owner/projects/kick", "Climbing", "--repo-mode", "existing", "--audit"}, map[string]any{"workspace": "/Users/owner/projects/kick", "description": "Climbing", "repo_mode": "existing", "audit": true}},
		{[]string{"projects", "create", "ideas", "/Users/owner/projects/ideas"}, map[string]any{"workspace": "/Users/owner/projects/ideas", "repo_mode": nil}},
		{[]string{"projects", "create", "remote", "--repo", "https://github.com/o/remote.git", "--branch", "main"}, map[string]any{"repo_url": "https://github.com/o/remote.git", "default_branch": "main"}},
	} {
		if err := engineCommand(context.Background(), api, c.args); err != nil {
			t.Fatal(c.args, err)
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Fatal(c.args, k, got)
			}
		}
		if c.want["workspace"] == nil && got["workspace"] != nil || got["request_id"] == "" {
			t.Fatal(c.args, got)
		}
	}
	if err := engineCommand(context.Background(), api, []string{"projects", "create", "x", "a", "b"}); err == nil {
		t.Fatal("two descriptions accepted")
	}
}
