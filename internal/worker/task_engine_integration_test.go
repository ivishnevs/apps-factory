package worker

import (
	"appfactory/internal/bus"
	"appfactory/internal/config"
	"appfactory/internal/core"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestJetStreamTaskEngineRestart(t *testing.T) {
	url := os.Getenv("NATS_TEST_URL")
	if url == "" {
		t.Skip("set NATS_TEST_URL to dedicated JetStream")
	}
	t.Setenv("FACTORY_FAKE_SERVER", "1")
	b, err := bus.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.NC.Close()
	defer b.JS.DeleteStream("FACTORY_COMMANDS")
	defer b.JS.DeleteStream("FACTORY_EVENTS")
	path := filepath.Join(t.TempDir(), "db")
	s, err := storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.DB.Close() }()
	agents, err := config.Load("../..")
	if err != nil {
		t.Fatal(err)
	}
	c := &core.Core{Store: s, Bus: b, Agents: agents, Token: "test"}
	server := httptest.NewServer(c.Handler())
	defer server.Close()
	api := API{URL: server.URL, Token: "test"}
	root, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	engineCtx, stopEngine := context.WithCancel(root)
	defer stopEngine()
	engineDone := make(chan error, 1)
	go func() { engineDone <- c.Run(engineCtx) }()
	workerCtx, stopWorker := context.WithCancel(root)
	defer stopWorker()
	bin, _ := os.Executable()
	w := Worker{Bus: b, API: api, Binary: bin, Args: []string{"-test.run=^TestFakeAppServer$"}, ShimBinary: buildShim(t)}
	workerDone := make(chan error, 1)
	go func() { workerDone <- w.Run(workerCtx) }()
	var project tasks.Project
	if err = api.Do(root, "POST", "/projects", tasks.ProjectCreate{ID: "engine", Name: "Engine", Workspace: t.TempDir()}, &project); err != nil {
		t.Fatal(err)
	}
	var a, second tasks.Task
	create := tasks.Create{ProjectID: project.ID, Title: "first", Assignee: "demo", RequestID: "first"}
	if err = api.Do(root, "POST", "/tasks", create, &a); err != nil {
		t.Fatal(err)
	}
	create.Title = "second"
	create.RequestID = "second"
	create.Dependencies = []string{a.ID}
	if err = api.Do(root, "POST", "/tasks", create, &second); err != nil {
		t.Fatal(err)
	}
	for _, v := range []tasks.Task{a, second} {
		var ready tasks.Task
		if err = api.Do(root, "POST", "/tasks/"+v.ID+"/actions", tasks.Action{Version: v.Version, Action: "ready"}, &ready); err != nil {
			t.Fatal(err)
		}
	}
	wait := func(id, status string) tasks.Task {
		t.Helper()
		for root.Err() == nil {
			var v tasks.Task
			if err := api.Do(root, "GET", "/tasks/"+id, nil, &v); err != nil {
				t.Fatal(err)
			}
			if v.Status == status {
				return v
			}
			if v.Status == "BLOCKED" || v.Status == "FAILED" {
				t.Fatalf("unexpected task %+v", v)
			}
			select {
			case <-root.Done():
			case <-time.After(50 * time.Millisecond):
			}
		}
		t.Fatal("task timeout")
		return tasks.Task{}
	}
	a = wait(a.ID, "IN_REVIEW")
	if err = api.Do(root, "GET", "/tasks/"+second.ID, nil, &second); err != nil || second.Status != "READY" || second.Attempt != 0 {
		t.Fatal("dependency review bypass", second, err)
	}
	// Restart the Core loop and close/reopen SQLite before accepting the prerequisite.
	// Stop the idle Worker too so no HTTP request can race replacement of Store.
	stopWorker()
	if err = <-workerDone; err != nil {
		t.Fatal(err)
	}
	stopEngine()
	if err = <-engineDone; err != nil {
		t.Fatal(err)
	}
	server.Close()
	s.DB.Close()
	s, err = storage.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	c = &core.Core{Store: s, Bus: b, Agents: agents, Token: "test"}
	server = httptest.NewServer(c.Handler())
	defer server.Close()
	api.URL = server.URL
	w.API = api
	engineCtx, stopEngine = context.WithCancel(root)
	defer stopEngine()
	go func() { engineDone <- c.Run(engineCtx) }()
	workerCtx, stopWorker = context.WithCancel(root)
	defer stopWorker()
	go func() { workerDone <- w.Run(workerCtx) }()
	a = wait(a.ID, "IN_REVIEW")
	if err = api.Do(root, "POST", "/tasks/"+a.ID+"/review", tasks.Review{Version: a.Version, Accept: true, RequestID: "review-first"}, &a); err != nil {
		t.Fatal(err)
	}
	second = wait(second.ID, "IN_REVIEW")
	if second.Attempt != 1 || second.Result != "foundation ok" {
		t.Fatal(second)
	}
	var attempts []storage.Run
	if err = api.Do(root, "GET", "/tasks/"+second.ID+"/attempts", nil, &attempts); err != nil || len(attempts) != 1 {
		t.Fatal("duplicate dispatch after restart", attempts, err)
	}
	stopWorker()
	if err = <-workerDone; err != nil {
		t.Fatal(err)
	}
	stopEngine()
	if err = <-engineDone; err != nil {
		t.Fatal(err)
	}
}
