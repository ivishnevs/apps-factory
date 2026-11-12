package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/testnode"
)

// TestNodeConfigRoutes (node-packaging-updates B): owners and admins change a node's settings with PUT
// /nodes/{id}/config (a stale base is 409, invalid settings 400, an agent never); the node reads them signed from GET
// /nodes/{id}/config but not another node's; a waiting poll answers at once with the new config_version, and a node
// that applied it is not woken again.
func TestNodeConfigRoutes(t *testing.T) {
	c, s, _ := nodeCore(t)
	saved := pollRecheck
	pollRecheck = 50 * time.Millisecond
	t.Cleanup(func() { pollRecheck = saved })
	co := storage.DefaultCompany
	a, b := testnode.Enroll(t, s, co), testnode.Enroll(t, s, co)
	for _, n := range []testnode.Node{a, b} {
		if code, out := signedCall(t, c, n, "POST", "/nodes/register", registration(n.ID, 1)); code != 200 {
			t.Fatal(code, out)
		}
	}
	if got := requiredRole("PUT", "/nodes/"+a.ID+"/config"); got != storage.RoleAdmin {
		t.Fatal("role", got)
	}
	waiting := pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 20})
	time.Sleep(200 * time.Millisecond)
	slots := 4
	code, out := call(t, c, "PUT", "/nodes/"+a.ID+"/config", c.Token, map[string]any{"config": protocol.NodeSettings{Slots: &slots}})
	if code != 200 || !strings.Contains(out, `"version":1`) || !strings.Contains(out, `"pending"`) {
		t.Fatal(code, out)
	}
	r := waitResult(t, waiting, 3*time.Second)
	if got := answerOf(t, r); got.ConfigVersion != 1 || r.took > 2*time.Second {
		t.Fatalf("%+v after %s", got, r.took)
	}
	// The node fetches its settings; another node cannot.
	code, out = signedCall(t, c, a, "GET", "/nodes/"+a.ID+"/config", nil)
	var v protocol.NodeConfig
	if code != 200 || json.Unmarshal([]byte(out), &v) != nil || v.Version != 1 || v.Config.Slots == nil || *v.Config.Slots != 4 {
		t.Fatal(code, out)
	}
	if code, _ = signedCall(t, c, b, "GET", "/nodes/"+a.ID+"/config", nil); code != 403 {
		t.Fatal("another node read the settings", code)
	}
	if code, _ = signedCall(t, c, a, "PUT", "/nodes/"+a.ID+"/config", map[string]any{"config": map[string]any{}}); code != 403 {
		t.Fatal("a node changed its settings", code)
	}
	// Applied: the poll waits again.
	r = waitResult(t, pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 1, ConfigVersion: 1}), 3*time.Second)
	if got := answerOf(t, r); got.ConfigVersion != 1 || r.took < 900*time.Millisecond {
		t.Fatalf("applied version answered at once: %+v after %s", got, r.took)
	}
	if code, out = call(t, c, "PUT", "/nodes/"+a.ID+"/config", c.Token, map[string]any{"config": map[string]any{}, "base_version": 7}); code != 409 {
		t.Fatal("stale base", code, out)
	}
	if code, out = call(t, c, "PUT", "/nodes/"+a.ID+"/config", c.Token, map[string]any{"config": map[string]any{"slots": 0}}); code != 400 {
		t.Fatal("slots 0", code, out)
	}
	if code, _ = call(t, c, "PUT", "/nodes/nope/config", c.Token, map[string]any{"config": map[string]any{}}); code != 404 {
		t.Fatal("unknown node", code)
	}
	// The Nodes list carries the settings.
	code, out = call(t, c, "GET", "/nodes", c.Token, nil)
	if code != 200 || !strings.Contains(out, `"desired":{"slots":4}`) {
		t.Fatal(code, out)
	}
}

// TestNodeInstallRelease: the install info names the release Core hands out and the minimum node version.
func TestNodeInstallRelease(t *testing.T) {
	c, s, _ := nodeCore(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte("2026.11.12.1530\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.NodeDownloads, s.MinNodeVersion = dir, "2026.11.01"
	code, out := call(t, c, "GET", "/node-install", c.Token, nil)
	if code != 200 || !strings.Contains(out, `"release":"2026.11.12.1530"`) || !strings.Contains(out, `"min_node_version":"2026.11.01"`) {
		t.Fatal(code, out)
	}
}

// TestFactoryNodesTool (node-packaging-updates 3.6): factory_nodes lists each node with the labels its owner set and
// the labels it detected, its slots, usable runtimes and version.
func TestFactoryNodesTool(t *testing.T) {
	h := newExitHarness(t)
	ctx := context.Background()
	if _, err := h.s.RegisterNode(ctx, storage.DefaultCompany, storage.NodeRegistration{NodeID: "linux-1", Name: "hetzner", Epoch: 1, ProtocolVersion: 2, Slots: 3,
		Drivers: []storage.NodeDriver{{Name: "codex", OK: true}, {Name: "claude", OK: false}}, Labels: []string{"team-a"}, DetectedLabels: []string{"linux", "amd64", "headless"},
		Version: "2026.11.12.1530"}); err != nil {
		t.Fatal(err)
	}
	h.create("nt", "notes", "demo")
	r := h.start("nt")
	w := h.do(r.token, "POST", "/agent/call", factorytools.Call{Name: "factory_nodes", Arguments: map[string]any{}}, nil)
	body := w.Body.String()
	for _, want := range []string{`"name":"hetzner"`, `"labels":["team-a"]`, `"detected_labels":["linux","amd64","headless"]`, `"runtimes":["codex"]`, `"slots":3`, `"version":"2026.11.12.1530"`} {
		if w.Code != 200 || !strings.Contains(body, want) {
			t.Fatal(w.Code, want, body)
		}
	}
}
