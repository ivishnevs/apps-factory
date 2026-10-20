package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/node/shim"
	"appfactory/internal/policies"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

func TestCommandGuardsCore(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Now())
	s.Clock = fake
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	demo := agents["demo"]
	demo.Spec.Communication.CanMessage = append(demo.Spec.Communication.CanMessage, "owner")
	demo.Rules = append(demo.Rules, policies.Rule{Name: "push", Type: "access", Commands: []string{"git push *"}, Effect: "approval", Instruction: "Ask before pushing."})
	agents["demo"] = demo
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/core-guards"}); err != nil {
		t.Fatal(err)
	}
	snapshot, _ := json.Marshal(demo)
	run := protocol.RunSpec{ID: "run", Project: "p", Agent: "demo", Workspace: "/tmp/core-guards", ConfigSnapshot: snapshot}
	if err = s.Create(ctx, co, run); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, co, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := c.runToken(co, run.ID, lease)
	post := func(auth, path string, body any) (int, []byte) {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+auth)
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w.Code, w.Body.Bytes()
	}
	command := func(argv ...string) (int, commandAnswer) {
		t.Helper()
		code, b := post(token, "/agent/command", map[string]any{"argv": argv})
		var a commandAnswer
		if code != 400 && json.Unmarshal(b, &a) != nil {
			t.Fatalf("%d: not JSON: %s", code, b)
		}
		return code, a
	}
	if code, _ := post("run.bogus.token", "/agent/command", map[string]any{"argv": []string{"ls"}}); code != 401 {
		t.Fatal("bad token", code)
	}
	if code, _ := command(); code != 400 {
		t.Fatal("empty argv", code)
	}
	if code, _ := command("/bin/ls"); code != 400 {
		t.Fatal("path as command name", code)
	}
	if code, a := command("cliclick", "c:1,1"); code != 403 || a.Effect != "deny" || a.Rule != "host-ui-input" || !strings.Contains(a.Instruction, "accessibility identifier") {
		t.Fatal("deny", code, a)
	}
	if code, a := command("ls", "-la"); code != 200 || a.Effect != "allow" || a.ApprovalID != "" {
		t.Fatal("allow", code, a)
	}
	if code, a := command("git", "push", "origin", "main"); code != 423 || a.Effect != "approval" || a.Rule != "push" || !strings.Contains(a.Instruction, "Ask before pushing.") || !strings.Contains(a.Instruction, `"name":"command"`) {
		t.Fatal("approval without grant", code, a)
	}

	request := func(argv ...string) (int, approvals.Approval) {
		t.Helper()
		call := factorytools.Call{Name: "factory_owner_request_approval", Arguments: map[string]any{
			"reason": "push the release branch", "request_id": "req-" + strings.Join(argv, "-"), "blocking": false,
			"call": map[string]any{"name": "command", "arguments": map[string]any{"argv": argv}}}}
		code, b := post(token, "/agent/call", call)
		var a approvals.Approval
		_ = json.Unmarshal(b, &a)
		return code, a
	}
	if code, _ := request("ls"); code != 400 {
		t.Fatal("grant for an allowed command", code)
	}
	if code, _ := request("cliclick", "x"); code != 403 {
		t.Fatal("grant for a denied command", code)
	}
	code, grant := request("git", "push", "origin", "main")
	if code != 200 || grant.Kind != "command" || grant.Status != "PENDING" {
		t.Fatal("grant request", code, grant)
	}
	if _, err = s.CallApproval(ctx, co, "p", "demo", "", grant.Call); err == nil {
		t.Fatal("command grant found as an action approval")
	}
	if code, b := post("owner", "/approvals/"+grant.ID+"/decision", approvals.Decision{Version: grant.Version, Action: "approve", Reason: "ok", RequestID: "approve"}); code != 200 {
		t.Fatal(code, string(b))
	}
	if code, a := command("git", "push", "origin", "dev"); code != 423 || a.ApprovalID != "" {
		t.Fatal("changed argv allowed", code, a)
	}
	if code, a := command("git", "push", "origin", "main"); code != 200 || a.Effect != "allow" || a.ApprovalID != grant.ID {
		t.Fatal("approved command refused", code, a)
	}
	if code, _ := command("git", "push", "origin", "main"); code != 423 {
		t.Fatal("grant used twice", code)
	}
	if used, _ := s.Approval(ctx, co, grant.ID); used.Status != "USED" {
		t.Fatal(used.Status)
	}
	refusals := 0
	events, _ := s.Events(ctx, co, run.ID)
	for _, e := range events {
		var p protocol.Event
		if json.Unmarshal(e.Payload, &p) == nil && p.Method == "factory/command_refused" {
			refusals++
		}
	}
	if refusals != 4 { // cliclick, push without grant, push dev, second push
		t.Fatal("refusal events", refusals)
	}
	// The same argv can be requested again after use; an approved grant expires with time.
	code, fresh := func() (int, approvals.Approval) {
		call := factorytools.Call{Name: "factory_owner_request_approval", Arguments: map[string]any{
			"reason": "push again", "request_id": "req-again", "blocking": false,
			"call": map[string]any{"name": "command", "arguments": map[string]any{"argv": []string{"git", "push", "origin", "main"}}}}}
		code, b := post(token, "/agent/call", call)
		var a approvals.Approval
		_ = json.Unmarshal(b, &a)
		return code, a
	}()
	if code != 200 || fresh.ID == grant.ID {
		t.Fatal("same argv not requestable again", code, fresh)
	}
	if _, b := post("owner", "/approvals/"+fresh.ID+"/decision", approvals.Decision{Version: fresh.Version, Action: "approve", Reason: "ok", RequestID: "approve-2"}); !strings.Contains(string(b), "APPROVED") {
		t.Fatal(string(b))
	}
	fake.Advance(25 * time.Hour)
	if err = s.ExpireApprovals(ctx, co); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Approval(ctx, co, fresh.ID); a.Status != "EXPIRED" {
		t.Fatal("approved command grant did not expire", a.Status)
	}
}

// factory-shim's client (internal/node/shim.Ask) understands Core's answers.
func TestShimClientAgainstCore(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	co := storage.DefaultCompany
	snapshot, _ := json.Marshal(agents["demo"])
	if err = s.Create(ctx, co, protocol.RunSpec{ID: "run", Project: "p", Agent: "demo", Workspace: "/tmp/shim-core", ConfigSnapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	lease, err := s.Claim(ctx, co, "run")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	token := c.runToken(co, "run", lease)
	for argv, want := range map[string]struct {
		status int
		effect string
	}{"factory tasks list": {403, "deny"}, "ls -la": {200, "allow"}} {
		check, status, err := shim.Ask(ctx, server.Client(), server.URL, token, strings.Fields(argv))
		if err != nil || status != want.status || check.Effect != want.effect {
			t.Errorf("%s: %+v %d %v", argv, check, status, err)
		}
	}
}
