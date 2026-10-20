package worker

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/driver/fake"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"

	"github.com/nats-io/nats.go"
)

// TestJetStreamStaleCommandIsDropped (trigger-log-scheduling D6): a v1 worker claims a run whose task no
// longer wants it (a dependency reopened after dispatch); Core answers 409 drop, the worker acknowledges the
// command without starting the runtime, and the run ends stale.
func TestJetStreamStaleCommandIsDropped(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to a dedicated JetStream instance")
	}
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	s, err := storage.Open(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.DB.Close()
	c := &core.Core{Store: s, Bus: b, Token: "test-token", Agents: map[string]config.Agent{"demo": {}}}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	co := storage.DefaultCompany
	if _, err = s.CreateProject(ctx, co, tasks.ProjectCreate{ID: "p", Name: "P", Workspace: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTask(ctx, co, tasks.Create{ID: "dep", ProjectID: "p", Title: "dep", Assignee: "demo"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(`UPDATE tasks SET status='DONE',body=json_set(body,'$.status','DONE') WHERE id='dep'`); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, co, tasks.Create{ID: "work", ProjectID: "p", Title: "work", Assignee: "demo", Dependencies: []string{"dep"}, Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Dispatch(ctx, co, task.ID, task.Version, protocol.RunSpec{ID: "stale-run", Project: "p", Agent: "demo", Provider: protocol.RuntimeCodex}); err != nil {
		t.Fatal(err)
	}
	// The dependency reopens after dispatch.
	if _, err = s.DB.Exec(`UPDATE tasks SET status='READY',body=json_set(body,'$.status','READY') WHERE id='dep'`); err != nil {
		t.Fatal(err)
	}
	out, err := s.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range out {
		if err = b.Publish(ctx, o.Subject, o.ID, o.Body); err != nil {
			t.Fatal(err)
		}
		if err = s.Sent(ctx, o.ID); err != nil {
			t.Fatal(err)
		}
	}
	d := &fake.Driver{}
	w := Worker{Bus: b, API: API{URL: server.URL, Token: "test-token"}, Driver: d}
	sub, err := w.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err = w.Process(ctx, msgs[0], nil); err != nil {
		t.Fatal(err)
	}
	if len(d.Launches()) != 0 {
		t.Fatal("the runtime started for a stale command")
	}
	r, err := s.Get(ctx, co, "stale-run")
	if err != nil || r.Status != "CANCELLED" || r.Outcome != "stale" || r.StartedAt != 0 {
		t.Fatalf("%+v %v", r, err)
	}
	// Acknowledged, not redelivered.
	if _, err = sub.Fetch(1, nats.MaxWait(2*time.Second)); !errors.Is(err, nats.ErrTimeout) {
		t.Fatal("the dropped command came back:", err)
	}
	if ci, err := sub.ConsumerInfo(); err != nil || ci.NumAckPending != 0 || ci.NumPending != 0 {
		t.Fatalf("%+v %v", ci, err)
	}
}
