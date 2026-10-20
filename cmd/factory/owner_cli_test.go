package main

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// TestOwnerCLIControls (waits-events-cost-cli 5.2): pause scopes by flag (quota key, node), comment and
// comment --urgent, run/retry, costs, pending (questions and escalations) and the kill switch over five live
// runs.
func TestOwnerCLIControls(t *testing.T) {
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
	c := &core.Core{Store: s, Agents: agents, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	co := storage.DefaultCompany
	cli := func(args ...string) string {
		t.Helper()
		return capture(t, func() error {
			switch args[0] {
			case "costs":
				return costsCommand(ctx, api, args)
			case "comment", "run", "retry", "telegram":
				return ownerTaskCommand(ctx, api, args)
			}
			return ownerCommand(ctx, api, args)
		})
	}

	// Pause scopes by flag.
	cli("pause", "--quota-key", "codex-team", "account switch")
	cli("pause", "--node", "mini-1", "maintenance")
	cli("pause", "--project", "p1")
	out := cli("pauses")
	for _, want := range []string{`"scope": "quota_key"`, `"target": "codex-team"`, `"scope": "node"`, `"target": "mini-1"`, `"scope": "project"`} {
		if !strings.Contains(out, want) {
			t.Fatal(want, out)
		}
	}
	cli("resume", "--node", "mini-1")
	cli("resume", "--quota-key", "codex-team")
	cli("resume", "--project", "p1")
	if out = cli("pauses"); strings.Contains(out, "scope") {
		t.Fatal(out)
	}

	// Five live runs in five projects (different agents), some claimed, some still queued.
	names := []string{"demo", "claude-demo", "manager", "calorie-ios", "calorie-qa"}
	runs, leases := []string{}, map[string]string{}
	for i, agent := range names {
		p := "p" + string(rune('1'+i))
		if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: p, Name: p, Workspace: "/tmp/owner-cli-" + p}); err != nil {
			t.Fatal(err)
		}
		if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "t" + p, ProjectID: p, Title: "work", Assignee: agent, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	for i := range names {
		tk, _ := s.Task(ctx, co, "tp"+string(rune('1'+i)))
		if tk.CurrentRunID == "" {
			t.Fatalf("not dispatched: %+v", tk)
		}
		runs = append(runs, tk.CurrentRunID)
		if i < 3 {
			if leases[tk.CurrentRunID], err = s.Claim(ctx, co, tk.CurrentRunID); err != nil {
				t.Fatal(err)
			}
		}
	}

	// comment: owner input (an authority trigger); --urgent stops the run.
	cli("comment", "tp1", "use", "the", "new", "icon")
	if snap, _ := s.TaskSnapshot(ctx, co, "tp1"); len(snap.Pending) == 0 || snap.Pending[len(snap.Pending)-1].Kind != "owner_comment" || !snap.Pending[len(snap.Pending)-1].Authority {
		t.Fatalf("%+v", snap.Pending)
	}
	cli("comment", "tp2", "stop,", "the", "spec", "changed", "--urgent")
	if r, _ := s.Get(ctx, co, runs[1]); r.Status != "CANCELLING" {
		t.Fatal(r.Status)
	}

	// pending: two questions and one escalation for the owner.
	for i, body := range []string{"Which store?", "Which price?"} {
		if _, err = s.SendMessage(ctx, co, messaging.Send{ProjectID: "p5", Sender: "calorie-qa", Recipient: "owner", MessageType: "question", Body: body, RequestID: "q" + string(rune('0'+i))}); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.SyncEscalations(ctx, co, "stalled", []storage.EscalationWant{{TaskID: "tp5", RefID: "x", Summary: "waits on x", Target: "owner"}}); err != nil {
		t.Fatal(err)
	}
	if out = cli("pending"); !strings.Contains(out, `"total": 3`) {
		t.Fatal(out)
	}

	// The kill switch: every live run is revoked, nothing new is dispatched; start lifts it.
	if out = cli("stop", "incident"); !strings.Contains(out, `"revoked": 4`) { // tp2's run was already stopping
		t.Fatal(out)
	}
	for i, id := range runs {
		r, _ := s.Get(ctx, co, id)
		switch {
		case i >= 3 && (r.Status != "ENDED" || r.Outcome != "revoked"):
			t.Fatalf("queued run %s: %s/%s", id, r.Status, r.Outcome)
		case i < 3 && r.Status != "CANCELLING":
			t.Fatalf("claimed run %s: %s", id, r.Status)
		}
		if i < 3 {
			if err = s.Apply(ctx, co, protocol.Wrap("agent.ended", id, "", protocol.Event{RunID: id, Lease: leases[id], Status: "ENDED", Outcome: "revoked"})); err != nil {
				t.Fatal(err)
			}
			if r, _ = s.Get(ctx, co, id); r.Outcome != "revoked" {
				t.Fatal(r.Outcome)
			}
		}
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if active, _ := s.ActiveRuns(ctx, co); len(active) != 0 {
		t.Fatalf("dispatched while stopped: %+v", active)
	}
	cli("start")

	// run/retry release a task now; costs list the ended runs (revoked ones included).
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "later", ProjectID: "p1", Title: "later", Assignee: "demo"}); err != nil {
		t.Fatal(err)
	}
	if out = cli("run", "later"); !strings.Contains(out, `"status": "READY"`) {
		t.Fatal(out)
	}
	if _, err = s.PriceRunCosts(ctx, co, nil); err != nil {
		t.Fatal(err)
	}
	out = cli("costs")
	if !strings.Contains(out, "5 runs") || strings.Count(out, "revoked") != 5 {
		t.Fatal(out)
	}
	if out = cli("costs", "--by", "agent", "--json"); !strings.Contains(out, `"key": "calorie-ios"`) || !strings.Contains(out, `"none": 5`) {
		t.Fatal(out)
	}
	if out = cli("costs", "--task", "tp1", "--day"); !strings.Contains(out, "DAY") || !strings.Contains(out, "1 runs") {
		t.Fatal(out)
	}
}
