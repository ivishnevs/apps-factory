package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestExactApprovalResumePersistenceAndRevocation(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "db")
	s, err := storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.DB.Close() }()
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	a := catalog.Agents["demo"]
	a.Spec.Approvals.Require = []string{"factory.task.update"}
	catalog.Agents["demo"] = a
	c := &Core{Store: s, Agents: catalog.Agents, Token: "owner"}
	_, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/approvals"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "before"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "approval requester", Assignee: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.TaskAction(ctx, storage.DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
	run, _ := s.Get(ctx, storage.DefaultCompany, task.CurrentRunID)
	lease, err := s.Claim(ctx, storage.DefaultCompany, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := c.runToken(storage.DefaultCompany, run.Spec.ID, lease)
	httpCall := func(auth, method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+auth)
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	call := func(call factorytools.Call) *httptest.ResponseRecorder {
		return httpCall(token, "POST", "/agent/call", call)
	}
	action := factorytools.Call{Name: "factory_task_update", Arguments: map[string]any{"id": target.ID, "version": target.Version, "title": "approved title", "request_id": "exact-intent"}}
	if w := call(action); w.Code != 423 {
		t.Fatal(w.Code, w.Body.String())
	}
	request := factorytools.Call{Name: "factory_owner_request_approval", Arguments: map[string]any{"call": action, "reason": "Approve this exact title change", "blocking": true, "request_id": "ask"}}
	w := call(request)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var grant approvals.Approval
	json.Unmarshal(w.Body.Bytes(), &grant)
	if grant.Status != "PENDING" || grant.MessageID == "" {
		t.Fatal(grant)
	}
	if w = call(request); w.Code != 200 {
		t.Fatal("request replay", w.Code, w.Body.String())
	}
	inbox, _ := s.Inbox(ctx, storage.DefaultCompany, "p")
	if len(inbox) != 1 || inbox[0].ApprovalID != grant.ID {
		t.Fatal(inbox)
	}
	if w = httpCall("owner", "POST", "/inbox/"+grant.MessageID+"/answer", map[string]any{"version": 1, "body": "yes", "request_id": "chat"}); w.Code != 400 {
		t.Fatal("chat authorized approval", w.Code)
	}
	if w = httpCall(token, "POST", "/approvals/"+grant.ID+"/decision", approvals.Decision{Version: 1, Action: "approve", Reason: "ok", RequestID: "decision"}); w.Code != 401 {
		t.Fatal("agent may decide", w.Code)
	}
	if err = s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.completed", run.Spec.ID, "", protocol.Event{RunID: run.Spec.ID, Lease: lease, Status: "COMPLETED", SessionID: "same-thread"})); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
	if task.Status != "BLOCKED" {
		t.Fatal(task)
	}
	s.DB.Close()
	s, err = storage.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	c.Store = s
	decide := approvals.Decision{Version: 1, Action: "approve", Reason: "Reviewed exact parameters", RequestID: "decision"}
	w = httpCall("owner", "POST", "/approvals/"+grant.ID+"/decision", decide)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &grant)
	if grant.Version != 2 || grant.Status != "APPROVED" {
		t.Fatal(grant)
	}
	if w = httpCall("owner", "POST", "/approvals/"+grant.ID+"/decision", decide); w.Code != 200 {
		t.Fatal("decision replay", w.Code)
	}
	task, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
	if task.Status != "READY" {
		t.Fatal(task)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	task, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
	resumed, _ := s.Get(ctx, storage.DefaultCompany, task.CurrentRunID)
	if resumed.Spec.SessionID != "same-thread" {
		t.Fatal(resumed)
	}
	lease, err = s.Claim(ctx, storage.DefaultCompany, resumed.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	token = c.runToken(storage.DefaultCompany, resumed.Spec.ID, lease)
	changed := factorytools.Call{Name: action.Name, Arguments: map[string]any{"id": target.ID, "version": target.Version, "title": "changed", "request_id": "exact-intent"}}
	if w = call(changed); w.Code != 423 {
		t.Fatal("changed parameters", w.Code, w.Body.String())
	}
	denied := a
	denied.Spec.Tools.Deny = []string{"factory.task.update"}
	c.Agents["demo"] = denied
	if w = call(action); w.Code != 403 {
		t.Fatal("approval overrides deny", w.Code)
	}
	c.Agents["demo"] = a
	if w = call(action); w.Code != 200 {
		t.Fatal("approved operation", w.Code, w.Body.String())
	}
	if w = call(action); w.Code != 200 {
		t.Fatal("approved idempotent replay", w.Code, w.Body.String())
	}
	target, _ = s.Task(ctx, storage.DefaultCompany, target.ID)
	if target.Title != "approved title" || target.Version != 2 {
		t.Fatal(target)
	}
	calls, _ := s.ToolCalls(ctx, storage.DefaultCompany, resumed.Spec.ID)
	uses := 0
	for _, v := range calls {
		if v.ApprovalID == grant.ID && v.Status == 200 {
			uses++
		}
	}
	if uses != 2 {
		t.Fatal(calls)
	}
	if w = httpCall("owner", "POST", "/approvals/"+grant.ID+"/decision", approvals.Decision{Version: 2, Action: "revoke", Reason: "Stop retries", RequestID: "revoke"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(action); w.Code != 423 {
		t.Fatal("revoked grant", w.Code)
	}
	// A different task/agent/project never receives this grant.
	for _, scope := range [][3]string{{"p", "demo", "different-task"}, {"p", "manager", task.ID}, {"other", "demo", task.ID}} {
		if _, err = s.CallApproval(ctx, storage.DefaultCompany, scope[0], scope[1], scope[2], action); err != storage.ErrNotFound {
			t.Fatal(scope, err)
		}
	}
}

func TestApprovalRejectedExpiredAndAlwaysMode(t *testing.T) {
	for _, outcome := range []string{"reject", "expire"} {
		t.Run(outcome, func(t *testing.T) {
			ctx := context.Background()
			s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.DB.Close()
			cat, err := config.LoadCatalog("../..")
			if err != nil {
				t.Fatal(err)
			}
			a := cat.Agents["demo"]
			a.Spec.Approvals.Mode = "always"
			cat.Agents["demo"] = a
			c := &Core{Store: s, Agents: cat.Agents, Token: "owner"}
			s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/approval-" + outcome})
			snapshot, _ := json.Marshal(a)
			s.Create(ctx, storage.DefaultCompany, protocol.RunSpec{ID: "run", Project: "p", Agent: "demo", Workspace: "/tmp/approval-" + outcome, ConfigSnapshot: snapshot})
			lease, err := s.Claim(ctx, storage.DefaultCompany, "run")
			if err != nil {
				t.Fatal(err)
			}
			call := func(name string, args map[string]any) *httptest.ResponseRecorder {
				b, _ := json.Marshal(factorytools.Call{Name: name, Arguments: args})
				r := httptest.NewRequest("POST", "/agent/call", bytes.NewReader(b))
				r.Header.Set("Authorization", "Bearer "+c.runToken(storage.DefaultCompany, "run", lease))
				w := httptest.NewRecorder()
				c.Handler().ServeHTTP(w, r)
				return w
			}
			action := factorytools.Call{Name: "factory_task_block", Arguments: map[string]any{"id": "missing", "version": 1, "reason": "test", "request_id": "intent"}}
			// Use a real task; the owner sees a concrete target.
			target, e := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "target"})
			if e != nil {
				t.Fatal(e)
			}
			action.Arguments["id"] = target.ID
			w := call("factory_owner_request_approval", map[string]any{"call": action, "reason": "test", "timeout_seconds": 1, "request_id": "ask"})
			if w.Code != 200 {
				t.Fatal("always mode blocked approval request", w.Code, w.Body.String())
			}
			var grant approvals.Approval
			json.Unmarshal(w.Body.Bytes(), &grant)
			if outcome == "reject" {
				_, err = s.DecideApproval(ctx, storage.DefaultCompany, grant.ID, approvals.Decision{Version: 1, Action: "reject", Reason: "No", RequestID: "reject"})
			} else {
				grant.ExpiresAt = time.Now().Unix() - 1
				b, _ := json.Marshal(grant)
				_, err = s.DB.Exec("UPDATE approvals SET body=? WHERE id=?", b, grant.ID)
				if err == nil {
					err = s.ExpireApprovals(ctx, storage.DefaultCompany)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if w = call(action.Name, action.Arguments); w.Code != 423 {
				t.Fatal(outcome, w.Code, w.Body.String())
			}
			grant, _ = s.Approval(ctx, storage.DefaultCompany, grant.ID)
			expected := "REJECTED"
			if outcome == "expire" {
				expected = "EXPIRED"
			}
			if grant.Status != expected {
				t.Fatal(grant)
			}
			m, _ := s.Message(ctx, storage.DefaultCompany, grant.MessageID)
			if m.Status != "ANSWERED" {
				t.Fatal(m)
			}
			if w = call("factory_approval_get", map[string]any{"approval_id": grant.ID}); w.Code != 200 {
				t.Fatal("always mode blocked status", w.Code)
			}
		})
	}
}

// TestApprovalDecisionDoesNotReleaseActiveOrLostWriter: an approval decision does not start the task early,
// neither while its run is active nor while a lost run's retry waits for its backoff.
func TestApprovalDecisionDoesNotReleaseActiveOrLostWriter(t *testing.T) {
	for _, terminal := range []string{"COMPLETED", "LOST"} {
		t.Run(terminal, func(t *testing.T) {
			ctx := context.Background()
			s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer s.DB.Close()
			cat, err := config.LoadCatalog("../..")
			if err != nil {
				t.Fatal(err)
			}
			c := &Core{Store: s, Agents: cat.Agents}
			if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/approval-writer-" + terminal}); err != nil {
				t.Fatal(err)
			}
			task, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "waiting", Assignee: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			task, err = s.TaskAction(ctx, storage.DefaultCompany, task.ID, tasks.Action{Version: task.Version, Action: "ready"})
			if err != nil {
				t.Fatal(err)
			}
			if err = c.Schedule(ctx); err != nil {
				t.Fatal(err)
			}
			task, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
			lease, err := s.Claim(ctx, storage.DefaultCompany, task.CurrentRunID)
			if err != nil {
				t.Fatal(err)
			}
			grant, err := s.RequestApproval(ctx, storage.DefaultCompany, approvals.Request{ProjectID: "p", Agent: "demo", TaskID: task.ID, RunID: task.CurrentRunID, Call: factorytools.Call{Name: "factory_task_create", Arguments: map[string]any{"title": "future", "request_id": "intent"}}, Reason: "test", Blocking: true, RequestID: "ask"})
			if err != nil {
				t.Fatal(err)
			}
			if terminal == "LOST" {
				if _, err = s.DB.Exec("UPDATE runs SET lease_until=0 WHERE id=?", task.CurrentRunID); err != nil {
					t.Fatal(err)
				}
				if err = s.Expire(ctx, storage.DefaultCompany); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = s.DecideApproval(ctx, storage.DefaultCompany, grant.ID, approvals.Decision{Version: 1, Action: "approve", Reason: "yes", RequestID: "decision"}); err != nil {
				t.Fatal(err)
			}
			saved, _ := s.Task(ctx, storage.DefaultCompany, task.ID)
			if terminal == "COMPLETED" {
				if saved.Status != "IN_PROGRESS" {
					t.Fatal("decision released active writer", saved)
				}
				if err = s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.completed", task.CurrentRunID, "", protocol.Event{RunID: task.CurrentRunID, Lease: lease, Status: "COMPLETED", SessionID: "thread"})); err != nil {
					t.Fatal(err)
				}
				saved, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
				if saved.Status != "READY" {
					t.Fatal(saved)
				}
			} else {
				if saved.Status != "READY" || saved.NotBefore <= s.Now().Unix() {
					t.Fatal("decision released the lost run's retry", saved)
				}
				if err = c.Schedule(ctx); err != nil {
					t.Fatal(err)
				}
				saved, _ = s.Task(ctx, storage.DefaultCompany, task.ID)
				if saved.CurrentRunID != task.CurrentRunID || saved.Status != "READY" {
					t.Fatal(saved)
				}
			}
		})
	}
}
