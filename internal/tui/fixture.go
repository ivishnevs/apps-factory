package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
)

// Fixture is a recorded Core state the UI renders without a running Core (factory ui --fixture, --demo).
type Fixture struct {
	Name      string                        `json:"name,omitempty"`
	Overview  Overview                      `json:"overview"`
	Details   map[string]TaskDetail         `json:"details,omitempty"`
	Feed      []storage.FeedItem            `json:"feed"`
	Runs      map[string]storage.Run        `json:"runs,omitempty"`
	RunEvents map[string][]RunEvent         `json:"run_events,omitempty"`
	ToolCalls map[string][]storage.ToolCall `json:"tool_calls,omitempty"`
	// Conversations are the owner conversations' messages by conversation ID (owner-<agent>-<project>).
	Conversations map[string][]messaging.Message `json:"conversations,omitempty"`
	// Artifacts are uploaded artifact contents by "project/artifact/version".
	Artifacts map[string]string `json:"artifacts,omitempty"`
}

// ownerConversation is the owner's conversation with a direct report in a project (POST /messages).
func ownerConversation(agent, project string) string { return "owner-" + agent + "-" + project }

// directReports are the owner's direct reports, sorted (the default report first).
func directReports(agents []Agent, defaultReport string) []string {
	out := []string{}
	for _, a := range agents {
		if a.ReportsTo == "owner" {
			out = append(out, a.Name)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i] == defaultReport) != (out[j] == defaultReport) {
			return out[i] == defaultReport
		}
		return out[i] < out[j]
	})
	return out
}

// defaultReportOf is owner.default_report as Core reports it, or the only direct report.
func defaultReportOf(ov Overview) string {
	if ov.Health.DefaultReport != "" {
		return ov.Health.DefaultReport
	}
	if r := directReports(ov.Agents, ""); len(r) == 1 {
		return r[0]
	}
	return ""
}

// addressee is the direct report an owner message goes to: a leading @name, else the default report.
func addressee(body string, reports []string, def string) (string, bool) {
	if strings.HasPrefix(body, "@") {
		name, _, _ := strings.Cut(strings.TrimPrefix(body, "@"), " ")
		name = strings.TrimRight(name, ",:")
		for _, r := range reports {
			if r == name {
				return name, true
			}
		}
		return "", false
	}
	return def, def != ""
}

// LoadFixture reads a fixture file, or fixture.json in a directory.
func LoadFixture(path string) (Fixture, error) {
	var f Fixture
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		path = filepath.Join(path, "fixture.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err = json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("fixture %s: %w", path, err)
	}
	if f.Name == "" {
		f.Name = filepath.Base(path)
	}
	return f, nil
}

// FixtureClient serves a Fixture. Actions are not sent anywhere: they are recorded in Sent. Run streams
// replay the recorded events Delay apart, then end like the recorded run.
type FixtureClient struct {
	F     Fixture
	Delay time.Duration

	mu   sync.Mutex
	Sent []Action
}

func (c *FixtureClient) Describe() string { return "fixture " + c.F.Name }

func (c *FixtureClient) Overview(context.Context) (Overview, error) { return c.F.Overview, nil }

func (c *FixtureClient) TaskDetail(_ context.Context, id string, all []Task) (TaskDetail, error) {
	if d, ok := c.F.Details[id]; ok {
		if d.Children == nil {
			d.Children = childrenOf(id, all)
		}
		return d, nil
	}
	for _, t := range c.F.Overview.Tasks {
		if t.ID == id {
			d := TaskDetail{Task: t, Children: childrenOf(id, all)}
			d.Why.TaskID, d.Why.Status, d.Why.Reason, d.Why.Clearer = t.ID, t.Status, t.Reason, t.Clearer
			for _, item := range c.F.Feed {
				if item.TaskID == id {
					d.Events = append(d.Events, item)
				}
			}
			return d, nil
		}
	}
	return TaskDetail{}, fmt.Errorf("task %s not in the fixture", id)
}

