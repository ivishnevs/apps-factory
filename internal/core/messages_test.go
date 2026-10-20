package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

func TestMessageMCPIdentityScopeAndScheduling(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: catalog.Agents, Skills: catalog.Skills, Token: "owner"}
	for _, id := range []string{"p", "other"} {
		if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: id, Name: id, Workspace: "/tmp/messages-" + id}); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := s.CreateConversation(ctx, storage.DefaultCompany, messaging.ConversationCreate{ProjectID: "other", Agent: "manager", Title: "Other", RequestID: "other"})
	if err != nil {
		t.Fatal(err)
	}
	a := catalog.Agents["demo"]
	snapshot, _ := json.Marshal(a)
	run := protocol.RunSpec{ID: "run", Project: "p", Agent: "demo", Workspace: "/tmp/messages-p", ConfigSnapshot: snapshot}
	if err = s.Create(ctx, storage.DefaultCompany, run); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, storage.DefaultCompany, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, args map[string]any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(factorytools.Call{Name: "factory_" + name, Arguments: args})
		r := httptest.NewRequest("POST", "/agent/call", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+c.runToken(storage.DefaultCompany, run.ID, lease))
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	for _, input := range []map[string]any{
		{"recipient": "manager", "body": "bad", "sender": "owner", "request_id": "inject"},
		{"recipient": "manager", "body": "bad", "project_id": "other", "request_id": "project"},
	} {
		if w := call("message_ask", input); w.Code != 400 {
			t.Fatal("identity injection", w.Code, w.Body.String())
		}
	}
	if w := call("message_list", map[string]any{"conversation_id": foreign.ID}); w.Code != 403 {
		t.Fatal("cross-project read", w.Code)
	}
	if w := call("message_ask", map[string]any{"recipient": "manager", "body": "bad", "blocking": true, "request_id": "blocking"}); w.Code != 403 {
		t.Fatal("blocking without task", w.Code)
	}
	changed := a
	changed.Spec.Communication.CanMessage = []string{"owner"}
	c.Agents["demo"] = changed
	if w := call("message_ask", map[string]any{"recipient": "manager", "body": "denied", "request_id": "denied"}); w.Code != 403 {
		t.Fatal("target permission ignored", w.Code)
	}
	c.Agents["demo"] = a
	changed = a
	changed.Spec.Approvals.Require = append(changed.Spec.Approvals.Require, "factory.owner.ask")
	c.Agents["demo"] = changed
	if w := call("owner_ask", map[string]any{"body": "approval", "request_id": "approval"}); w.Code != 423 {
		t.Fatal("approval bypass", w.Code)
	}
	c.Agents["demo"] = a
	w := call("message_ask", map[string]any{"recipient": "manager", "body": "Question", "request_id": "ask"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var m messaging.Message
	if err = json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m.Sender != "demo" || m.ProjectID != "p" {
		t.Fatal(m)
	}
	// The question is a trigger on the recipient's conversation task (hierarchy D5), scheduled like any task;
	// it does not wait for the sender's run or take the project writer slot.
	if m.DeliveryStatus != "TRIGGERED" {
		t.Fatal(m)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	conv, _ := s.Task(ctx, storage.DefaultCompany, storage.ConversationID("p", "manager"))
	if conv.CurrentRunID == "" || conv.Kind != tasks.KindConversation {
		t.Fatal("no recipient work", conv)
	}
	responder, err := s.Get(ctx, storage.DefaultCompany, conv.CurrentRunID)
	if err != nil || responder.Spec.TaskID != conv.ID || responder.Spec.SessionID != "" || !responder.Spec.Conversation {
		t.Fatal("mixed sessions/tasks", responder, err)
	}
	managerLease, err := s.Claim(ctx, storage.DefaultCompany, responder.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if responder, _ = s.Get(ctx, storage.DefaultCompany, conv.CurrentRunID); !strings.Contains(responder.Spec.Prompt, m.ID) || !strings.Contains(responder.Spec.Prompt, "factory_message_reply") {
		t.Fatal(responder.Spec.Prompt)
	}
	// Sender cannot forge recipient identity to answer; real recipient can.
	run = responder.Spec
	lease = managerLease
	if w = call("message_reply", map[string]any{"message_id": m.ID, "version": m.Version, "body": "Answer", "request_id": "reply", "sender": "owner"}); w.Code != 400 {
		t.Fatal("reply injection", w.Code)
	}
	w = call("message_reply", map[string]any{"message_id": m.ID, "version": m.Version, "body": "Answer", "request_id": "reply"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	m, _ = s.Message(ctx, storage.DefaultCompany, m.ID)
	if m.Status != "ANSWERED" {
		t.Fatal(m)
	}
}
