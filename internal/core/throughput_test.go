package core

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/clock"
	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestThroughputSweep replays the night of 2026-11-07 with fakes (throughput-signals 1.1, 4.1): demo (limit 1) has
// one live run and four READY tasks waiting for its limit while the node has free slots, and three released spec
// tasks wait behind one verification. The stall sweep says nothing for 30 minutes; then the project's manager gets
// one notice naming demo's queue, limit and the free slots and the verification with the tasks behind it, and the
// owner gets one alert per cause; later sweeps repeat nothing; the gate's alert clears when the gate closes.
func TestThroughputSweep(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	fake := clock.NewFake(time.Unix(1_800_000_000, 0))
	s.Clock = fake
	s.LeadOf = func(company, team string) string { return "manager" }
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &Core{Store: s, Agents: agents, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "game", Name: "Game", Workspace: "/tmp/core-throughput"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RegisterNode(ctx, co, storage.NodeRegistration{NodeID: "n1", Name: "mac", Epoch: 1, ProtocolVersion: 2, Slots: 5,
		Drivers: []storage.NodeDriver{{Name: "codex", Version: "codex-cli 0.162.1", OK: true}}}); err != nil {
		t.Fatal(err)
	}
	create := func(id, assignee string, deps ...string) {
		t.Helper()
		if _, err := s.CreateTask(ctx, co, tasks.Create{ID: id, ProjectID: "game", Title: id, Assignee: assignee, Ready: true, Dependencies: deps, RequestID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"net1", "net2", "net3", "net4", "net5"} {
		create(id, "demo")
	}
	create("verify", "manager-autonomous")
	if err = s.SetPause(ctx, co, storage.Pause{Scope: "agent", Target: "manager-autonomous", Reason: "keep the gate open", Actor: "owner"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"spec1", "spec2", "spec3"} {
		create(id, "manager", "verify")
	}
	// demo's first task runs on n1.
	snapshot, _, err := c.RunSnapshot(agents["demo"])
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.Task(ctx, co, "net1")
	if _, err = s.Dispatch(ctx, co, "net1", v.Version, protocol.RunSpec{ID: "r1", Project: "game", Agent: "demo", Provider: protocol.RuntimeCodex, ConfigSnapshot: snapshot}); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimV2(ctx, co, "r1", storage.ClaimRequest{NodeID: "n1", Epoch: 1, ClaimID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.StartRun(ctx, co, "r1", claimed.Lease); err != nil {
		t.Fatal(err)
	}
	if snap, _ := s.TaskSnapshot(ctx, co, "net2"); c.evaluate(snap).Reason != tasks.ReasonCapacity {
		t.Fatalf("net2 %+v", c.evaluate(snap))
	}
	alerts := func() map[string]string {
		t.Helper()
		page, err := s.Feed(ctx, co, storage.FeedFilter{}, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, it := range page.Items {
			switch it.Type {
			case "task.alert.throughput":
				out[it.Ref] = it.Summary
			case "task.alert_cleared.throughput":
				delete(out, it.Ref)
			}
		}
		return out
	}
	noticesTo := func(agent string) []string {
		t.Helper()
		rows, err := s.DB.QueryContext(ctx, "SELECT body->>'body' FROM messages WHERE company_id=? AND sender='factory' AND recipient=? AND body->>'body' LIKE 'Throughput:%'", co, agent)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var b string
			_ = rows.Scan(&b)
			out = append(out, b)
		}
		return out
	}
	sweep := func(after time.Duration) {
		t.Helper()
		fake.Advance(after)
		if err := c.sweepStalls(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sweep(time.Minute)
	if len(alerts()) != 0 || len(noticesTo("manager")) != 0 {
		t.Fatal("throughput signal within the threshold")
	}
	sweep(30 * time.Minute)
	got := alerts()
	if len(got) != 2 || !strings.HasPrefix(got["game/agent_busy:demo"], "demo: 4 queued, limit 1, 4 free slots (tasks net2, net3, net4, net5); raise the agent's limit") ||
		!strings.HasPrefix(got["game/gate:verify"], `3 tasks wait behind verify "verify" (READY, manager-autonomous) (tasks spec1, spec2, spec3)`) {
		t.Fatalf("alerts %+v", got)
	}
	ns := noticesTo("manager")
	if len(ns) != 1 || !strings.Contains(ns[0], "demo: 4 queued, limit 1, 4 free slots") || !strings.Contains(ns[0], "3 tasks wait behind verify") || !strings.Contains(ns[0], "Factory changes nothing") {
		t.Fatalf("notices %q", ns)
	}
	// The API shows the same causes, due.
	var bs []storage.Bottleneck
	r := httptest.NewRequest("GET", "/throughput?project=game", nil)
	r.Header.Set("Authorization", "Bearer owner")
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, r)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &bs) != nil || len(bs) != 2 || !bs[0].Due || !bs[1].Due {
		t.Fatalf("GET /throughput %d %s", w.Code, w.Body.String())
	}
	// The Inbox's Alerts list them with what releases them, marked with the project's lead (inbox-badge-parity).
	listed, err := c.alerts(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range listed {
		if a.Kind == "throughput" {
			n++
			if a.Action == "" || a.With == "owner" {
				t.Fatalf("listed %+v", a)
			}
		}
	}
	if n != 2 {
		t.Fatalf("listed %+v", listed)
	}
	sweep(time.Minute)
	sweep(time.Minute)
	if len(alerts()) != 2 || len(noticesTo("manager")) != 1 {
		t.Fatal("the episode repeated itself")
	}
	// The gate closes: its alert clears, the limit's stays.
	if err = s.ClearPause(ctx, co, "agent", "manager-autonomous", "owner"); err != nil {
		t.Fatal(err)
	}
	v, _ = s.Task(ctx, co, "verify")
	if _, err = s.TaskAction(ctx, co, "verify", tasks.Action{Version: v.Version, Action: "cancel"}); err != nil {
		t.Fatal(err)
	}
	sweep(time.Minute)
	if got = alerts(); len(got) != 1 || got["game/agent_busy:demo"] == "" {
		t.Fatalf("after the gate closed %+v", got)
	}
}

// A person's task (people-assignees) is never a capacity cause, whatever its verdict.
func TestTaskWaitSkipsPeople(t *testing.T) {
	v := tasks.Verdict{Reason: tasks.ReasonCapacity, Wait: &tasks.Wait{Kind: tasks.WaitAgent, Agent: "member:alice", Limit: 1}}
	if w := taskWait(tasks.Task{ID: "x", ProjectID: "p", Assignee: "member:alice"}, v); w != nil {
		t.Fatalf("person's task counted: %+v", w)
	}
	if w := taskWait(tasks.Task{ID: "x", ProjectID: "p", Assignee: "dev"}, v); w == nil {
		t.Fatal("agent's capacity wait dropped")
	}
}
