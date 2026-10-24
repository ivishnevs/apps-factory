package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"appfactory/internal/artifacts"
	"appfactory/internal/protocol"
	"appfactory/internal/reports"
	"appfactory/internal/storage"
	"appfactory/internal/worker"
)

const testToken = "owner-secret-token"

// fakeCore serves the demo fixture under Core's owner routes and records the requests it gets.
type fakeCore struct {
	t        *testing.T
	f        Fixture
	mu       sync.Mutex
	requests []string
	bodies   map[string]map[string]any
	fail     map[string]int // path → status to answer instead
	agentGet int
}

func newFakeCore(t *testing.T) (*fakeCore, *httptest.Server, *HTTPClient) {
	fc := &fakeCore{t: t, f: DemoFixture(), bodies: map[string]map[string]any{}, fail: map[string]int{}}
	srv := httptest.NewServer(fc)
	t.Cleanup(srv.Close)
	c := &HTTPClient{API: worker.API{URL: srv.URL, Token: testToken}, Backoff: 10 * time.Millisecond, Now: func() time.Time { return DemoAt }}
	return fc, srv, c
}

func (fc *fakeCore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fc.mu.Lock()
	fc.requests = append(fc.requests, r.Method+" "+r.URL.String())
	status, failing := fc.fail[r.URL.Path]
	fc.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+testToken {
		http.Error(w, "unauthorized", 401)
		return
	}
	if failing {
		msg := "boom"
		if status == 409 {
			msg = storage.ErrConflict.Error()
		}
		http.Error(w, msg, status)
		return
	}
	reply := func(v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	ov := fc.f.Overview
	p := r.URL.Path
	if r.Method == "POST" {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		fc.mu.Lock()
		fc.bodies[p] = body
		fc.mu.Unlock()
		if p == "/tasks" {
			if body["title"] == "" {
				http.Error(w, "valid task id/title and priority -1000..1000 required", 400)
				return
			}
			reply(map[string]any{"id": "P1-10", "title": body["title"]})
			return
		}
		reply(map[string]any{"ok": true})
		return
	}
	switch {
	case p == "/health":
		reply(ov.Health)
	case p == "/status":
		reply(ov.Status)
	case p == "/agents":
		names := []string{}
		for _, a := range ov.Agents {
			names = append(names, a.Name)
		}
		reply(names)
	case strings.HasPrefix(p, "/agents/"):
		fc.mu.Lock()
		fc.agentGet++
		fc.mu.Unlock()
		name := strings.TrimPrefix(p, "/agents/")
		for _, a := range ov.Agents {
			if a.Name == name {
				reply(map[string]any{"metadata": map[string]any{"name": name}, "spec": map[string]any{"role": a.Role, "reports_to": a.ReportsTo, "runtime": map[string]any{"provider": a.Provider, "model": a.Model}}})
				return
			}
		}
		http.Error(w, "agent not found", 404)
	case p == "/tasks":
		reply(ov.Tasks)
	case p == "/pauses":
		reply(ov.Pauses)
	case p == "/pending/count":
		reply(ov.Pending)
	case p == "/nodes":
		reply(ov.Nodes)
	case p == "/inbox":
		reply(ov.Inbox)
	case p == "/approvals":
		reply(ov.Approvals)
	case p == "/costs":
		// The Costs screen and the recorder read the ledger (older than today's and the 24 h reads).
		q := r.URL.Query()
		var since int64
		fmt.Sscan(q.Get("since"), &since)
		by := q.Get("by")
		switch {
		case by == "day" || by == "model" || by == "project" || by == "agent" && since < startOfDay(DemoAt).Unix():
			v := groupCosts(fc.f.Costs, time.Unix(since, 0), time.UTC)
			reply(v.report(by))
		case by == "" && since > 0 && since < DemoAt.Add(-25*time.Hour).Unix():
			reply(storage.CostReport{Runs: fc.f.Costs})
		default:
			reply(ov.Costs)
		}
	case p == "/feed":
		c := &FixtureClient{F: fc.f}
		var after int64
		var limit int
		fmt.Sscan(r.URL.Query().Get("after"), &after)
		fmt.Sscan(r.URL.Query().Get("limit"), &limit)
		page, _ := c.Feed(r.Context(), FeedQuery{Task: r.URL.Query().Get("task"), Project: r.URL.Query().Get("project")}, after, limit)
		reply(page)
	case strings.HasPrefix(p, "/tasks/"):
		parts := strings.Split(strings.TrimPrefix(p, "/tasks/"), "/")
		d := fc.f.Details[parts[0]]
		if d.Task.ID == "" {
			http.Error(w, "not found", 404)
			return
		}
		switch {
		case len(parts) == 1:
			reply(d.Task)
		case parts[1] == "why":
			b, _ := json.Marshal(d.Why)
			var why map[string]any
			_ = json.Unmarshal(b, &why)
			if d.Queued != nil {
				why["queued"] = d.Queued
			}
			reply(why)
		case parts[1] == "parks":
			if d.Parks == nil {
				http.Error(w, "no route", 404) // an older Core
				return
			}
			reply(d.Parks)
		case parts[1] == "attempts":
			reply(d.Attempts)
		case parts[1] == "comments":
			reply(d.Comments)
		case parts[1] == "costs":
			reply(d.Costs)
		case parts[1] == "exits":
			reply(d.Exits)
		}
	case strings.HasPrefix(p, "/conversations/") && strings.HasSuffix(p, "/messages"):
		id := strings.TrimSuffix(strings.TrimPrefix(p, "/conversations/"), "/messages")
		msgs, ok := fc.f.Conversations[id]
		if !ok {
			http.Error(w, "conversation not found", 404)
			return
		}
		reply(msgs)
	case strings.HasPrefix(p, "/projects/") && strings.HasSuffix(p, "/content"):
		parts := strings.Split(strings.TrimPrefix(p, "/projects/"), "/")
		// /projects/{p}/artifacts/{a}/{v}/content
		b, ok := fc.f.Artifacts[parts[0]+"/"+parts[2]+"/"+parts[3]]
		if !ok {
			http.Error(w, "artifact not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte(b))
	case strings.HasPrefix(p, "/projects/") && strings.HasSuffix(p, "/reports"):
		// One report a page, so the client must follow next_after; task_id filters.
		project := strings.Split(strings.TrimPrefix(p, "/projects/"), "/")[0]
		var after int64
		fmt.Sscan(r.URL.Query().Get("after"), &after)
		page := storage.Page[reports.Report]{Items: []reports.Report{}, NextAfter: after}
		for i, rep := range fc.f.Reports[project] {
			if seq := int64(i + 1); seq > after && (r.URL.Query().Get("task_id") == "" || rep.TaskID == r.URL.Query().Get("task_id")) {
				if len(page.Items) == 1 {
					page.HasMore = true
					break
				}
				page.Items, page.NextAfter = append(page.Items, rep), seq
			}
		}
		reply(page)
	case strings.HasPrefix(p, "/projects/") && strings.Contains(p, "/artifacts"):
		parts := strings.Split(strings.TrimPrefix(p, "/projects/"), "/")
		if len(parts) == 2 {
			reply(append([]artifacts.Record{}, fc.f.ArtifactRecords[parts[0]]...))
			return
		}
		a, _ := (&FixtureClient{F: fc.f}).ArtifactApproval(r.Context(), parts[0], parts[2], parts[3])
		reply(map[string]any{"artifact": map[string]any{"id": parts[2]}, "approval": a})
	case strings.HasPrefix(p, "/blobs/"):
		b, ok := fc.f.Blobs[strings.TrimPrefix(p, "/blobs/")]
		if !ok {
			http.Error(w, "blob not found", 404)
			return
		}
		_, _ = w.Write([]byte(b))
	case strings.HasPrefix(p, "/runs/"):
		parts := strings.Split(strings.TrimPrefix(p, "/runs/"), "/")
		run, ok := fc.f.Runs[parts[0]]
		if !ok {
			http.Error(w, "not found", 404)
			return
		}
		switch {
		case len(parts) == 1:
			reply(run)
		case parts[1] == "tool-calls":
			reply(fc.f.ToolCalls[parts[0]])
		case parts[1] == "events":
			out := []protocol.Envelope{}
			for _, e := range fc.f.RunEvents[parts[0]] {
				// Core's stored events carry the worker lease; the recorder must drop it.
				var payload map[string]any
				_ = json.Unmarshal(e.Envelope.Payload, &payload)
				payload["lease"] = "lease-secret"
				e.Envelope.Payload, _ = json.Marshal(payload)
				out = append(out, e.Envelope)
			}
			reply(out)
		}
	default:
		http.Error(w, "no route "+p, 404)
	}
}

func (fc *fakeCore) saw(prefix string) int {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	n := 0
	for _, r := range fc.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func TestHTTPClientOverview(t *testing.T) {
	fc, _, c := newFakeCore(t)
	ov, err := c.Overview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ov.Health.Company != "Company 1" || len(ov.Status) != 3 || len(ov.Tasks) != 22 || len(ov.LiveRuns) != 5 || len(ov.Inbox) != 5 || len(ov.Nodes) != 3 || len(ov.Approvals) != 2 || ov.Errors != nil || ov.Health.DefaultReport != "pm" {
		t.Fatalf("%+v", ov)
	}
	if len(ov.Agents) != 7 || ov.Agents[1].Name != "developer-1" || ov.Agents[1].ReportsTo != "pm" || ov.Agents[1].Provider != "claude" {
		t.Fatalf("%+v", ov.Agents)
	}
	// Today's cost per agent: since the local midnight of the client's clock.
	if fc.saw(fmt.Sprintf("GET /costs?by=agent&since=%d", startOfDay(DemoAt).Unix())) != 1 {
		t.Fatal(fc.requests)
	}
	// The catalog is read once.
	if _, err = c.Overview(context.Background()); err != nil || fc.agentGet != 7 {
		t.Fatal(err, fc.agentGet)
	}
}

func TestHTTPClientOverviewPartsAndErrors(t *testing.T) {
	fc, srv, c := newFakeCore(t)
	fc.fail["/nodes"] = 500
	ov, err := c.Overview(context.Background())
	if err != nil || !strings.Contains(ov.Errors["nodes"], "500") || len(ov.Tasks) == 0 {
		t.Fatalf("%v %+v", err, ov.Errors)
	}
	// Core unreachable (health fails): the whole read fails.
	fc.fail["/health"] = 503
	if _, err = c.Overview(context.Background()); err == nil {
		t.Fatal("want error")
	}
	// A wrong token: the error never carries a token.
	bad := &HTTPClient{API: worker.API{URL: srv.URL, Token: "wrong-token-value"}}
	_, err = bad.Overview(context.Background())
	if err == nil || strings.Contains(err.Error(), "wrong-token-value") || !strings.Contains(err.Error(), "401") {
		t.Fatal(err)
	}
}

func TestHTTPClientTaskDetail(t *testing.T) {
	_, _, c := newFakeCore(t)
	d, err := c.TaskDetail(context.Background(), "P1-3", DemoFixture().Overview.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	if d.Task.ID != "P1-3" || len(d.Attempts) != 2 || len(d.Comments) != 2 || d.Why.Reason != "live_run" || d.Costs.Total.Runs != 1 {
		t.Fatalf("%+v", d)
	}
	for _, e := range d.Events {
		if e.TaskID != "P1-3" {
			t.Fatalf("%+v", e)
		}
	}
	if len(d.Events) != len(DemoFixture().Details["P1-3"].Events) {
		t.Fatal(len(d.Events))
	}
	d, err = c.TaskDetail(context.Background(), "P1-3", DemoFixture().Overview.Tasks)
	kids := childrenOf("P1-1", DemoFixture().Overview.Tasks)
	if err != nil || len(kids) != 6 || kids[0].ID != "P1-2" {
		t.Fatalf("%v %+v", err, kids)
	}
}

// TestHTTPClientActions: each owner action goes to its Core route with the task's version.
func TestHTTPClientActions(t *testing.T) {
	fc, _, c := newFakeCore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		a    Action
		path string
		want map[string]any
	}{
		{Action{Kind: "pause", TaskID: "T-1", Version: 3, Text: "hold"}, "/tasks/T-1/pause", map[string]any{"version": 3.0, "reason": "hold"}},
		{Action{Kind: "resume", TaskID: "T-1", Version: 4}, "/tasks/T-1/resume", map[string]any{"version": 4.0}},
		{Action{Kind: "retry", TaskID: "T-1", Version: 5}, "/tasks/T-1/actions", map[string]any{"version": 5.0, "action": "ready", "reason": "retry by owner"}},
		{Action{Kind: "run", TaskID: "T-2", Version: 1}, "/tasks/T-2/actions", map[string]any{"action": "ready", "reason": "run by owner"}},
		{Action{Kind: "cancel", TaskID: "T-3", Version: 2, Cascade: true}, "/tasks/T-3/actions", map[string]any{"action": "cancel", "cascade": true}},
		{Action{Kind: "reassign", TaskID: "T-1", Version: 6, Assignee: "developer-2"}, "/tasks/T-1/reassign", map[string]any{"assignee": "developer-2", "version": 6.0}},
		{Action{Kind: "reprioritize", TaskID: "T-1", Version: 7, Priority: 9}, "/tasks/T-1/reprioritize", map[string]any{"priority": 9.0}},
		{Action{Kind: "comment", TaskID: "T-1", Text: "looks good"}, "/tasks/T-1/comments", map[string]any{"text": "looks good"}},
		{Action{Kind: "direct", TaskID: "T-1", Text: "stop now", Urgent: true}, "/tasks/T-1/directives", map[string]any{"text": "stop now", "urgent": true}},
		{Action{Kind: "answer", MessageID: "m-1", Version: 2, Text: "usda-free", Answers: map[string]any{"answer": "usda-free"}}, "/inbox/m-1/answer", map[string]any{"body": "usda-free", "version": 2.0, "answers": map[string]any{"answer": "usda-free"}}},
		{Action{Kind: "approve", ApprovalID: "ap-1", Version: 1, Text: "ok"}, "/approvals/ap-1/decision", map[string]any{"action": "approve", "reason": "ok", "version": 1.0}},
		{Action{Kind: "reject", ApprovalID: "ap-1", Version: 1, Text: "no"}, "/approvals/ap-1/decision", map[string]any{"action": "reject"}},
		{Action{Kind: "stop", Text: "incident"}, "/stop", map[string]any{"reason": "incident"}},
		{Action{Kind: "start"}, "/pauses/resume", map[string]any{"scope": "company"}},
		{Action{Kind: "hold", Scope: "node", Target: "n1", Text: "maintenance"}, "/pauses", map[string]any{"scope": "node", "target": "n1", "reason": "maintenance"}},
		{Action{Kind: "hold", Scope: "quota_key", Target: "codex-team"}, "/pauses", map[string]any{"scope": "quota_key", "target": "codex-team"}},
		{Action{Kind: "release", Scope: "agent", Target: "designer"}, "/pauses/resume", map[string]any{"scope": "agent", "target": "designer"}},
		{Action{Kind: "read", MessageID: "m-note-1", Version: 1}, "/inbox/m-note-1/read", map[string]any{"version": 1.0}},
	} {
		if _, err := c.Do(ctx, tc.a); err != nil {
			t.Fatal(tc.a.Kind, err)
		}
		got := fc.bodies[tc.path]
		for k, v := range tc.want {
			if fmt.Sprint(got[k]) != fmt.Sprint(v) {
				t.Errorf("%s %s: %s = %v, want %v (%v)", tc.a.Kind, tc.path, k, got[k], v, got)
			}
		}
		if tc.a.TaskID != "" || tc.a.MessageID != "" || tc.a.ApprovalID != "" {
			if id, _ := got["request_id"].(string); id == "" {
				t.Errorf("%s: no request_id", tc.a.Kind)
			}
		}
	}
	if _, err := c.Do(ctx, Action{Kind: "explode"}); err == nil {
		t.Fatal("unknown action accepted")
	}
}

// sseServer answers each stream connection with the next script: lines written, then the connection closes.
type sseServer struct {
	mu      sync.Mutex
	scripts []string
	seen    []http.Header
	urls    []string
	status  int
}

func (s *sseServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.seen = append(s.seen, r.Header.Clone())
	s.urls = append(s.urls, r.URL.String())
	n := len(s.seen) - 1
	status := s.status
	s.mu.Unlock()
	if status != 0 {
		http.Error(w, "gone", status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	if n < len(s.scripts) {
		fmt.Fprint(w, s.scripts[n])
		w.(http.Flusher).Flush()
		return
	}
	<-r.Context().Done() // no more scripts: hold the connection open
}

func feedEvent(seq int64, kind string) string {
	b, _ := json.Marshal(storage.FeedItem{Sequence: seq, Type: kind, TaskID: "T"})
	return fmt.Sprintf("id: %d\nevent: feed\ndata: %s\n\n", seq, b)
}

// TestStreamFeedReconnect: a dropped stream reconnects with Last-Event-ID and delivers each row once.
func TestStreamFeedReconnect(t *testing.T) {
	s := &sseServer{scripts: []string{feedEvent(1, "a") + ": keepalive\n\n" + feedEvent(2, "b"), feedEvent(3, "c")}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := &HTTPClient{API: worker.API{URL: srv.URL, Token: testToken}, Backoff: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan StreamEvent, 32)
	go func() {
		c.StreamFeed(ctx, FeedQuery{Project: "p", Attention: true}, 0, func(e StreamEvent) { got <- e })
		close(got)
	}()
	types, states := []string{}, []bool{}
	for len(types) < 3 {
		select {
		case e := <-got:
			if e.State {
				states = append(states, e.Connected)
				if e.Connected == false && strings.Contains(e.Err, testToken) {
					t.Fatal("token in error")
				}
			} else if e.Feed != nil {
				types = append(types, e.Feed.Type)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("timeout", types, states)
		}
	}
	cancel()
	for range got {
	}
	if strings.Join(types, "") != "abc" || len(states) < 3 || !states[0] || states[1] || !states[2] {
		t.Fatal(types, states)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[0].Get("Last-Event-ID") != "" || s.seen[1].Get("Last-Event-ID") != "2" || s.seen[0].Get("Authorization") != "Bearer "+testToken {
		t.Fatal(s.seen)
	}
	if s.urls[0] != "/feed/stream?attention=1&project=p" {
		t.Fatal(s.urls)
	}
}

// TestStreamRunEnds: a run stream asks for its tail, delivers the events and returns at the end event.
func TestStreamRunEnds(t *testing.T) {
	env := protocol.Wrap("agent.progress", "r1", "", protocol.Event{RunID: "r1", Method: "claude/assistant"})
	b, _ := json.Marshal(env)
	s := &sseServer{scripts: []string{fmt.Sprintf("id: 7\nevent: run\ndata: %s\n\nevent: end\ndata: {\"status\":\"ENDED\",\"outcome\":\"succeeded\"}\n\n", b)}}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := &HTTPClient{API: worker.API{URL: srv.URL, Token: testToken}, Backoff: 5 * time.Millisecond}
	events := []StreamEvent{}
	done := make(chan struct{})
	go func() {
		c.StreamRun(context.Background(), "r1", 0, 3, func(e StreamEvent) { events = append(events, e) })
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not end")
	}
	if len(events) != 3 || !events[0].State || events[1].Run == nil || events[1].Run.Seq != 7 || !events[2].End || events[2].Outcome != "succeeded" || events[1].Stream != "run:r1" {
		t.Fatalf("%+v", events)
	}
	if s.urls[0] != "/runs/r1/events/stream?tail=3" {
		t.Fatal(s.urls)
	}
}

// TestStreamGivesUpOnNotFound: an unknown run is not retried forever.
func TestStreamGivesUpOnNotFound(t *testing.T) {
	s := &sseServer{status: 404}
	srv := httptest.NewServer(s)
	defer srv.Close()
	c := &HTTPClient{API: worker.API{URL: srv.URL, Token: testToken}, Backoff: time.Millisecond}
	var last StreamEvent
	c.StreamRun(context.Background(), "nope", 0, 0, func(e StreamEvent) { last = e })
	if !last.State || last.Connected || !strings.Contains(last.Err, "404") {
		t.Fatalf("%+v", last)
	}
}

func TestParseSSE(t *testing.T) {
	in := ": comment\nid: 5\nevent: feed\ndata: {\"a\":1}\ndata: {\"b\":2}\n\nevent: end\ndata: x\n\ndata: plain\n\n"
	got := []sseMessage{}
	if err := parseSSE(strings.NewReader(in), func(m sseMessage) { got = append(got, m) }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != 5 || string(got[0].Data) != "{\"a\":1}\n{\"b\":2}" || got[1].Event != "end" || got[2].Event != "message" {
		t.Fatalf("%+v", got)
	}
}

// TestRecordFixture: recording reads the overview, the feed, task details and runs; leases are dropped, and
// the file loads back.
func TestRecordFixture(t *testing.T) {
	_, _, c := newFakeCore(t)
	fc := c
	// Only P1-3 has a detail in the fake; record just reads what exists.
	fx := DemoFixture()
	tasks := []Task{}
	for _, tk := range fx.Overview.Tasks {
		if tk.ID == "P1-3" {
			tasks = append(tasks, tk)
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tasks" {
			_ = json.NewEncoder(w).Encode(tasks)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = r.URL.Path
		proxy := &fakeCore{t: t, f: fx, bodies: map[string]map[string]any{}, fail: map[string]int{}}
		proxy.ServeHTTP(w, r2)
	}))
	defer srv.Close()
	fc.API.URL = srv.URL
	f, err := Record(context.Background(), fc)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Feed) != len(fx.Feed) || len(f.Details) != 1 || f.Details["P1-3"].Task.ID != "P1-3" {
		t.Fatalf("%d %d", len(f.Feed), len(f.Details))
	}
	if len(f.Costs) != len(fx.Costs) {
		t.Fatalf("cost ledger: %d rows, want %d", len(f.Costs), len(fx.Costs))
	}
	if _, ok := f.Runs["r-d1-7"]; !ok || len(f.RunEvents["r-d1-7"]) != len(fx.RunEvents["r-d1-7"]) || len(f.ToolCalls["r-d1-7"]) != 3 {
		t.Fatalf("%v", f.Runs)
	}
	path := filepath.Join(t.TempDir(), "rec")
	if err = SaveFixture(path, f); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(path, "fixture.json"))
	if strings.Contains(string(b), "lease-secret") || strings.Contains(string(b), testToken) {
		t.Fatal("recorded a secret")
	}
	st, _ := os.Stat(filepath.Join(path, "fixture.json"))
	if st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	back, err := LoadFixture(path)
	if err != nil || len(back.Feed) != len(f.Feed) || back.Overview.Health.Company != "Company 1" {
		t.Fatal(err)
	}
}

// TestDemoFixtureRoundTrip: the demo fixture survives JSON (what --fixture loads).
func TestDemoFixtureRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "demo.json")
	if err := SaveFixture(path, DemoFixture()); err != nil {
		t.Fatal(err)
	}
	f, err := LoadFixture(path)
	if err != nil {
		t.Fatal(err)
	}
	if f.Name != "demo" || len(f.Overview.Tasks) != 22 || len(f.RunEvents["r-d1-7"]) != len(DemoFixture().RunEvents["r-d1-7"]) || f.Details["P1-3"].Why.Reason != "live_run" {
		t.Fatalf("%+v", f.Overview.Tasks)
	}
	if _, err = LoadFixture(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("missing fixture loaded")
	}
}

// TestHTTPClientChatTranscriptLinks: the chat reads a conversation (none yet is empty, not an error) and
// sends through POST /messages; the transcript reads the run's stored events; the task detail carries its
// exits' pull request; an artifact's content downloads.
func TestHTTPClientChatTranscriptLinks(t *testing.T) {
	fc, _, c := newFakeCore(t)
	ctx := context.Background()
	msgs, err := c.Messages(ctx, "owner-pm-project-1")
	if err != nil || len(msgs) != 3 || msgs[1].Sender != "owner" {
		t.Fatal(err, msgs)
	}
	if msgs, err = c.Messages(ctx, "owner-qa-project-1"); err != nil || msgs != nil {
		t.Fatal("a missing conversation:", err, msgs)
	}
	if _, err = c.Do(ctx, Action{Kind: "say", Project: "project-2", Text: "@lead ship it"}); err != nil {
		t.Fatal(err)
	}
	if b := fc.bodies["/messages"]; b["project_id"] != "project-2" || b["body"] != "@lead ship it" || b["request_id"] == "" {
		t.Fatal(b)
	}
	events, err := c.RunEvents(ctx, "r-d1-7")
	if err != nil || len(events) != len(DemoFixture().RunEvents["r-d1-7"]) || events[0].Seq != 1 {
		t.Fatal(err, len(events))
	}
	if tally := tallyErrors(transcriptFromEvents(events, DemoFixture().ToolCalls["r-d1-7"])); tally.N != 3 {
		t.Fatalf("%+v", tally)
	}
	d, err := c.TaskDetail(ctx, "P2-1", DemoFixture().Overview.Tasks)
	if pr := taskPR(d); err != nil || pr == nil || pr.Number != 12 || pr.State != "open" {
		t.Fatal(err, d.Exits)
	}
	b, err := c.ArtifactContent(ctx, "project-2", "release-notes", "v2")
	if err != nil || !strings.HasPrefix(string(b), "# Project 2") {
		t.Fatal(err, string(b))
	}
	if _, err = c.ArtifactContent(ctx, "project-2", "nope", "v1"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatal(err)
	}
}

// TestHTTPClientMaterials (owner-web 2.6): reports are read across pages and narrowed to a task, artifact
// versions with their approval, a task's exits, and blobs by digest.
func TestHTTPClientMaterials(t *testing.T) {
	_, _, c := newFakeCore(t)
	ctx := context.Background()
	reps, err := c.Reports(ctx, "project-1", "")
	if err != nil || len(reps) != 2 {
		t.Fatal(err, len(reps))
	}
	if reps, err = c.Reports(ctx, "project-1", "P1-8"); err != nil || len(reps) != 0 {
		t.Fatal(err, reps)
	}
	recs, err := c.Artifacts(ctx, "project-2")
	if err != nil || len(recs) != 1 || recs[0].TaskID != "P2-4" {
		t.Fatal(err, recs)
	}
	a, err := c.ArtifactApproval(ctx, "project-2", "release-notes", "v2")
	if err != nil || a == nil || a.Status != "PENDING" {
		t.Fatal(err, a)
	}
	if a, err = c.ArtifactApproval(ctx, "project-1", "module-1-prototype", "v3"); err != nil || a != nil {
		t.Fatal(err, a)
	}
	exits, err := c.TaskExits(ctx, "P2-6")
	if err != nil || len(exits) != 1 || exits[0].Facts.PR.State != "merged" {
		t.Fatal(err, exits)
	}
	att := DemoFixture().Reports["project-1"][0].Attachments[0]
	b, err := c.Blob(ctx, att.SHA256)
	if err != nil || int64(len(b)) != att.Size || !strings.HasPrefix(string(b), "# Module 1") {
		t.Fatal(err, len(b))
	}
	if _, err = c.Blob(ctx, "../tasks"); err == nil {
		t.Fatal("a path is not a digest")
	}
}

// TestHTTPClientCreateTask: the form's action is POST /tasks with the fields, dependencies, ready and a
// request_id; Core's refusal comes back as the error.
func TestHTTPClientCreateTask(t *testing.T) {
	fc, _, c := newFakeCore(t)
	text, err := c.Do(context.Background(), Action{Kind: "create", Project: "project-1", Title: "Add export", Text: "CSV export\nof items", Assignee: "developer-1", Priority: 7, Deps: []string{"P1-3"}, Ready: true})
	if err != nil || text != "created P1-10: Add export" {
		t.Fatal(text, err)
	}
	b := fc.bodies["/tasks"]
	if b["project_id"] != "project-1" || b["title"] != "Add export" || b["description"] != "CSV export\nof items" || b["assignee"] != "developer-1" || b["priority"] != 7.0 || b["ready"] != true || b["request_id"] == "" {
		t.Fatal(b)
	}
	if deps, _ := b["dependencies"].([]any); len(deps) != 1 || deps[0] != "P1-3" {
		t.Fatal(b["dependencies"])
	}
	if _, err = c.Do(context.Background(), Action{Kind: "create", Project: "project-1"}); err == nil || !strings.Contains(err.Error(), "title") {
		t.Fatal(err)
	}
	if b := fc.bodies["/tasks"]; b["ready"] != nil || len(b["dependencies"].([]any)) != 0 {
		t.Fatal("a backlog task without dependencies:", b)
	}
}
