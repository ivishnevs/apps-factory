package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/ownermcp"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// ownerAgent is the owner's MCP server against c over HTTP, as `factory mcp` runs it.
func ownerAgent(t *testing.T, c *Core) func(tool string, args map[string]any) (string, error) {
	t.Helper()
	srv := httptest.NewServer(c.Handler())
	t.Cleanup(srv.Close)
	s := &ownermcp.Server{Core: worker.API{URL: srv.URL, Token: "owner"}}
	return func(tool string, args map[string]any) (string, error) {
		v, err := s.Call(context.Background(), factorytools.Call{Name: tool, Arguments: args})
		if err != nil {
			return "", err
		}
		b, _ := json.Marshal(v)
		return string(b), nil
	}
}

func mustTool(t *testing.T, call func(string, map[string]any) (string, error), tool string, args map[string]any, want ...string) string {
	t.Helper()
	out, err := call(tool, args)
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("%s: %q missing in %s", tool, w, out)
		}
	}
	return out
}

func withCEO(c *Core) {
	var ceo config.Agent
	ceo.Metadata.Name = "ceo"
	ceo.Spec.Runtime.Provider = "codex"
	c.Agents["ceo"] = ceo
}

// TestOwnerAgentPausedProject (setup-assistant 5.2; spec "Why the CEO does not answer", "Change needs confirmation"):
// the owner's agent asks why the CEO's chat in tracely has no answer; the server names the project pause, its fix
// and the pauses runbook. Resuming previews what starts running and changes nothing until the owner's confirmation
// code comes back; the code works once and only for the same arguments.
func TestOwnerAgentPausedProject(t *testing.T) {
	c, s, _ := nodeCore(t)
	withCEO(c)
	ctx := context.Background()
	co := storage.DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "tracely", Name: "Tracely", Workspace: "/tmp/support-tracely"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPause(ctx, co, storage.Pause{Scope: "project", Target: "tracely", Reason: "paused during the move", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "tracely", Sender: "owner", Recipient: "ceo", MessageType: "notification", Body: "Status?", RequestID: "m1"}); err != nil {
		t.Fatal(err)
	}
	call := ownerAgent(t, c)
	mustTool(t, call, "factory_why", map[string]any{"agent": "ceo", "project": "tracely"},
		"paused", "factory resume --project tracely", `"runbook":"pauses"`)
	mustTool(t, call, "factory_doctor", nil, "chat with ceo in tracely", "project tracely", "paused during the move")

	// Resume: a preview first, nothing changes.
	out := mustTool(t, call, "factory_resume", map[string]any{"scope": "project", "target": "tracely"},
		"Resumes project tracely", "paused during the move", storage.ConversationID("tracely", "ceo"), `"confirm"`)
	var preview struct{ Confirm string }
	_ = json.Unmarshal([]byte(out), &preview)
	if ps, _ := s.Pauses(ctx, co); len(ps) != 1 {
		t.Fatalf("a preview changed the pauses: %+v", ps)
	}
	// A code for other arguments, or a made-up one, is refused.
	if _, err := call("factory_resume", map[string]any{"scope": "company", "confirm": preview.Confirm}); err == nil {
		t.Fatal("a code confirmed other arguments")
	}
	if _, err := call("factory_resume", map[string]any{"scope": "project", "target": "tracely", "confirm": "zz-0123456789abcdef"}); err == nil {
		t.Fatal("a made-up code confirmed")
	}
	mustTool(t, call, "factory_resume", map[string]any{"scope": "project", "target": "tracely", "confirm": preview.Confirm}, `"done"`)
	if ps, _ := s.Pauses(ctx, co); len(ps) != 0 {
		t.Fatalf("not resumed: %+v", ps)
	}
	if _, err := call("factory_resume", map[string]any{"scope": "project", "target": "tracely", "confirm": preview.Confirm}); err == nil {
		t.Fatal("a code confirmed twice")
	}
}

