package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/testnode"
)

// TestNodeUpdateRoutes (node-packaging-updates 2.2, 3.4): Core offers a node behind its release the package for the
// node's platform with its sha256, in the poll and heartbeat answers, under the default auto policy; owners and admins
// switch the company to approval (GET/PUT /settings/node-updates), after which a behind node gets nothing until it is
// approved: Telegram asks with a button that approves it, POST /nodes/{id}/update approves on the web and with now
// wakes the waiting poll with "update now". A node that reports no platform, or runs the release, gets nothing.
func TestNodeUpdateRoutes(t *testing.T) {
	c, s, _ := nodeCore(t)
	saved := pollRecheck
	pollRecheck = 50 * time.Millisecond
	t.Cleanup(func() { pollRecheck = saved })
	ctx := context.Background()
	co := storage.DefaultCompany
	dir := t.TempDir()
	const sum = "4f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f8"
	for name, body := range map[string]string{"VERSION": "2026.11.13.0900\n", "factory-node-linux-arm64.tar.gz": "pkg",
		"SHA256SUMS": sum + "  factory-node-linux-arm64.tar.gz\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c.NodeDownloads = dir
	a, b, cur, old := testnode.Enroll(t, s, co), testnode.Enroll(t, s, co), testnode.Enroll(t, s, co), testnode.Enroll(t, s, co)
	reg := func(n testnode.Node, version, platform string) {
		r := registration(n.ID, 1)
		r.Version, r.Platform = version, platform
		if code, out := signedCall(t, c, n, "POST", "/nodes/register", r); code != 200 {
			t.Fatal(code, out)
		}
	}
	reg(a, "2026.11.12.1500", "linux-arm64")
	reg(b, "2026.11.12.1500", "linux-arm64")
	reg(cur, "2026.11.13.0900", "linux-arm64")
	reg(old, "2026.11.12.1500", "") // predates updates: no platform
	poll := func(n testnode.Node, wait int) PollAnswer {
		t.Helper()
		return answerOf(t, waitResult(t, pollAsync(t, c, n, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: wait}), 5*time.Second))
	}
	beat := func(n testnode.Node) *protocol.NodeUpdate {
		t.Helper()
		code, out := signedCall(t, c, n, "POST", "/nodes/"+n.ID+"/heartbeat", map[string]any{"epoch": 1, "inventory": []any{}, "health": map[string]any{}, "drivers": []any{}})
		var v struct {
			Update *protocol.NodeUpdate `json:"update"`
		}
		if code != 200 || json.Unmarshal([]byte(out), &v) != nil {
			t.Fatal(code, out)
		}
		return v.Update
	}

	// Auto (the default): offered at once, in the poll and the heartbeat.
	code, out := call(t, c, "GET", "/settings/node-updates", c.Token, nil)
	if code != 200 || !strings.Contains(out, `"policy":"auto"`) || !strings.Contains(out, `"release":"2026.11.13.0900"`) {
		t.Fatal(code, out)
	}
	if u := poll(a, 0).Update; u == nil || u.Version != "2026.11.13.0900" || u.URL != "/downloads/node/factory-node-linux-arm64.tar.gz" || u.SHA256 != sum || u.Now {
		t.Fatalf("%+v", u)
	}
	if u := beat(a); u == nil || u.SHA256 != sum {
		t.Fatalf("%+v", u)
	}
	if poll(cur, 0).Update != nil || poll(old, 0).Update != nil || beat(cur) != nil {
		t.Fatal("offered to a node on the release or without a platform")
	}

	// Approval: owners and admins set it; nothing offered, Telegram asks once per node.
	if got := requiredRole("PUT", "/settings/node-updates"); got != storage.RoleAdmin {
		t.Fatal("role", got)
	}
	if got := requiredRole("POST", "/nodes/"+a.ID+"/update"); got != storage.RoleAdmin {
		t.Fatal("role", got)
	}
	if code, out = call(t, c, "PUT", "/settings/node-updates", c.Token, map[string]string{"policy": "weekly"}); code != 400 {
		t.Fatal(code, out)
	}
	if code, out = call(t, c, "PUT", "/settings/node-updates", c.Token, map[string]string{"policy": "approve"}); code != 200 {
		t.Fatal(code, out)
	}
	if poll(a, 0).Update != nil || beat(b) != nil {
		t.Fatal("offered without approval")
	}
	tg := c.TelegramBackendFor(co)
	items, err := tg.Attention(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	asked := map[string]bool{}
	for _, it := range items {
		if strings.HasPrefix(it.Target.Question, nodeUpdateQuestion) {
			asked[strings.TrimPrefix(it.Target.Question, nodeUpdateQuestion)] = true
			if len(it.Options) != 1 || it.Options[0] != "Update to 2026.11.13.0900" || it.OwnersOnly {
				t.Fatalf("%+v", it)
			}
		}
	}
	if !asked[a.ID] || !asked[b.ID] || len(asked) != 2 {
		t.Fatal("asked", asked)
	}
	beat(b) // asked once
	if items, _ = tg.Attention(ctx, items[len(items)-1].Seq); len(items) != 0 {
		t.Fatalf("asked again: %+v", items)
	}

	// The Telegram button approves b; the web approves a now and wakes its waiting poll.
	if out, err = tg.Choose(ctx, nodeUpdateQuestion+b.ID, 0, "k1"); err != nil || !strings.Contains(out, "updates to 2026.11.13.0900") {
		t.Fatal(out, err)
	}
	if u := beat(b); u == nil || u.Now || u.Attempt == 0 {
		t.Fatalf("%+v", u)
	}
	waiting := pollAsync(t, c, a, PollRequest{Epoch: 1, Free: map[string]int{}, WaitSeconds: 20})
	time.Sleep(200 * time.Millisecond)
	if code, out = call(t, c, "POST", "/nodes/"+a.ID+"/update", c.Token, map[string]bool{"now": true}); code != 200 {
		t.Fatal(code, out)
	}
	r := waitResult(t, waiting, 3*time.Second)
	if u := answerOf(t, r).Update; u == nil || !u.Now || r.took > 2*time.Second {
		t.Fatalf("%+v after %s", u, r.took)
	}
	if code, _ = call(t, c, "POST", "/nodes/"+cur.ID+"/update", c.Token, map[string]bool{}); code != 409 {
		t.Fatal("approved a node on the release", code)
	}
	if code, _ = signedCall(t, c, a, "POST", "/nodes/"+a.ID+"/update", map[string]bool{"now": true}); code != 403 {
		t.Fatal("a node approved its own update", code)
	}
}