func (c *FixtureClient) Run(_ context.Context, id string) (storage.Run, error) {
	if r, ok := c.F.Runs[id]; ok {
		return r, nil
	}
	return storage.Run{}, fmt.Errorf("run %s not in the fixture", id)
}

func (c *FixtureClient) ToolCalls(_ context.Context, run string) ([]storage.ToolCall, error) {
	return c.F.ToolCalls[run], nil
}

func feedMatches(q FeedQuery, item storage.FeedItem) bool {
	return (q.Project == "" || item.ProjectID == q.Project) && (q.Task == "" || item.TaskID == q.Task) && (!q.Attention || item.Attention)
}

func (c *FixtureClient) Feed(_ context.Context, q FeedQuery, after int64, limit int) (storage.Page[storage.FeedItem], error) {
	page := storage.Page[storage.FeedItem]{Items: []storage.FeedItem{}, NextAfter: after}
	for _, item := range c.F.Feed {
		if item.Sequence <= after || !feedMatches(q, item) {
			continue
		}
		if len(page.Items) == limit {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, item)
		page.NextAfter = item.Sequence
	}
	return page, nil
}

func (c *FixtureClient) StreamFeed(ctx context.Context, q FeedQuery, after int64, out func(StreamEvent)) {
	out(StreamEvent{Stream: "feed", State: true, Connected: true})
	for _, item := range c.F.Feed {
		if item.Sequence > after && feedMatches(q, item) {
			item := item
			out(StreamEvent{Stream: "feed", Feed: &item})
		}
	}
	<-ctx.Done()
}

func (c *FixtureClient) StreamRun(ctx context.Context, run string, after int64, tail int, out func(StreamEvent)) {
	name := "run:" + run
	out(StreamEvent{Stream: name, State: true, Connected: true})
	events := c.F.RunEvents[run]
	if after == 0 && tail > 0 && len(events) > tail {
		events = events[len(events)-tail:]
	}
	for _, e := range events {
		if e.Seq <= after {
			continue
		}
		if c.Delay > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(c.Delay):
			}
		}
		e := e
		out(StreamEvent{Stream: name, Run: &e})
	}
	if r, ok := c.F.Runs[run]; ok && r.Status == "ENDED" {
		out(StreamEvent{Stream: name, End: true, Outcome: r.Outcome})
		return
	}
	<-ctx.Done()
}

func (c *FixtureClient) Do(_ context.Context, a Action) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Sent = append(c.Sent, a)
	if a.Kind == "say" {
		// The demo keeps the owner's message in its conversation, as Core would.
		def := defaultReportOf(c.F.Overview)
		to, ok := addressee(a.Text, directReports(c.F.Overview.Agents, def), def)
		if !ok {
			return "", fmt.Errorf("address a direct report with @name, or set owner.default_report")
		}
		if c.F.Conversations == nil {
			c.F.Conversations = map[string][]messaging.Message{}
		}
		id := ownerConversation(to, a.Project)
		n := len(c.F.Conversations[id]) + 1
		// After the conversation's newest message: the fixture's clock stands still.
		at := c.F.Overview.At
		for _, x := range c.F.Conversations[id] {
			if t := time.Unix(x.CreatedAt, 0).Add(time.Minute); t.After(at) {
				at = t
			}
		}
		c.F.Conversations[id] = append(c.F.Conversations[id], messaging.Message{ID: fmt.Sprintf("m-%s-%d", id, n), ProjectID: a.Project, Sender: "owner", Recipient: to,
			ConversationID: id, MessageType: "question", Body: a.Text, Status: "PENDING", Version: 1, CreatedAt: at.Unix(), DeliveryStatus: "TRIGGERED"})
	}
	return "fixture: not sent — " + a.Describe(), nil
}

func (c *FixtureClient) RunEvents(_ context.Context, run string) ([]RunEvent, error) {
	if _, ok := c.F.Runs[run]; !ok {
		return nil, fmt.Errorf("run %s not in the fixture", run)
	}
	return c.F.RunEvents[run], nil
}

