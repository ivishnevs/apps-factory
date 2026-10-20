package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestOpenSpecGatewayPolicyScopeAndInitialization(t *testing.T) {
	ctx := context.Background()
	s, e := storage.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	agents, e := config.Load("../..")
	if e != nil {
		t.Fatal(e)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	workspace := t.TempDir()
	if _, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: workspace}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.InitOpenSpec(ctx, storage.DefaultCompany, "p", "owner", "", "setup"); e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(filepath.Join(workspace, "openspec/changes/feature"), 0755)
	os.WriteFile(filepath.Join(workspace, "openspec/changes/feature/proposal.md"), []byte("# Feature\nCurrent requirements\n"), 0644)
	current := agents["demo"]
	snapshot, _ := json.Marshal(current)
	run := protocol.RunSpec{ID: "spec-agent", Project: "p", Agent: "demo", Workspace: workspace, ConfigSnapshot: snapshot}
	if e = s.Create(ctx, storage.DefaultCompany, run); e != nil {
		t.Fatal(e)
	}
	lease, e := s.Claim(ctx, storage.DefaultCompany, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	token := c.runToken(storage.DefaultCompany, run.ID, lease)
	httpCall := func(auth, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+auth)
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		return httpCall(token, "POST", "/agent/call", factorytools.Call{Name: name, Arguments: args})
	}
	for name, args := range map[string]map[string]any{"factory_openspec_init": {"request_id": "init-agent"}, "factory_openspec_list": {"dir": "changes", "limit": 2}, "factory_openspec_read": {"path": "changes/feature/proposal.md"}, "factory_openspec_check": {"change": "feature"}} {
		if w := call(name, args); w.Code != 200 {
			t.Fatal(name, w.Code, w.Body.String())
		}
	}
	if w := httpCall("owner", "POST", "/projects/p/openspec/init", map[string]any{"request_id": "owner-active"}); w.Code != 409 {
		t.Fatal("owner bypassed writer", w.Code, w.Body.String())
	}
	if w := httpCall(token, "GET", "/projects/p/openspec/read?path=changes/feature/proposal.md", nil); w.Code != 401 {
		t.Fatal("run token on owner endpoint", w.Code)
	}
	for _, args := range []map[string]any{{"path": "../foreign/proposal.md"}, {"path": "changes/feature/proposal.md", "project_id": "other"}, {"path": "changes/feature/proposal.md", "workspace": "/tmp/other"}, {"path": "changes/feature/proposal.md", "limit": 8193}} {
		if w := call("factory_openspec_read", args); w.Code != 400 {
			t.Fatal("invalid scope/bounds", w.Code, w.Body.String())
		}
	}
	current.Spec.Tools.Deny = []string{"factory.openspec.read"}
	c.Agents["demo"] = current
	if w := call("factory_openspec_read", map[string]any{"path": "changes/feature/proposal.md"}); w.Code != 403 {
		t.Fatal("current policy ignored", w.Code)
	}
	current.Spec.Tools.Deny = nil
	current.Spec.Approvals.Require = []string{"factory.openspec.init"}
	c.Agents["demo"] = current
	if w := call("factory_openspec_init", map[string]any{"request_id": "need-grant"}); w.Code != 423 {
		t.Fatal("approval bypass", w.Code, w.Body.String())
	}
	c.Agents["demo"] = agents["demo"]
	// The snapshot remains authoritative even after a current policy becomes permissive.
	captured := agents["demo"]
	captured.Spec.Tools.Deny = []string{"factory.openspec.read"}
	b, _ := json.Marshal(captured)
	var raw map[string]any
	json.Unmarshal(mustRunSpec(t, s, run.ID), &raw)
	raw["config_snapshot"] = json.RawMessage(b)
	specBytes, _ := json.Marshal(raw)
	if _, e = s.DB.Exec("UPDATE runs SET spec=? WHERE id=?", specBytes, run.ID); e != nil {
		t.Fatal(e)
	}
	if w := call("factory_openspec_read", map[string]any{"path": "changes/feature/proposal.md"}); w.Code != 403 {
		t.Fatal("captured policy ignored", w.Code, w.Body.String())
	}
}
func mustRunSpec(t *testing.T, s *storage.Store, id string) []byte {
	t.Helper()
	r, e := s.Get(context.Background(), storage.DefaultCompany, id)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(r.Spec)
	return b
}

func TestSchedulerBlocksDisappearedOpenSpecBeforeRun(t *testing.T) {
	ctx := context.Background()
	s, e := storage.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	agents, e := config.Load("../..")
	if e != nil {
		t.Fatal(e)
	}
	c := &Core{Store: s, Agents: agents}
	workspace := t.TempDir()
	if _, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: workspace}); e != nil {
		t.Fatal(e)
	}
	s.OpenSpec.Init(workspace)
	os.MkdirAll(filepath.Join(workspace, "openspec/changes/feature"), 0755)
	proposal := filepath.Join(workspace, "openspec/changes/feature/proposal.md")
	os.WriteFile(proposal, []byte("# Feature"), 0644)
	task, e := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "linked", Assignee: "demo", OpenSpecChange: "feature"})
	if e != nil {
		t.Fatal(e)
	}
	task, e = s.TaskAction(ctx, storage.DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"})
	if e != nil {
		t.Fatal(e)
	}
	os.Rename(proposal, proposal+".held")
	if e = c.Schedule(ctx); e != nil {
		t.Fatal(e)
	}
	current, _ := s.Task(ctx, storage.DefaultCompany, task.ID)
	if current.Status != "BLOCKED" || current.Attempt != 0 {
		t.Fatal(current)
	}
}

func TestOpenSpecBrokenLinkBlocksKnownFailedRetry(t *testing.T) {
	ctx := context.Background()
	s, e := storage.Open(filepath.Join(t.TempDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	agents, e := config.Load("../..")
	if e != nil {
		t.Fatal(e)
	}
	fake := clock.NewFake(time.Now())
	s.Clock = fake
	c := &Core{Store: s, Agents: agents}
	workspace := t.TempDir()
	if _, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: workspace}); e != nil {
		t.Fatal(e)
	}
	s.OpenSpec.Init(workspace)
	os.MkdirAll(filepath.Join(workspace, "openspec/changes/feature"), 0755)
	proposal := filepath.Join(workspace, "openspec/changes/feature/proposal.md")
	os.WriteFile(proposal, []byte("# Feature"), 0644)
	task, e := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "linked", Assignee: "demo", OpenSpecChange: "feature"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.TaskAction(ctx, storage.DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"}); e != nil {
		t.Fatal(e)
	}
	if e = c.Schedule(ctx); e != nil {
		t.Fatal(e)
	}
	task, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
	lease, e := s.Claim(ctx, storage.DefaultCompany, task.CurrentRunID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.completed", task.CurrentRunID, "", protocol.Event{RunID: task.CurrentRunID, Lease: lease, Status: "ENDED", Outcome: "failed"})); e != nil {
		t.Fatal(e)
	}
	os.Rename(proposal, proposal+".held")
	fake.Advance(2 * time.Minute) // past the retry backoff
	if e = c.Schedule(ctx); e != nil {
		t.Fatal("broken retry killed scheduler", e)
	}
	current, _ := s.Task(ctx, storage.DefaultCompany, task.ID)
	if current.Status != "BLOCKED" || current.Attempt != 1 {
		t.Fatal(current)
	}
}
