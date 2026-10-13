package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/decisions"
	"appfactory/internal/factorytools"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

func TestDecisionAutonomyAndDurableRejection(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "db")
	s, e := storage.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.DB.Close() }()
	agents, e := config.Load("../..")
	if e != nil {
		t.Fatal(e)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	_, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/autonomy"})
	if e != nil {
		t.Fatal(e)
	}
	a := agents["manager-autonomous"]
	snapshot, _ := json.Marshal(a)
	run := protocol.RunSpec{ID: "autonomy", Project: "p", Agent: a.Metadata.Name, Workspace: "/tmp/autonomy", ConfigSnapshot: snapshot}
	if e = s.Create(ctx, storage.DefaultCompany, run); e != nil {
		t.Fatal(e)
	}
	lease, e := s.Claim(ctx, storage.DefaultCompany, run.ID)
	if e != nil {
		t.Fatal(e)
	}
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(factorytools.Call{Name: "factory_" + name, Arguments: args})
		r := httptest.NewRequest("POST", "/agent/call", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+c.runToken(storage.DefaultCompany, run.ID, lease))
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	args := map[string]any{"category": "technical", "topic": "storage", "value": "SQLite", "rationale": "Single writer", "request_id": "decide"}
	w := call("decision_decide", args)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var d decisions.Decision
	json.Unmarshal(w.Body.Bytes(), &d)
	if d.ApprovedBy != a.Metadata.Name || d.Authority != "policy" || d.MessageID != "" || len(d.AuthorityRules) == 0 || d.ConfigHash == "" || d.CurrentConfigHash == "" {
		t.Fatal(d)
	}
	if w = call("decision_decide", args); w.Code != 200 {
		t.Fatal("replay", w.Code, w.Body.String())
	}
	inbox, _ := s.Inbox(ctx, storage.DefaultCompany, "p")
	if len(inbox) != 0 {
		t.Fatal("autonomy notified owner", inbox)
	}
	args["value"] = "changed"
	if w = call("decision_decide", args); w.Code != 409 {
		t.Fatal("changed replay", w.Code)
	}
	args["request_id"] = "product"
	args["category"] = "product"
	if w = call("decision_decide", args); w.Code != 403 {
		t.Fatal("undelegated category", w.Code)
	}
	args["category"] = "technical"
	args["request_id"] = "replace"
	if w = call("decision_decide", args); w.Code != 409 {
		t.Fatal("implicit replacement", w.Code)
	}
	args["supersedes_id"] = d.ID
	args["supersedes_version"] = d.Version
	if w = call("decision_decide", args); w.Code != 200 {
		t.Fatal("explicit replacement", w.Code, w.Body.String())
	}
	// Both current and captured authority are mandatory; defaults, prompts and approval cannot substitute.
	changed := a
	changed.Rules = nil
	c.Agents[a.Metadata.Name] = changed
	args["topic"] = "new"
	args["request_id"] = "revoked"
	delete(args, "supersedes_id")
	delete(args, "supersedes_version")
	if w = call("decision_decide", args); w.Code != 403 {
		t.Fatal("revoked authority", w.Code)
	}
	changed = a
	changed.Rules = append(append([]policies.Rule{}, a.Rules...), policies.Rule{Name: "deny", Type: "access", Effect: "deny", Operations: []string{"factory.decision.decide.technical"}})
	c.Agents[a.Metadata.Name] = changed
	if w = call("decision_decide", args); w.Code != 403 {
		t.Fatal("deny priority", w.Code)
	}
	c.Agents[a.Metadata.Name] = a
	for _, name := range []string{"owner_ask", "decision_propose"} {
		input := map[string]any{"body": "ask", "request_id": "ask"}
		if name == "decision_propose" {
			input = map[string]any{"topic": "product", "value": "idea", "rationale": "why", "request_id": "ask"}
		}
		if w = call(name, input); w.Code != 403 {
			t.Fatal("owner ban", name, w.Code, w.Body.String())
		}
	}
	if a.Authorize("external.purchase").Effect != "approval" || a.Authorize("factory.decision.record").Effect != "approval" {
		t.Fatal("protected permissions changed")
	}

	// An approved owner choice stays authoritative, including when a replacement is rejected.
	ownerChoice, e := s.ProposeDecision(ctx, storage.DefaultCompany, "p", decisions.Propose{Topic: "owner storage", Value: "PostgreSQL", Rationale: "Owner requirement", Actor: "owner", RequestID: "owner-choice"})
	if e != nil {
		t.Fatal(e)
	}
	ownerChoice, e = s.RecordDecision(ctx, storage.DefaultCompany, ownerChoice.ID, decisions.Record{Version: 1, Status: "approved", Rationale: "Confirmed", Actor: "owner", RequestID: "owner-approve"})
	if e != nil {
		t.Fatal(e)
	}
	args["topic"] = "owner storage"
	args["request_id"] = "owner-takeover"
	args["supersedes_id"] = ownerChoice.ID
	args["supersedes_version"] = ownerChoice.Version
	if w = call("decision_decide", args); w.Code != 409 {
		t.Fatal("owner choice takeover", w.Code)
	}
	replacement, e := s.ProposeDecision(ctx, storage.DefaultCompany, "p", decisions.Propose{Topic: "owner storage", Value: "SQLite", Rationale: "Alternative", Actor: "owner", RequestID: "owner-alternative"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.RecordDecision(ctx, storage.DefaultCompany, replacement.ID, decisions.Record{Version: 1, Status: "rejected", Rationale: "Keep original", Actor: "owner", RequestID: "reject-alternative"}); e != nil {
		t.Fatal(e)
	}
	retained, e := s.Decision(ctx, storage.DefaultCompany, ownerChoice.ID)
	if e != nil || retained.Status != "approved" || retained.Version != ownerChoice.Version {
		t.Fatal("rejection displaced previous choice", retained, e)
	}
	delete(args, "supersedes_id")
	delete(args, "supersedes_version")
	// Owner-managed topics cannot be taken over by changing category or decision value.
	proposal, e := s.ProposeDecision(ctx, storage.DefaultCompany, "p", decisions.Propose{Topic: "product", Value: "Paid app", Rationale: "idea", Actor: "manager", RequestID: "proposal"})
	if e != nil {
		t.Fatal(e)
	}
	args["topic"] = "product"
	args["request_id"] = "pending-takeover"
	if w = call("decision_decide", args); w.Code != 409 {
		t.Fatal("pending takeover", w.Code)
	}
	reject := decisions.Record{Version: 1, Status: "rejected", Rationale: "Free offline app only", Actor: "owner", RequestID: "reject"}
	rejected, e := s.RecordDecision(ctx, storage.DefaultCompany, proposal.ID, reject)
	if e != nil {
		t.Fatal(e)
	}
	repeated, e := s.RecordDecision(ctx, storage.DefaultCompany, proposal.ID, reject)
	if e != nil || repeated.ReactionMessageID != rejected.ReactionMessageID {
		t.Fatal("rejection replay", repeated, e)
	}
	pending, e := s.PendingMessages(ctx, storage.DefaultCompany)
	if e != nil || len(pending) != 1 || pending[0].DecisionID != proposal.ID || pending[0].Recipient != "manager" {
		t.Fatal("reaction", pending, e)
	}
	args["request_id"] = "rejected-takeover"
	if w = call("decision_decide", args); w.Code != 409 {
		t.Fatal("rejection bypass", w.Code)
	}
	if e = c.ScheduleMessages(ctx); e != nil {
		t.Fatal(e)
	}
	m, _ := s.Message(ctx, storage.DefaultCompany, rejected.ReactionMessageID)
	if m.DeliveryRunID != "" {
		t.Fatal("overlapping writer", m)
	}
	// Restart before dispatch: the revision remains durable and is scheduled once after writer release.
	if e = s.Apply(ctx, storage.DefaultCompany, protocol.Wrap("agent.completed", run.ID, "", protocol.Event{RunID: run.ID, Lease: lease, Status: "COMPLETED"})); e != nil {
		t.Fatal(e)
	}
	s.DB.Close()
	s, e = storage.Open(dbPath)
	if e != nil {
		t.Fatal(e)
	}
	c.Store = s
	if e = c.ScheduleMessages(ctx); e != nil {
		t.Fatal(e)
	}
	m, _ = s.Message(ctx, storage.DefaultCompany, rejected.ReactionMessageID)
	if m.DeliveryRunID == "" {
		t.Fatal("no revision run", m)
	}
	revision, e := s.Get(ctx, storage.DefaultCompany, m.DeliveryRunID)
	if e != nil || revision.Spec.Agent != "manager" || revision.Spec.TaskID != "" || !strings.Contains(revision.Spec.Prompt, "owner decision rejection") || !strings.Contains(revision.Spec.Prompt, "Free offline app only") {
		t.Fatal(revision, e)
	}
	if e = c.ScheduleMessages(ctx); e != nil {
		t.Fatal(e)
	}
	runs, _ := s.List(ctx, storage.DefaultCompany)
	if len(runs) != 2 {
		t.Fatal("duplicated revision", runs)
	}
}

func TestDecisionAuthorityRequiresCapturedGrant(t *testing.T) {
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
	current := agents["manager"]
	captured := current
	captured.Rules = nil
	b, _ := json.Marshal(captured)
	_, e = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/captured"})
	if e != nil {
		t.Fatal(e)
	}
	run := protocol.RunSpec{ID: "captured", Project: "p", Agent: "manager", Workspace: "/tmp/captured", ConfigSnapshot: b}
	if e = s.Create(ctx, storage.DefaultCompany, run); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Claim(ctx, storage.DefaultCompany, run.ID); e != nil {
		t.Fatal(e)
	}
	in := decisions.Decide{Propose: decisions.Propose{Category: "technical", Topic: "storage", Value: "SQLite", Rationale: "why", Actor: "manager", RunID: run.ID, RequestID: "new"}}
	if _, e = s.DecideDecision(ctx, storage.DefaultCompany, "p", in, current); e == nil {
		t.Fatal("current grant expanded captured permissions")
	}
	current.Rules = nil
	if _, e = s.DecideDecision(ctx, storage.DefaultCompany, "other", in, current); e == nil {
		t.Fatal("project mismatch accepted")
	}
}
