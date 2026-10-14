package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

func TestOwnerControlsAgainstCore(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
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
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/owner-controls"}); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		return capture(t, func() error { return ownerCommand(ctx, api, args) })
	}
	run("pause", "--all", "night")
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Task(ctx, co, "a"); a.Status != "READY" {
		t.Fatal("dispatched while paused", a.Status)
	}
	if out := run("pauses"); !strings.Contains(out, `"scope": "company"`) || !strings.Contains(out, "night") {
		t.Fatal(out)
	}
	run("start")
	if err = c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := s.Task(ctx, co, "a")
	if a.Status != "IN_PROGRESS" {
		t.Fatal("not dispatched after start", a.Status)
	}
	if _, err = s.Claim(ctx, co, a.CurrentRunID); err != nil {
		t.Fatal(err)
	}
	q, err := s.SendMessage(ctx, co, messaging.Send{ProjectID: "p", TaskID: "a", RunID: a.CurrentRunID, Sender: "demo", Recipient: "owner", MessageType: "question", Body: "Which database?", Blocking: true, RequestID: "q"})
	if err != nil {
		t.Fatal(err)
	}
	if out := run("pending"); !strings.Contains(out, `"inbox": 1`) || !strings.Contains(out, `"total": 1`) {
		t.Fatal(out)
	}
	run("answer", q.ID, "SQLite", "for", "now")
	if m, _ := s.Message(ctx, co, q.ID); m.Status != "ANSWERED" {
		t.Fatal("not answered", m.Status)
	}
	if out := run("stop", "incident"); !strings.Contains(out, `"revoked": 1`) {
		t.Fatal(out)
	}
	if r, _ := s.Get(ctx, co, a.CurrentRunID); r.Status != "CANCELLING" {
		t.Fatal(r.Status)
	}

	// The feed stream resumes after Last-Event-ID without repeats.
	stream := func(lastID string, want int) []storage.FeedItem {
		t.Helper()
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(reqCtx, "GET", server.URL+"/feed/stream?project=p", nil)
		req.Header.Set("Authorization", "Bearer owner")
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatal(resp.Header)
		}
		items := []storage.FeedItem{}
		sc := bufio.NewScanner(resp.Body)
		for len(items) < want && sc.Scan() {
			if data, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
				var item storage.FeedItem
				if err = json.Unmarshal([]byte(data), &item); err != nil {
					t.Fatal(err)
				}
				items = append(items, item)
			}
		}
		if len(items) < want {
			t.Fatalf("stream ended after %d of %d events", len(items), want)
		}
		return items
	}
	page, _ := s.Feed(ctx, co, storage.FeedFilter{Project: "p"}, 0, 100)
	all := stream("", len(page.Items))
	last := all[len(all)-1].Sequence
	go func() {
		time.Sleep(300 * time.Millisecond)
		_, _ = s.CreateTask(ctx, co, tasks.Create{ID: "later", ProjectID: "p", Title: "later"})
	}()
	next := stream(fmtInt(last), 1)
	if next[0].EntityID != "later" || next[0].Sequence <= last {
		t.Fatal("stream repeated or skipped", next[0])
	}
}

func fmtInt(n int64) string { b, _ := json.Marshal(n); return string(b) }

// TestEscalationsCLI: factory escalations lists open escalations and factory answer MSG OPTION answers
// one with its single choice; an option outside the list is refused before reaching Core.
func TestEscalationsCLI(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Agents: map[string]config.Agent{}, Token: "owner"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := worker.API{URL: server.URL, Token: "owner"}
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: "/tmp/owner-escalations"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "dep", ProjectID: "p", Title: "dep"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "a", ProjectID: "p", Title: "a", Assignee: "demo", Ready: true, Dependencies: []string{"dep"}}); err != nil {
		t.Fatal(err)
	}
	if err = s.SyncEscalations(ctx, co, "stalled", []storage.EscalationWant{{TaskID: "a", RefID: "dep", Summary: "waits on dep, which is in BACKLOG"}}); err != nil {
		t.Fatal(err)
	}
	var open []storage.Escalation
	out := capture(t, func() error { return ownerCommand(ctx, api, []string{"escalations"}) })
	if err = json.Unmarshal([]byte(out), &open); err != nil || len(open) != 1 || open[0].Target != "owner" {
		t.Fatal(out, err)
	}
	if err = ownerCommand(ctx, api, []string{"answer", open[0].MessageID, "maybe"}); err == nil || !strings.Contains(err.Error(), "unblock, cancel") {
		t.Fatal(err)
	}
	capture(t, func() error { return ownerCommand(ctx, api, []string{"answer", open[0].MessageID, "unblock"}) })
	if a, _ := s.Task(ctx, co, "a"); len(a.Dependencies) != 0 {
		t.Fatal(a.Dependencies)
	}
	if out = capture(t, func() error { return ownerCommand(ctx, api, []string{"escalations", "--all"}) }); !strings.Contains(out, `"status": "answered"`) {
		t.Fatal(out)
	}
}
