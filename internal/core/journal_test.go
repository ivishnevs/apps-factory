package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/decisions"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestJournalRunScopeAndExactOwnerDecision(t *testing.T) {
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
	for _, id := range []string{"p", "other"} {
		if _, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: id, Name: id, Workspace: "/tmp/journal-" + id}); e != nil {
			t.Fatal(e)
		}
	}
	a, _ := json.Marshal(agents["demo"])
	run := protocol.RunSpec{ID: "journal", Project: "p", Agent: "demo", Workspace: "/tmp/journal-p", ConfigSnapshot: a}
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
	props := map[string]any{"topic": "database", "value": "SQLite", "rationale": "Single Core state owner", "request_id": "proposal"}
	w := call("factory_decision_propose", props)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d decisions.Decision
	json.Unmarshal(w.Body.Bytes(), &d)
	if d.CreatedBy != "demo" || d.RunID != run.ID || d.Status != "proposed" {
		t.Fatal(d)
	}
	inbox, _ := s.Inbox(ctx, storage.DefaultCompany, "p")
	if len(inbox) != 1 || inbox[0].DecisionID != d.ID || inbox[0].Blocking {
		t.Fatal(inbox)
	}
	if w = httpCall("owner", "POST", "/inbox/"+d.MessageID+"/read", map[string]any{"version": 1, "request_id": "read"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := s.Decision(ctx, storage.DefaultCompany, d.ID)
	if got.Status != "proposed" {
		t.Fatal("read approved decision", got)
	}
	args := map[string]any{"decision_id": d.ID, "version": 1, "status": "approved", "rationale": "Reviewed design", "request_id": "record"}
	if w = call("factory_decision_record", args); w.Code != 423 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = httpCall(token, "POST", "/decisions/"+d.ID+"/record", args); w.Code != 401 {
		t.Fatal("run token used owner route", w.Code)
	}
	intent := factorytools.Call{Name: "factory_decision_record", Arguments: args}
	w = call("factory_owner_request_approval", map[string]any{"call": intent, "reason": "Approve exact decision", "blocking": false, "request_id": "ask"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var grant approvals.Approval
	json.Unmarshal(w.Body.Bytes(), &grant)
	w = httpCall("owner", "POST", "/approvals/"+grant.ID+"/decision", approvals.Decision{Version: grant.Version, Action: "approve", Reason: "Reviewed exact decision", RequestID: "grant"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	changed := map[string]any{}
	for k, v := range args {
		changed[k] = v
	}
	changed["rationale"] = "different rationale"
	if w = call("factory_decision_record", changed); w.Code != 423 {
		t.Fatal("changed grant accepted", w.Code, w.Body.String())
	}
	if w = call("factory_decision_record", args); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &got)
	if got.Status != "approved" || got.ApprovedBy != "owner" || got.RecordedBy != "demo" || got.ApprovalID != grant.ID {
		t.Fatal(got)
	}
	if w = call("factory_decision_record", args); w.Code != 200 {
		t.Fatal("replay", w.Code, w.Body.String())
	}
	other, e := s.ProposeDecision(ctx, storage.DefaultCompany, "other", decisions.Propose{Topic: "secret", Value: "other project", Rationale: "private", RequestID: "other"})
	if e != nil {
		t.Fatal(e)
	}
	if w = call("factory_decision_get", map[string]any{"decision_id": other.ID}); w.Code != 403 {
		t.Fatal("cross project decision", w.Code)
	}
	report := map[string]any{"kind": "stage_completed", "summary": "Done", "purpose": "Verify", "changes": []string{}, "result": "Passed", "request_id": "report"}
	report["created_by"] = "owner"
	if w = call("factory_report_create", report); w.Code != 400 {
		t.Fatal("forged identity", w.Code)
	}
	delete(report, "created_by")
	current := agents["demo"]
	current.Spec.Communication.CanMessage = nil
	c.Agents["demo"] = current
	report["needs_owner_action"] = true
	report["owner_action"] = "Review"
	if w = call("factory_report_create", report); w.Code != 403 {
		t.Fatal("owner notification bypass", w.Code)
	}
	delete(report, "needs_owner_action")
	delete(report, "owner_action")
	if w = call("factory_report_create", report); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current.Spec.Tools.Deny = []string{"factory.report.create"}
	c.Agents["demo"] = current
	report["request_id"] = "denied"
	if w = call("factory_report_create", report); w.Code != 403 {
		t.Fatal("tool deny bypass", w.Code)
	}
	if w = call("factory_decision_list", map[string]any{"limit": 21}); w.Code != 400 {
		t.Fatal("unbounded list", w.Code)
	}
}
