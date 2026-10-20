package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/openspec"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

func TestOpenSpecGatewayPolicyScopeAndInitialization(t *testing.T) {
	ctx := context.Background()
	s, e := storage.Open(storagetest.URL(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	agents, e := config.Load("../..")
	if e != nil {
		t.Fatal(e)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	workspace := "/srv/nodes-only/p" // a node path: Core never opens it
	if _, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: workspace}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.InitOpenSpec(ctx, storage.DefaultCompany, "p", "owner", "", "setup"); e != nil {
		t.Fatal(e)
	}
	putProposal(t, s, "# Feature\nCurrent requirements\n")
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
	if w := httpCall("owner", "POST", "/projects/p/openspec/init", map[string]any{"request_id": "owner-active"}); w.Code != 200 {
		t.Fatal("owner init", w.Code, w.Body.String())
	}
	// Writes: compare-and-set on digests; a stale base gets 409 with the current digest.
	read := call("factory_openspec_read", map[string]any{"path": "changes/feature/proposal.md"})
	var doc struct {
		SHA256 string `json:"sha256"`
	}
	json.Unmarshal(read.Body.Bytes(), &doc)
	if w := call("factory_openspec_write", map[string]any{"path": "changes/feature/proposal.md", "base_sha256": doc.SHA256, "content": "# Feature\nAgent edit\n"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w := httpCall("owner", "POST", "/projects/p/openspec/write", map[string]any{"path": "changes/feature/proposal.md", "base_sha256": doc.SHA256, "content": "# Owner edit\n"})
	var conflict map[string]string
	json.Unmarshal(w.Body.Bytes(), &conflict)
	if w.Code != 409 || conflict["current_sha256"] != openspec.Digest([]byte("# Feature\nAgent edit\n")) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("factory_openspec_sync", map[string]any{}); w.Code != 400 {
		t.Fatal("sync is the adapter's", w.Code)
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

// putProposal writes the feature change's proposal as the owner over whatever is current.
func putProposal(t *testing.T, s *storage.Store, content string) {
	t.Helper()
	ctx := context.Background()
	base := ""
	if d, e := s.ReadOpenSpec(ctx, storage.DefaultCompany, "p", "changes/feature/proposal.md", 0, 8192, ""); e == nil {
		base = d.SHA256
	}
	if _, e := s.WriteOpenSpec(ctx, storage.DefaultCompany, "p", storage.OpenSpecChange{Path: "changes/feature/proposal.md", Base: base, Content: []byte(content), Actor: "owner"}); e != nil {
		t.Fatal(e)
	}
}

// dropProposal deletes the feature change's proposal as the owner.
func dropProposal(t *testing.T, s *storage.Store) {
	t.Helper()
	ctx := context.Background()
	d, e := s.ReadOpenSpec(ctx, storage.DefaultCompany, "p", "changes/feature/proposal.md", 0, 8192, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DeleteOpenSpec(ctx, storage.DefaultCompany, "p", storage.OpenSpecChange{Path: "changes/feature/proposal.md", Base: d.SHA256, Actor: "owner"}); e != nil {
		t.Fatal(e)
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
	s, e := storage.Open(storagetest.URL(t))
	if e != nil {
		t.Fatal(e)
	}
	defer s.DB.Close()
	agents, e := config.Load("../..")
	if e != nil {
		t.Fatal(e)
	}
	c := &Core{Store: s, Agents: agents}
	if _, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/srv/nodes-only/p"}); e != nil {
		t.Fatal(e)
	}
	putProposal(t, s, "# Feature")
	task, e := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "linked", Assignee: "demo", OpenSpecChange: "feature"})
	if e != nil {
		t.Fatal(e)
	}
	task, e = s.TaskAction(ctx, storage.DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"})
	if e != nil {
		t.Fatal(e)
	}
	dropProposal(t, s)
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
	s, e := storage.Open(storagetest.URL(t))
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
	if _, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/srv/nodes-only/p"}); e != nil {
		t.Fatal(e)
	}
	putProposal(t, s, "# Feature")
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
	dropProposal(t, s)
	fake.Advance(2 * time.Minute) // past the retry backoff
	if e = c.Schedule(ctx); e != nil {
		t.Fatal("broken retry killed scheduler", e)
	}
	current, _ := s.Task(ctx, storage.DefaultCompany, task.ID)
	if current.Status != "BLOCKED" || current.Attempt != 1 {
		t.Fatal(current)
	}
}
