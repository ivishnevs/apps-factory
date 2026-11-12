package runner

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver"
	"appfactory/internal/driver/fake"
	"appfactory/internal/node"
	"appfactory/internal/secrets"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestAgentSecretsEndToEnd (agent-secrets-ui 4.1): Core over HTTP with its stored catalog and a real node with a
// fake runtime. The owner adds a file secret, gives it to an agent through the agent editor's route (a catalog
// change), and the agent's next run finds the file at the path in the variable; after the owner removes the use, the
// next run gets neither the variable nor the file.
func TestAgentSecretsEndToEnd(t *testing.T) {
	bin := t.TempDir()
	buildNodeBin(t, bin)
	beat := storage.HeartbeatSeconds
	storage.HeartbeatSeconds = 1
	defer func() { storage.HeartbeatSeconds = beat }()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	s.Keys, _ = secrets.NewMasterKeys(bytes.Repeat([]byte{3}, secrets.KeySize), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c := &core.Core{Store: s, Root: "../../..", Token: "test-token"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	if err = c.LoadCatalogs(ctx); err != nil {
		t.Fatal(err)
	}
	go c.Run(ctx)
	co := storage.DefaultCompany
	owner := func(method, path string, body any) string {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, method, server.URL+path, bytes.NewReader(b))
		req.Header.Set("Authorization", "Bearer test-token")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatal(method, path, res.StatusCode, string(out))
		}
		return string(out)
	}
	key := "-----BEGIN OPENSSH PRIVATE KEY-----\nplaytest-server-key\n-----END OPENSSH PRIVATE KEY-----\n"
	owner("PUT", "/secrets/GAME_SERVER_SSH_KEY", core.SecretPut{Kind: storage.SecretFile, Value: base64.StdEncoding.EncodeToString([]byte(key))})
	var view core.AgentSecrets
	if err = json.Unmarshal([]byte(owner("GET", "/catalog/agents/demo/secrets", nil)), &view); err != nil {
		t.Fatal(err)
	}
	owner("PUT", "/catalog/agents/demo/secrets", core.AgentSecretsPut{Base: view.Base, Secrets: []config.SecretUse{{Name: "GAME_SERVER_SSH_KEY", File: true}}})

	// The runtime copies what it finds at the variable's path into probe (or "unset").
	probe := filepath.Join(t.TempDir(), "probe")
	d := &fake.Driver{Script: fake.Script{Status: "COMPLETED", Text: "done", Activity: []fake.Activity{{Exec: []string{"sh", "-c",
		`if [ -n "$GAME_SERVER_SSH_KEY" ] && [ -f "$GAME_SERVER_SSH_KEY" ]; then cat "$GAME_SERVER_SSH_KEY" >"$0"; else echo unset >"$0"; fi`, probe}}}}}
	cfg := node.Defaults()
	cfg.Slots, cfg.StateDir, cfg.BinDir, cfg.Poll.WaitSeconds = 1, t.TempDir(), bin, 2
	t.Cleanup(func() { // the node's skill store is read-only (worker.readOnly); let TempDir remove it
		_ = filepath.WalkDir(cfg.StateDir, func(p string, e os.DirEntry, err error) error {
			if err == nil && e.IsDir() {
				_ = os.Chmod(p, 0o755)
			}
			return nil
		})
	})
	api := enrolled(t, s, cfg.StateDir, server.URL)
	keys, err := node.LoadKeys(cfg.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	nodeCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- Run(nodeCtx, Node{Config: cfg, API: api, Drivers: []driver.Driver{perRunSessions{d}}, Box: keys.Box})
	}()
	defer func() { stop(); <-done }()

	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "playtest", Name: "playtest", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	runTask := func(id string) string {
		t.Helper()
		_ = os.Remove(probe)
		if _, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "playtest", Title: "deploy the playtest server", Assignee: "demo", Ready: true, RequestID: id}); err != nil {
			t.Fatal(err)
		}
		var runID string
		for {
			var run storage.Run
			if task, _ := s.Task(ctx, co, id); task.CurrentRunID != "" {
				runID = task.CurrentRunID
			}
			if runID != "" {
				run, _ = s.Get(ctx, co, runID)
			}
			if run.Status == "ENDED" {
				if run.Outcome != "succeeded" {
					t.Fatalf("%s: %+v", id, run)
				}
				b, err := os.ReadFile(probe)
				if err != nil {
					t.Fatal(err)
				}
				return string(b)
			}
			select {
			case err := <-done:
				t.Fatal("node stopped", err)
			case <-ctx.Done():
				task, _ := s.Task(ctx, co, id)
				t.Fatalf("timeout: %+v", task)
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	if got := runTask("with-key"); got != key {
		t.Fatalf("the run did not find the key file: %q", got)
	}

	// Removed from the agent: the next run gets nothing.
	if err = json.Unmarshal([]byte(owner("GET", "/catalog/agents/demo/secrets", nil)), &view); err != nil {
		t.Fatal(err)
	}
	owner("PUT", "/catalog/agents/demo/secrets", core.AgentSecretsPut{Base: view.Base, Secrets: []config.SecretUse{}})
	if got := runTask("without-key"); strings.TrimSpace(got) != "unset" {
		t.Fatalf("the run still got the key: %q", got)
	}
}