func (c *FixtureClient) Messages(_ context.Context, conversation string) ([]messaging.Message, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]messaging.Message(nil), c.F.Conversations[conversation]...), nil
}

func (c *FixtureClient) ArtifactContent(_ context.Context, project, artifact, version string) ([]byte, error) {
	if b, ok := c.F.Artifacts[project+"/"+artifact+"/"+version]; ok {
		return []byte(b), nil
	}
	return nil, fmt.Errorf("artifact %s %s has no content in the fixture", artifact, version)
}

// Record reads the current Core state into a fixture: the overview, the last feed rows, the detail of every
// open task (and up to 50 closed ones), and the events and tool calls of the live runs and the latest
// attempts. Run leases are left out.
func Record(ctx context.Context, c *HTTPClient) (Fixture, error) {
	f := Fixture{Name: "recorded " + c.now().UTC().Format(time.RFC3339), Details: map[string]TaskDetail{}, Runs: map[string]storage.Run{}, RunEvents: map[string][]RunEvent{}, ToolCalls: map[string][]storage.ToolCall{}}
	var err error
	if f.Overview, err = c.Overview(ctx); err != nil {
		return f, err
	}
	after := int64(0)
	for {
		page, err := c.Feed(ctx, FeedQuery{}, after, 100)
		if err != nil {
			return f, err
		}
		f.Feed = append(f.Feed, page.Items...)
		if len(f.Feed) > 2000 {
			f.Feed = f.Feed[len(f.Feed)-2000:]
		}
		after = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	runs := map[string]bool{}
	for _, p := range f.Overview.Status {
		for _, r := range p.Runs {
			runs[r.ID] = true
		}
	}
	closed := 0
	for _, t := range f.Overview.Tasks {
		terminal := t.Status == "DONE" || t.Status == "CANCELLED"
		if terminal {
			if closed >= 50 {
				continue
			}
			closed++
		}
		d, err := c.TaskDetail(ctx, t.ID, f.Overview.Tasks)
		if err != nil {
			return f, err
		}
		f.Details[t.ID] = d
		if n := len(d.Attempts); n > 0 && len(runs) < 40 {
			runs[d.Attempts[n-1].Spec.ID] = true
		}
	}
	ids := make([]string, 0, len(runs))
	for id := range runs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		r, err := c.Run(ctx, id)
		if err != nil {
			return f, err
		}
		f.Runs[id] = r
		var events []protocol.Envelope
		if err = c.get(ctx, "/runs/"+id+"/events", &events); err != nil {
			return f, err
		}
		for i, e := range events {
			e.Payload = withoutLease(e.Payload)
			f.RunEvents[id] = append(f.RunEvents[id], RunEvent{Seq: int64(i + 1), Envelope: e})
		}
		if f.ToolCalls[id], err = c.ToolCalls(ctx, id); err != nil {
			return f, err
		}
	}
	// The owner's conversations with each direct report in each project (the chat).
	f.Conversations = map[string][]messaging.Message{}
	for _, p := range f.Overview.Status {
		for _, a := range directReports(f.Overview.Agents, "") {
			id := ownerConversation(a, p.Project.ID)
			msgs, err := c.Messages(ctx, id)
			if err != nil {
				return f, err
			}
			if len(msgs) > 0 {
				f.Conversations[id] = msgs
			}
		}
	}
	return f, nil
}

// withoutLease drops a run event's lease (a worker credential) from its payload.
func withoutLease(payload json.RawMessage) json.RawMessage {
	var p map[string]json.RawMessage
	if json.Unmarshal(payload, &p) != nil {
		return payload
	}
	if _, ok := p["lease"]; !ok {
		return payload
	}
	delete(p, "lease")
	b, err := json.Marshal(p)
	if err != nil {
		return payload
	}
	return b
}

// SaveFixture writes a fixture as indented JSON (0600: it holds the company's work).
func SaveFixture(path string, f Fixture) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if !strings.HasSuffix(path, ".json") {
		if err = os.MkdirAll(path, 0o700); err != nil {
			return err
		}
		path = filepath.Join(path, "fixture.json")
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
