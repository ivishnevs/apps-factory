// Package tui is the owner's full-screen terminal UI (factory ui): dashboard, tasks, live runs, the flow of
// work between agents, the feed and the inbox. The Client is the API layer; views only read its types.
package tui

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"appfactory/internal/worker"
)

// Task is a task as GET /tasks lists it: the task with why it does not run now and who clears that.
type Task struct {
	tasks.Task
	Reason  string        `json:"reason"`
	Clearer tasks.Clearer `json:"clearer"`
}

// Agent is one catalog agent as the UI shows it.
type Agent struct {
	Name      string `json:"name"`
	Role      string `json:"role,omitempty"`
	ReportsTo string `json:"reports_to,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
}

// Node is one entry of GET /nodes.
type Node struct {
	storage.Node
	InUse storage.NodeUse `json:"in_use"`
}

// Health is GET /health.
type Health struct {
	Status        string `json:"status"`
	Database      string `json:"database,omitempty"`
	Company       string `json:"company"`
	ExplicitExits string `json:"explicit_exits,omitempty"`
	// DefaultReport is owner.default_report: where an unaddressed owner message goes.
	DefaultReport string `json:"default_report,omitempty"`
}

// Overview is everything the dashboard and the status bar show, read in one refresh.
type Overview struct {
	Health  Health                `json:"health"`
	Status  []core.ProjectStatus  `json:"status"`
	Agents  []Agent               `json:"agents"`
	Tasks   []Task                `json:"tasks"`
	Pauses  []storage.Pause       `json:"pauses"`
	Pending storage.PendingCounts `json:"pending"`
	Nodes   []Node                `json:"nodes"`
	Costs   storage.CostReport    `json:"costs_today"`
	// TaskCosts are the ended runs' cost per task (all time); RecentCosts the runs that ended in the last 24 h.
	TaskCosts   storage.CostReport `json:"task_costs"`
	RecentCosts storage.CostReport `json:"recent_costs"`
	// LiveRuns are the live runs' records (usage so far), by run ID.
	LiveRuns  map[string]storage.Run `json:"live_runs,omitempty"`
	Inbox     []messaging.Message    `json:"inbox"`
	Approvals []approvals.Approval   `json:"approvals"`
	At        time.Time              `json:"at"`
	Errors    map[string]string      `json:"errors,omitempty"`
}

// TaskDetail is one task's detail pane.
type TaskDetail struct {
	Task     Task               `json:"task"`
	Why      tasks.Verdict      `json:"why"`
	Attempts []storage.Run      `json:"attempts"`
	Comments []storage.Comment  `json:"comments"`
	Costs    storage.CostReport `json:"costs"`
	Events   []storage.FeedItem `json:"events"`
	Children []Task             `json:"children,omitempty"`
	// Exits are the accepted exits with their git and forge facts (the pull request); nil from an older Core.
	Exits []storage.TaskExit `json:"exits,omitempty"`
}

// RunEvent is one event of a run stream: its sequence and envelope.
type RunEvent struct {
	Seq      int64             `json:"seq"`
	Envelope protocol.Envelope `json:"envelope"`
}

// Action is an owner action the UI sends.
type Action struct {
	Kind       string         // pause, resume, retry, run, cancel, reassign, reprioritize, comment, direct, answer, approve, reject, stop, start, say, create
	TaskID     string         // task actions
	Project    string         // say, create
	Title      string         // create (Text is the description)
	Deps       []string       // create: dependencies
	Ready      bool           // create: released at once
	Version    int            // task, message or approval version
	Text       string         // reason, comment, directive, answer body
	Assignee   string         // reassign
	Priority   int            // reprioritize
	Urgent     bool           // direct
	Cascade    bool           // cancel
	MessageID  string         // answer
	Answers    map[string]any // answer: structured answers
	ApprovalID string         // approve, reject
}

// Describe is the action in words, for confirmations and toasts.
func (a Action) Describe() string {
	switch a.Kind {
	case "comment":
		return "comment on " + a.TaskID + ": " + a.Text
	case "direct":
		u := ""
		if a.Urgent {
			u = " (urgent)"
		}
		return "directive to " + a.TaskID + u + ": " + a.Text
	case "reassign":
		return "reassign " + a.TaskID + " to " + a.Assignee
	case "reprioritize":
		return fmt.Sprintf("reprioritize %s to %d", a.TaskID, a.Priority)
	case "cancel":
		if a.Cascade {
			return "cancel " + a.TaskID + " and its open children"
		}
		return "cancel " + a.TaskID
	case "answer":
		return "answer " + a.MessageID + ": " + a.Text
	case "approve", "reject":
		return a.Kind + " " + a.ApprovalID + ": " + a.Text
	case "say":
		return "message in " + a.Project + ": " + a.Text
	case "create":
		r := ""
		if a.Ready {
			r = ", ready"
		}
		return fmt.Sprintf("create task %q in %s for %s (priority %d%s)", a.Title, a.Project, a.Assignee, a.Priority, r)
	case "stop":
		return "STOP the company (kill switch): pause everything and revoke live runs"
	case "start":
		return "start the company (lift the kill switch)"
	}
	return a.Kind + " task " + a.TaskID
}

// FeedQuery narrows a feed read or stream on the server.
type FeedQuery struct {
	Project, Task, Subtree string
	Attention              bool
}

func (q FeedQuery) values() url.Values {
	v := url.Values{}
	if q.Project != "" {
		v.Set("project", q.Project)
	}
	if q.Task != "" {
		v.Set("task", q.Task)
	}
	if q.Subtree != "" {
		v.Set("subtree", q.Subtree)
	}
	if q.Attention {
		v.Set("attention", "1")
	}
	return v
}

// StreamEvent is what a stream delivers: one feed item or run event, the end of a run, or a change of the
// connection state.
type StreamEvent struct {
	Stream    string // "feed" or "run:<id>"
	Feed      *storage.FeedItem
	Run       *RunEvent
	End       bool   // the run ended; the stream closes
	Outcome   string // with End
	Connected bool   // state change (Feed, Run and End are nil)
	State     bool   // this is a state change
	Err       string
}

// Client is the Core API the UI uses.
type Client interface {
	Describe() string
	Overview(ctx context.Context) (Overview, error)
	TaskDetail(ctx context.Context, id string, all []Task) (TaskDetail, error)
	Run(ctx context.Context, id string) (storage.Run, error)
	ToolCalls(ctx context.Context, run string) ([]storage.ToolCall, error)
	Feed(ctx context.Context, q FeedQuery, after int64, limit int) (storage.Page[storage.FeedItem], error)
	// StreamFeed and StreamRun deliver events to out until ctx ends (a run stream also ends with the run);
	// they reconnect after an error, resuming after the last event they delivered.
	StreamFeed(ctx context.Context, q FeedQuery, after int64, out func(StreamEvent))
	StreamRun(ctx context.Context, run string, after int64, tail int, out func(StreamEvent))
	Do(ctx context.Context, a Action) (string, error)
	// RunEvents are a run's stored events, oldest first (the full runtime transcript Core keeps).
	RunEvents(ctx context.Context, run string) ([]RunEvent, error)
	// Messages are a conversation's messages, oldest first; an unknown conversation has none.
	Messages(ctx context.Context, conversation string) ([]messaging.Message, error)
	// ArtifactContent is an uploaded artifact version's content.
	ArtifactContent(ctx context.Context, project, artifact, version string) ([]byte, error)
}

// HTTPClient talks to a running Core with the owner token (FACTORY_URL, FACTORY_TOKEN). The token is only
// sent in the Authorization header; it is never part of an error, a URL or the screen.
type HTTPClient struct {
	API worker.API
	// Stream is the client for event streams (no timeout); nil uses a default one.
	Stream *http.Client
	// Backoff is the first reconnect delay of a stream (doubling up to 15 s); 0 means 1 s.
	Backoff time.Duration
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time

	agents []Agent
}

func (c *HTTPClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *HTTPClient) Describe() string { return c.API.URL }

func (c *HTTPClient) get(ctx context.Context, path string, v any) error {
	return c.API.Do(ctx, "GET", path, nil, v)
}

// Agents reads the catalog once: names, then each agent's reports_to, role and runtime.
func (c *HTTPClient) Agents(ctx context.Context) ([]Agent, error) {
	if c.agents != nil {
		return c.agents, nil
	}
	var names []string
	if err := c.get(ctx, "/agents", &names); err != nil {
		return nil, err
	}
	out := make([]Agent, 0, len(names))
	for _, n := range names {
		var a config.Agent
		if err := c.get(ctx, "/agents/"+url.PathEscape(n), &a); err != nil {
			return nil, err
		}
		out = append(out, Agent{Name: n, Role: a.Spec.Role, ReportsTo: a.Spec.ReportsTo, Provider: a.Spec.Runtime.Provider, Model: a.Spec.Runtime.Model})
	}
	c.agents = out
	return out, nil
}

// Overview reads the dashboard data. A failing optional part is recorded in Errors; only health and status
// fail the whole read (Core unreachable).
func (c *HTTPClient) Overview(ctx context.Context) (Overview, error) {
	o := Overview{At: c.now(), Errors: map[string]string{}}
	if err := c.get(ctx, "/health", &o.Health); err != nil {
		return o, err
	}
	if err := c.get(ctx, "/status", &o.Status); err != nil {
		return o, err
	}
	part := func(name string, err error) {
		if err != nil {
			o.Errors[name] = err.Error()
		}
	}
	var err error
	o.Agents, err = c.Agents(ctx)
	part("agents", err)
	part("tasks", c.get(ctx, "/tasks", &o.Tasks))
	part("pauses", c.get(ctx, "/pauses", &o.Pauses))
	part("pending", c.get(ctx, "/pending/count", &o.Pending))
	part("nodes", c.get(ctx, "/nodes", &o.Nodes))
	part("inbox", c.get(ctx, "/inbox", &o.Inbox))
	part("approvals", c.get(ctx, "/approvals", &o.Approvals))
	midnight := startOfDay(o.At)
	part("costs", c.get(ctx, fmt.Sprintf("/costs?by=agent&since=%d", midnight.Unix()), &o.Costs))
	part("task_costs", c.get(ctx, "/costs?by=task", &o.TaskCosts))
	part("recent_costs", c.get(ctx, fmt.Sprintf("/costs?since=%d", o.At.Add(-24*time.Hour).Unix()), &o.RecentCosts))
	o.LiveRuns = map[string]storage.Run{}
	for _, p := range o.Status {
		for _, r := range p.Runs {
			if run, err := c.Run(ctx, r.ID); err == nil {
				o.LiveRuns[r.ID] = run
			}
		}
	}
	if len(o.Errors) == 0 {
		o.Errors = nil
	}
	return o, nil
}

func startOfDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location())
}

// TaskDetail reads one task's detail; children come from all (tasks whose parent or origin it is).
func (c *HTTPClient) TaskDetail(ctx context.Context, id string, all []Task) (TaskDetail, error) {
	var d TaskDetail
	base := "/tasks/" + url.PathEscape(id)
	for _, p := range []struct {
		path string
		v    any
	}{{base, &d.Task}, {base + "/why", &d.Why}, {base + "/attempts", &d.Attempts}, {base + "/comments", &d.Comments}, {base + "/costs", &d.Costs}} {
		if err := c.get(ctx, p.path, p.v); err != nil {
			return d, err
		}
	}
	after := int64(0)
	for {
		page, err := c.Feed(ctx, FeedQuery{Task: id}, after, 100)
		if err != nil {
			return d, err
		}
		d.Events = append(d.Events, page.Items...)
		if len(d.Events) > 500 {
			d.Events = d.Events[len(d.Events)-500:]
		}
		after = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	d.Children = childrenOf(id, all)
	// The exits (pull request facts) are optional: an older Core has no such route.
	var exits []storage.TaskExit
	if err := c.get(ctx, base+"/exits", &exits); err == nil {
		d.Exits = exits
	}
	return d, nil
}

func childrenOf(id string, all []Task) []Task {
	out := []Task{}
	for _, t := range all {
		if t.ID != id && (t.ParentTaskID == id || t.OriginTask == id) {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

func (c *HTTPClient) Run(ctx context.Context, id string) (storage.Run, error) {
	var r storage.Run
	err := c.get(ctx, "/runs/"+url.PathEscape(id), &r)
	return r, err
}

func (c *HTTPClient) ToolCalls(ctx context.Context, run string) ([]storage.ToolCall, error) {
	var v []storage.ToolCall
	err := c.get(ctx, "/runs/"+url.PathEscape(run)+"/tool-calls", &v)
	return v, err
}

func (c *HTTPClient) Feed(ctx context.Context, q FeedQuery, after int64, limit int) (storage.Page[storage.FeedItem], error) {
	v := q.values()
	v.Set("after", fmt.Sprint(after))
	v.Set("limit", fmt.Sprint(limit))
	var page storage.Page[storage.FeedItem]
	err := c.get(ctx, "/feed?"+v.Encode(), &page)
	return page, err
}

func (c *HTTPClient) RunEvents(ctx context.Context, run string) ([]RunEvent, error) {
	var events []protocol.Envelope
	if err := c.get(ctx, "/runs/"+url.PathEscape(run)+"/events", &events); err != nil {
		return nil, err
	}
	out := make([]RunEvent, len(events))
	for i, e := range events {
		out[i] = RunEvent{Seq: int64(i + 1), Envelope: e}
	}
	return out, nil
}

func (c *HTTPClient) Messages(ctx context.Context, conversation string) ([]messaging.Message, error) {
	var v []messaging.Message
	err := c.get(ctx, "/conversations/"+url.PathEscape(conversation)+"/messages", &v)
	if he, ok := err.(*worker.HTTPError); ok && he.Status == 404 {
		return nil, nil // no conversation yet: the first owner message creates it
	}
	return v, err
}

// maxArtifactPreview bounds an artifact download for a preview.
const maxArtifactPreview = 32 << 20

func (c *HTTPClient) ArtifactContent(ctx context.Context, project, artifact, version string) ([]byte, error) {
	b := &limitedBuffer{max: maxArtifactPreview}
	err := c.API.Fetch(ctx, "/projects/"+url.PathEscape(project)+"/artifacts/"+url.PathEscape(artifact)+"/"+url.PathEscape(version)+"/content", "", b)
	return b.buf, err
}

// limitedBuffer keeps at most max bytes and fails beyond.
type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.buf)+len(p) > b.max {
		return 0, fmt.Errorf("artifact larger than %d MiB: download it with factory artifacts", b.max>>20)
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

// Do sends an owner action; the result is a short confirmation.
func (c *HTTPClient) Do(ctx context.Context, a Action) (string, error) {
	task := "/tasks/" + url.PathEscape(a.TaskID)
	rid := protocol.ID()
	var result map[string]any
	var err error
	switch a.Kind {
	case "pause", "resume":
		err = c.API.Do(ctx, "POST", task+"/"+a.Kind, map[string]any{"version": a.Version, "reason": a.Text, "request_id": rid}, &result)
	case "reassign":
		err = c.API.Do(ctx, "POST", task+"/reassign", map[string]any{"version": a.Version, "assignee": a.Assignee, "reason": a.Text, "request_id": rid}, &result)
	case "reprioritize":
		err = c.API.Do(ctx, "POST", task+"/reprioritize", map[string]any{"version": a.Version, "priority": a.Priority, "request_id": rid}, &result)
	case "retry", "run":
		// run and retry release the task at its version (an owner ready), as factory run|retry do.
		err = c.API.Do(ctx, "POST", task+"/actions", map[string]any{"version": a.Version, "action": "ready", "reason": a.Kind + " by owner", "request_id": rid}, &result)
	case "cancel":
		body := map[string]any{"version": a.Version, "action": "cancel", "request_id": rid}
		if a.Text != "" {
			body["reason"] = a.Text
		}
		if a.Cascade {
			body["cascade"] = true
		}
		err = c.API.Do(ctx, "POST", task+"/actions", body, &result)
	case "comment":
		err = c.API.Do(ctx, "POST", task+"/comments", map[string]any{"text": a.Text, "request_id": rid}, &result)
	case "direct":
		err = c.API.Do(ctx, "POST", task+"/directives", map[string]any{"text": a.Text, "urgent": a.Urgent, "request_id": rid}, &result)
	case "answer":
		body := map[string]any{"version": a.Version, "body": a.Text, "request_id": rid}
		if len(a.Answers) > 0 {
			body["answers"] = a.Answers
		}
		err = c.API.Do(ctx, "POST", "/inbox/"+url.PathEscape(a.MessageID)+"/answer", body, &result)
	case "approve", "reject":
		err = c.API.Do(ctx, "POST", "/approvals/"+url.PathEscape(a.ApprovalID)+"/decision", map[string]any{"version": a.Version, "action": a.Kind, "reason": a.Text, "request_id": rid}, &result)
	case "say":
		// POST /messages: to owner.default_report, or the direct report a leading @name addresses.
		err = c.API.Do(ctx, "POST", "/messages", map[string]any{"project_id": a.Project, "body": a.Text, "request_id": rid}, &result)
	case "create":
		deps := a.Deps
		if deps == nil {
			deps = []string{}
		}
		body := map[string]any{"project_id": a.Project, "title": a.Title, "description": a.Text, "priority": a.Priority, "assignee": a.Assignee, "dependencies": deps, "request_id": rid}
		if a.Ready {
			body["ready"] = true // created released (READY, or waiting for its dependencies)
		}
		if err = c.API.Do(ctx, "POST", "/tasks", body, &result); err == nil {
			if id, _ := result["id"].(string); id != "" {
				return "created " + id + ": " + a.Title, nil
			}
		}
	case "stop":
		err = c.API.Do(ctx, "POST", "/stop", map[string]string{"reason": a.Text}, &result)
	case "start":
		err = c.API.Do(ctx, "POST", "/pauses/resume", map[string]string{"scope": "company"}, &result)
	default:
		return "", fmt.Errorf("unknown action %s", a.Kind)
	}
	if err != nil {
		return "", err
	}
	return "done: " + a.Describe(), nil
}

// StreamFeed follows GET /feed/stream.
func (c *HTTPClient) StreamFeed(ctx context.Context, q FeedQuery, after int64, out func(StreamEvent)) {
	path := "/feed/stream"
	if v := q.values().Encode(); v != "" {
		path += "?" + v
	}
	c.follow(ctx, "feed", path, after, out)
}

// StreamRun follows GET /runs/{id}/events/stream until the run ended; tail > 0 starts at its last events.
func (c *HTTPClient) StreamRun(ctx context.Context, run string, after int64, tail int, out func(StreamEvent)) {
	path := "/runs/" + url.PathEscape(run) + "/events/stream"
	if after == 0 && tail > 0 {
		path += fmt.Sprintf("?tail=%d", tail)
	}
	c.follow(ctx, "run:"+run, path, after, out)
}

// follow reads an event stream, reconnecting with Last-Event-ID after a failure (backoff doubling to 15 s).
func (c *HTTPClient) follow(ctx context.Context, name, path string, after int64, out func(StreamEvent)) {
	client := c.Stream
	if client == nil {
		client = &http.Client{}
	}
	backoff := c.Backoff
	if backoff <= 0 {
		backoff = time.Second
	}
	delay := backoff
	connected := false
	state := func(ok bool, err error) {
		e := StreamEvent{Stream: name, State: true, Connected: ok}
		if err != nil {
			e.Err = err.Error()
		}
		connected = ok
		out(e)
	}
	for ctx.Err() == nil {
		ended := false
		err := readSSE(ctx, client, strings.TrimRight(c.API.URL, "/")+path, c.API.Token, after, func() {
			delay = backoff
			if !connected {
				state(true, nil)
			}
		}, func(ev sseMessage) {
			switch ev.Event {
			case "feed":
				item, err := decodeFeed(ev.Data)
				if err == nil {
					out(StreamEvent{Stream: name, Feed: &item})
				}
			case "run":
				e, err := decodeEnvelope(ev.Data)
				if err == nil {
					out(StreamEvent{Stream: name, Run: &RunEvent{Seq: ev.ID, Envelope: e}})
				}
			case "end":
				ended = true
				out(StreamEvent{Stream: name, End: true, Outcome: endOutcome(ev.Data)})
			}
			if ev.ID > 0 {
				after = ev.ID
			}
		})
		if ended || ctx.Err() != nil {
			return
		}
		if err == nil {
			err = fmt.Errorf("stream closed")
		}
		if he, ok := err.(*worker.HTTPError); ok && (he.Status == 404 || he.Status == 401 || he.Status == 400) {
			state(false, err)
			return
		}
		state(false, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay *= 2
		if delay > 15*time.Second {
			delay = 15 * time.Second
		}
	}
}
