package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// TestReviewGate (review-coverage 3.1, 3.4): a task leaves the backlog only with a reviewer; the refusal names the
// field and the policy's suggestion; clearing it outside the backlog is refused; a submit goes to the task's
// reviewer; the fill gives open tasks the suggestion.
func TestReviewGate(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.DB.Close() }()
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	reviewer := catalog.Agents["demo"]
	reviewer.Metadata.Name = "reviewer"
	catalog.Agents["reviewer"] = reviewer
	c := &Core{Store: s, Agents: catalog.Agents, Skills: catalog.Skills, Token: "owner-secret"}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer owner-secret")
		w := httptest.NewRecorder()
		c.Handler().ServeHTTP(w, r)
		return w
	}
	if _, err = s.CreateProject(ctx, storage.DefaultCompany, tasks.ProjectCreate{ID: "p", Name: "p", Workspace: "/tmp/review-gate"}); err != nil {
		t.Fatal(err)
	}

	w := call("POST", "/tasks", tasks.Create{ProjectID: "p", Title: "ready", Assignee: "demo", Ready: true, RequestID: "a"})
	if w.Code != 400 || !strings.HasPrefix(w.Body.String(), ErrReviewerRequired) || !strings.Contains(w.Body.String(), `suggests review_by`) {
		t.Fatal("create ready without a reviewer", w.Code, w.Body.String())
	}
	if w = call("POST", "/tasks", tasks.Create{ProjectID: "p", Title: "bad", Assignee: "demo", ReviewBy: "nobody", RequestID: "b"}); w.Code != 400 {
		t.Fatal("unknown reviewer", w.Code, w.Body.String())
	}
	if w = call("POST", "/tasks", tasks.Create{ProjectID: "p", Title: "ok", Assignee: "demo", ReviewBy: "reviewer", Ready: true, RequestID: "c"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var ready tasks.Task
	_ = json.Unmarshal(w.Body.Bytes(), &ready)
	if ready.ReviewBy != "reviewer" || ready.Status != "READY" {
		t.Fatal(ready)
	}
	empty := ""
	if w = call("PATCH", "/tasks/"+ready.ID, tasks.Update{Version: ready.Version, ReviewBy: &empty}); w.Code != 400 {
		t.Fatal("clearing the reviewer outside the backlog", w.Code, w.Body.String())
	}

	// A backlog task: created without a reviewer, refused on ready, released after one is set.
	w = call("POST", "/tasks", tasks.Create{ProjectID: "p", Title: "later", Assignee: "demo", RequestID: "d"})
	var later tasks.Task
	_ = json.Unmarshal(w.Body.Bytes(), &later)
	if w = call("POST", "/tasks/"+later.ID+"/actions", tasks.Action{Version: later.Version, Action: "ready"}); w.Code != 400 {
		t.Fatal("ready without a reviewer", w.Code, w.Body.String())
	}
	owner := "owner"
	if w = call("PATCH", "/tasks/"+later.ID, tasks.Update{Version: later.Version, ReviewBy: &owner}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &later)
	if w = call("POST", "/tasks/"+later.ID+"/actions", tasks.Action{Version: later.Version, Action: "ready"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}

	// The submit's reviewer: the task's agent; a person keeps the policy's default out.
	if name, policy := c.reviewerOfSubmit(storage.DefaultCompany, ready, "demo"); name != "reviewer" || !policy {
		t.Fatal(name, policy)
	}
	if name, policy := c.reviewerOfSubmit(storage.DefaultCompany, later, "demo"); name != "" || policy {
		t.Fatal(name, policy)
	}

	// Fill: an open task without a reviewer (made ready in storage, as before the gate) gets the suggestion.
	old, err := s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: "p", Title: "old", Assignee: "demo", Ready: true})
	if err != nil {
		t.Fatal(err)
	}
	if w = call("POST", "/tasks/review-fill", map[string]any{"dry_run": true}); w.Code != 200 || !strings.Contains(w.Body.String(), old.ID) {
		t.Fatal(w.Code, w.Body.String())
	}
	if got, _ := s.Task(ctx, storage.DefaultCompany, old.ID); got.ReviewBy != "" {
		t.Fatal("dry run changed the task", got.ReviewBy)
	}
	if w = call("POST", "/tasks/review-fill", map[string]any{}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if got, _ := s.Task(ctx, storage.DefaultCompany, old.ID); got.ReviewBy != c.suggestReviewer(storage.DefaultCompany, "demo") || got.ReviewBy == "" {
		t.Fatal("fill", got.ReviewBy)
	}
}

func TestReviewCoverageWarnings(t *testing.T) {
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	warnings := config.ReviewCoverage(catalog)
	if len(warnings) == 0 {
		t.Skip("the repository catalog covers every agent")
	}
	for _, w := range warnings {
		if !strings.Contains(w, "has no review rule") || !strings.Contains(w, "default to review by") {
			t.Fatal(w)
		}
	}
}
