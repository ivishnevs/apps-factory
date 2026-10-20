package core

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestOwnerUIRoutes (owner-ui 6.1, 6.2, 6.4, 6.5): a QUEUED run says why no node claims it on GET /runs/{id},
// in its task's why and in GET /status; GET /nodes keeps the drivers' hold-off and transient failure count;
// GET /tasks/{id}/parks lists Core's parks; GET /costs groups by project.
func TestOwnerUIRoutes(t *testing.T) {
	c, s, _ := nodeCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	if _, err := s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "app", Name: "app", Workspace: "/tmp/owner-ui-app"}); err != nil {
		t.Fatal(err)
	}
	until := s.Now().Add(10 * time.Minute).Unix()
	reg := registration("n1", 1)
	reg.Drivers = []storage.NodeDriver{{Name: "codex", OK: false, Check: "auth", Detail: "401", Until: until}, {Name: "claude", OK: true, TransientFailures: 1}}
	if _, err := s.RegisterNode(ctx, co, reg); err != nil {
		t.Fatal(err)
	}
	v, err := s.CreateTask(ctx, co, tasks.Create{ID: "t", ProjectID: "app", Title: "t", Assignee: "dev", Ready: true, RequestID: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, co, v.ID, v.Version, protocol.RunSpec{ID: "r1", Project: "app", Agent: "dev", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
	get := func(path string, out any) {
		t.Helper()
		code, body := call(t, c, "GET", path, "owner", nil)
		if code != 200 || json.Unmarshal([]byte(body), out) != nil {
			t.Fatal(path, code, body)
		}
	}
	queued := func(q *storage.QueuedReason) {
		t.Helper()
		if q == nil || q.Reason != storage.QueuedDriverDisabled || q.Until != until || !strings.Contains(q.Detail, "driver codex disabled: auth check failed: 401") {
			t.Fatalf("%+v", q)
		}
	}
	var run struct {
		Status string                `json:"status"`
		Queued *storage.QueuedReason `json:"queued"`
	}
	get("/runs/r1", &run)
	if run.Status != "QUEUED" {
		t.Fatal(run)
	}
	queued(run.Queued)
	var why struct {
		tasks.Verdict
		Queued *storage.QueuedReason `json:"queued"`
	}
	get("/tasks/t/why", &why)
	if why.Reason != tasks.ReasonNoCapableNode {
		t.Fatalf("%+v", why.Verdict)
	}
	queued(why.Queued)
	var status []ProjectStatus
	get("/status", &status)
	if len(status) != 1 || len(status[0].Runs) != 1 {
		t.Fatalf("%+v", status)
	}
	queued(status[0].Runs[0].Queued)
	var nodes []struct {
		Drivers []storage.NodeDriver `json:"drivers"`
	}
	get("/nodes", &nodes)
	if len(nodes) != 1 || nodes[0].Drivers[0].Until != until || nodes[0].Drivers[1].TransientFailures != 1 {
		t.Fatalf("%+v", nodes)
	}

	// Parks: a blocked task's park; an unknown task is 404.
	b, err := s.CreateTask(ctx, co, tasks.Create{ID: "b", ProjectID: "app", Title: "b", Assignee: "dev", Ready: true, RequestID: "b"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.TaskAction(ctx, co, "b", tasks.Action{Version: b.Version, Action: "block", Reason: "keys"}); err != nil {
		t.Fatal(err)
	}
	var parks []storage.TaskPark
	get("/tasks/b/parks", &parks)
	if len(parks) != 1 || parks[0].Kind != tasks.ParkBlock || !strings.Contains(parks[0].Reason, "keys") || parks[0].Status != "active" {
		t.Fatalf("%+v", parks)
	}
	if code, _ := call(t, c, "GET", "/tasks/nope/parks", "owner", nil); code != 404 {
		t.Fatal(code)
	}

	var costs storage.CostReport
	get("/costs?by=project", &costs)
	if code, _ := call(t, c, "GET", "/costs?by=nope", "owner", nil); code != 400 {
		t.Fatal(code)
	}
}