// TestOwnerAgentNodeFaults (setup-assistant 5.2): the owner's agent names the cause and the runbook of each node fault of
// 2026-11-11: the only node's disk below its critical watermark, a node whose DNS is broken (Claude disabled by
// net.api), and a session tied to a paused node.
func TestOwnerAgentNodeFaults(t *testing.T) {
	c, s, fake := nodeCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/support-app"}); err != nil {
		t.Fatal(err)
	}
	reg := registration("n1", 1)
	reg.Name = "tvorog-mac"
	reg.Drivers = append(reg.Drivers, storage.NodeDriver{Name: "claude", Version: "2.1", OK: false, Check: "probe",
		Detail: "net.dns: cannot resolve api.anthropic.com: no such host; net.api: api.anthropic.com not reached"})
	if _, err := s.RegisterNode(ctx, co, reg); err != nil {
		t.Fatal(err)
	}
	disk := `{"disk":"critical","free_disk_bytes":3865470566,"disk_low_bytes":21474836480,"disk_critical_bytes":5368709120,"disk_conversation_floor_bytes":1073741824,"checks":[]}`
	if _, err := s.NodeHeartbeat(ctx, co, "n1", storage.NodeHeartbeat{Epoch: 1, Health: json.RawMessage(disk)}); err != nil {
		t.Fatal(err)
	}
	v, err := s.CreateTask(ctx, co, tasks.Create{ID: "w1", ProjectID: "app", Title: "w1", Assignee: "dev", Ready: true, RequestID: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := []byte(`{"spec":{"runtime":{"provider":"codex"},"workspace":{"mode":"worktree"}}}`)
	if _, err = s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	call := ownerAgent(t, c)
	mustTool(t, call, "factory_why", map[string]any{"task": "w1"}, "disk critical", "free disk", `"runbook":"disk"`)
	mustTool(t, call, "factory_doctor", nil, "3.6 GiB free", `"runbook":"disk"`, "claude on tvorog-mac is disabled", `"runbook":"dns-and-proxy"`)
	mustTool(t, call, "factory_status", nil, "claude: disabled, probe net.dns", "codex: usable")

	// The disk is freed; the run starts on n1 and ends with a session; its retry is tied to n1, which the owner pauses.
	if _, err = s.NodeHeartbeat(ctx, co, "n1", storage.NodeHeartbeat{Epoch: 1, Health: json.RawMessage(`{"disk":"ok","checks":[]}`)}); err != nil {
		t.Fatal(err)
	}
	cl, err := s.ClaimV2(ctx, co, "r1", storage.ClaimRequest{NodeID: "n1", Epoch: 1, ClaimID: "1-0-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, co, "r1", cl.Lease); err != nil {
		t.Fatal(err)
	}
	if err = s.Apply(ctx, co, protocol.Wrap("agent.failed", "r1", "", protocol.Event{RunID: "r1", Lease: cl.Lease, Status: "ENDED", Outcome: "failed", Result: "tests fail", SessionID: "sess-1",
		Usage: []protocol.Usage{{Model: "gpt-5", Input: 10000, Output: 2140, CacheRead: 340000}}})); err != nil {
		t.Fatal(err)
	}
	fake.Advance(time.Hour)
	if v, err = s.Task(ctx, co, "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r2", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	if err = s.SetPause(ctx, co, storage.Pause{Scope: "node", Target: "n1", Sessions: storage.PauseSessionsKeep, Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	mustTool(t, call, "factory_why", map[string]any{"task": "w1"}, "agent's session", "paused", "release", `"runbook":"agent-does-not-answer"`)
	mustTool(t, call, "factory_doctor", nil, "task w1", "node paused by the owner", "factory resume --node n1")
	mustTool(t, call, "factory_runs", map[string]any{"failed": true}, `"id":"r1"`, "352k tokens", "340k cache reads")
}
