package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/node"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestNodesCommandAgainstCore: `factory nodes enroll|keys|approve|reject|revoke` and `nodes list`; `factory tasks why`
// shows the node reasons (no_capable_node while no registered node has the assignee's driver).
func TestNodesCommandAgainstCore(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Agents: agents, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	run := func(args ...string) string {
		t.Helper()
		return capture(t, func() error { return nodesCommand(ctx, api, args) })
	}
	co := storage.DefaultCompany
	// nodes enroll issues a code; the node enrolls with it through the real route and waits.
	var code storage.IssuedNodeEnrollment
	if err = json.Unmarshal([]byte(run("nodes", "enroll", "--name", "mini-1")), &code); err != nil || !strings.HasPrefix(code.Code, storage.EnrollmentCodePrefix) {
		t.Fatal(code, err)
	}
	enroll := func(issued string) (node.Keys, string, <-chan error) {
		t.Helper()
		dir := t.TempDir()
		id, err := node.NodeID(dir)
		if err != nil {
			t.Fatal(err)
		}
		waitCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		t.Cleanup(cancel)
		done := make(chan error, 1)
		go func() {
			_, err := node.Enrollment{URL: server.URL, Code: issued, StateDir: dir, Poll: 5 * time.Millisecond, Out: io.Discard}.Run(waitCtx)
			done <- err
		}()
		for {
			if k, err := s.NodeKeyOf(waitCtx, id); err == nil && k.Status == storage.NodeKeyPending {
				break
			}
			select {
			case err := <-done:
				t.Fatal("enrollment ended:", err)
			case <-waitCtx.Done():
				t.Fatal(waitCtx.Err())
			case <-time.After(2 * time.Millisecond):
			}
		}
		keys, err := node.LoadKeys(dir)
		if err != nil {
			t.Fatal(err)
		}
		return keys, id, done
	}
	keys, id, done := enroll(code.Code)
	if out := run("nodes", "keys"); !strings.Contains(out, id) || !strings.Contains(out, keys.Fingerprint()) || !strings.Contains(out, storage.NodeKeyPending) {
		t.Fatal(out)
	}
	// A wrong fingerprint is refused; the right one activates the keys.
	if err = nodesCommand(ctx, api, []string{"nodes", "approve", id, "0000"}); err == nil {
		t.Fatal("wrong fingerprint accepted")
	}
	if out := run("nodes", "approve", id, keys.Fingerprint()); !strings.Contains(out, storage.NodeKeyActive) {
		t.Fatal(out)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	signer := worker.API{URL: server.URL, Node: id, Key: keys.Sign}
	register := storage.NodeRegistration{NodeID: id, Name: "mini-1", Epoch: 1, ProtocolVersion: 2, Slots: 1, Drivers: []storage.NodeDriver{{Name: "codex", OK: true}}}
	if err = signer.Do(ctx, "POST", "/nodes/register", register, nil); err != nil {
		t.Fatal("approved node:", err)
	}
	if out := run("nodes", "list"); !strings.Contains(out, `"mini-1"`) {
		t.Fatal(out)
	}
	// nodes reject ends pending keys.
	if err = json.Unmarshal([]byte(run("nodes", "enroll", "--name", "stolen")), &code); err != nil {
		t.Fatal(err)
	}
	stolen, stolenID, stolenDone := enroll(code.Code)
	if out := run("nodes", "reject", stolenID); !strings.Contains(out, `"ok"`) {
		t.Fatal(out)
	}
	if err = <-stolenDone; err == nil {
		t.Fatal("a rejected enrollment succeeded")
	}
	if out := run("nodes", "keys"); strings.Contains(out, stolenID) || strings.Contains(out, stolen.Fingerprint()) {
		t.Fatal(out)
	}
	// nodes revoke cuts an online node off at once, and another company cannot do it.
	if err = s.CreateCompany(ctx, "globex", "Globex"); err != nil {
		t.Fatal(err)
	}
	foreign, err := s.CreateOwnerToken(ctx, "globex", "first owner", "operator")
	if err != nil {
		t.Fatal(err)
	}
	if err = nodesCommand(ctx, worker.API{URL: server.URL, Token: foreign.Token}, []string{"nodes", "revoke", id}); err == nil {
		t.Fatal("revoked by another company")
	}
	if err = signer.Do(ctx, "POST", "/nodes/"+id+"/heartbeat", storage.NodeHeartbeat{Epoch: 1}, nil); err != nil {
		t.Fatal(err)
	}
	if out := run("nodes", "revoke", id); !strings.Contains(out, `"ok"`) {
		t.Fatal(out)
	}
	var refused *worker.HTTPError
	if err = signer.Do(ctx, "POST", "/nodes/"+id+"/heartbeat", storage.NodeHeartbeat{Epoch: 1}, nil); !errors.As(err, &refused) || refused.Status != 401 {
		t.Fatal("revoked node:", err)
	}
	if out := run("nodes", "keys"); strings.Contains(out, id) {
		t.Fatal(out)
	}
	if err = nodesCommand(ctx, api, []string{"nodes", "revoke", id}); err == nil {
		t.Fatal("revoked twice")
	}
	// The removed commands and bad arguments answer with the usage.
	for _, bad := range [][]string{{"nodes", "token", "create", "x"}, {"nodes", "token", "list"}, {"nodes", "enroll", "--bogus", "y"}, {"nodes", "approve", id}} {
		if err = nodesCommand(ctx, api, bad); err == nil || !strings.Contains(err.Error(), "usage") {
			t.Fatal(bad, err)
		}
	}

	if _, err = s.RegisterNode(ctx, co, storage.NodeRegistration{NodeID: "n2", Name: "linux-1", Epoch: 1, ProtocolVersion: 2, Slots: 1,
		Drivers: []storage.NodeDriver{{Name: "claude", OK: true}}}); err != nil {
		t.Fatal(err)
	}
	if out := run("nodes", "list"); !strings.Contains(out, `"linux-1"`) {
		t.Fatal(out)
	}
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/nodes-why"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	out := capture(t, func() error { return whyCommand(ctx, api, []string{"a"}) })
	if !strings.Contains(out, "no_capable_node") || !strings.Contains(out, "no usable codex driver") {
		t.Fatal(out)
	}
}
