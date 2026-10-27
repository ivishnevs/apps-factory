package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"appfactory/internal/tui"
)

func demo(t *testing.T) (*httptest.Server, *tui.FixtureClient) {
	t.Helper()
	c := &tui.FixtureClient{F: tui.DemoFixture()}
	srv := httptest.NewServer((&Server{Client: c, Mode: "demo"}).Handler())
	t.Cleanup(srv.Close)
	return srv, c
}

func getJSON(t *testing.T, url string, v any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if v != nil {
		if err = json.NewDecoder(res.Body).Decode(v); err != nil {
			t.Fatal(url, err)
		}
	}
	return res.StatusCode
}

// TestReads (owner-web 1.2): every read answers the fixture's data as JSON.
func TestReads(t *testing.T) {
	srv, c := demo(t)
	var o tui.Overview
	if code := getJSON(t, srv.URL+"/api/overview", &o); code != 200 || len(o.Tasks) == 0 || len(o.Agents) == 0 {
		t.Fatal(code, len(o.Tasks))
	}
	var d tui.TaskDetail
	if code := getJSON(t, srv.URL+"/api/tasks/"+o.Tasks[0].ID, &d); code != 200 || d.Task.ID != o.Tasks[0].ID {
		t.Fatal(code, d.Task.ID)
	}
	var run string
	for id := range c.F.RunEvents {
		run = id
		break
	}
	var events []tui.RunEvent
	if code := getJSON(t, srv.URL+"/api/runs/"+run+"/events", &events); code != 200 || len(events) == 0 {
		t.Fatal(code, len(events))
	}
	var feed map[string]any
	if code := getJSON(t, srv.URL+"/api/feed?limit=5", &feed); code != 200 {
		t.Fatal(code)
	}
	var costs tui.CostView
	if code := getJSON(t, srv.URL+"/api/costs?since=168h", &costs); code != 200 {
		t.Fatal(code)
	}
	var bad map[string]string
	if code := getJSON(t, srv.URL+"/api/costs?since=week", &bad); code != 400 || bad["error"] == "" {
		t.Fatal(code, bad)
	}
	var meta map[string]string
	if getJSON(t, srv.URL+"/api/meta", &meta); meta["mode"] != "demo" {
		t.Fatal(meta)
	}
}

// TestActionNeedsTheWebHeader (owner-web): an action without X-Factory-Web is refused and not performed;
// with it, the client receives the action.
func TestActionNeedsTheWebHeader(t *testing.T) {
	srv, c := demo(t)
	body := `{"Kind":"comment","TaskID":"t1","Text":"hello"}`
	res, err := http.Post(srv.URL+"/api/action", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 400 || len(c.Sent) != 0 {
		t.Fatal(res.StatusCode, c.Sent)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/action", strings.NewReader(body))
	req.Header.Set("X-Factory-Web", "1")
	if res, err = http.DefaultClient.Do(req); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || len(c.Sent) != 1 || c.Sent[0].Kind != "comment" || c.Sent[0].Text != "hello" {
		t.Fatal(res.StatusCode, c.Sent)
	}
}

// TestCreateProject (project-creation 2.1): the web form's create_project action carries the POST /projects
// body with its request id; the demo adds the project to the overview and refuses a taken ID with a 502 and
// Core's message.
func TestCreateProject(t *testing.T) {
	srv, c := demo(t)
	post := func(body string) (int, map[string]string) {
		req, _ := http.NewRequest("POST", srv.URL+"/api/action", strings.NewReader(body))
		req.Header.Set("X-Factory-Web", "1")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]string
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	body := `{"Kind":"create_project","NewProject":{"id":"advent-gift","name":"Advent gift","description":"","request_id":"r-1","openspec":{"enabled":true},"repo_mode":"existing","repo_url":"https://github.com/me/advent","audit":true}}`
	code, out := post(body)
	if code != 200 || out["result"] != "created project advent-gift: Advent gift" {
		t.Fatal(code, out)
	}
	p := c.Sent[0].NewProject
	if p == nil || p.RequestID != "r-1" || p.RepoMode != "existing" || p.RepoURL != "https://github.com/me/advent" || !p.Audit || !*p.OpenSpec.Enabled {
		t.Fatalf("%+v", p)
	}
	var ov tui.Overview
	getJSON(t, srv.URL+"/api/overview", &ov)
	if last := ov.Status[len(ov.Status)-1]; last.Project.ID != "advent-gift" || last.Tasks["READY"] != 1 {
		t.Fatalf("%+v", last)
	}
	if code, out = post(body); code != 502 || !strings.Contains(out["error"], "exists") {
		t.Fatal(code, out)
	}
}

// TestFeedStream: the feed stream starts with a state event and relays feed rows as SSE.
func TestFeedStream(t *testing.T) {
	srv, _ := demo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/stream/feed", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatal(ct)
	}
	sc := bufio.NewScanner(res.Body)
	var kinds []string
	for sc.Scan() && len(kinds) < 2 {
		if k, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) < 2 || kinds[0] != "state" || kinds[1] != "feed" {
		t.Fatal(kinds)
	}
}
