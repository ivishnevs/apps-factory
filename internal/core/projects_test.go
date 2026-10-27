package core

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/factorytools"
	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

type projectHarness struct {
	t *testing.T
	s *storage.Store
	c *Core
}

func newProjectHarness(t *testing.T) projectHarness {
	t.Helper()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	catalog, err := config.LoadCatalog("../..")
	if err != nil {
		t.Fatal(err)
	}
	return projectHarness{t, s, &Core{Store: s, Agents: catalog.Agents, Token: "owner"}}
}

func (h projectHarness) http(auth, method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+auth)
	w := httptest.NewRecorder()
	h.c.Handler().ServeHTTP(w, r)
	return w
}

// create posts a project as the owner and decodes the answer.
func (h projectHarness) create(body map[string]any) (tasks.Project, *httptest.ResponseRecorder) {
	w := h.http("owner", "POST", "/projects", body)
	var p tasks.Project
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	return p, w
}

// readyTask creates a ready task of project for agent.
func (h projectHarness) readyTask(project, agent, title string) tasks.Task {
	ctx := context.Background()
	t, err := h.s.CreateTask(ctx, storage.DefaultCompany, tasks.Create{ProjectID: project, Title: title, Assignee: agent, Ready: true})
	if err != nil {
		h.t.Fatal(err)
	}
	return t
}

func (h projectHarness) task(id string) tasks.Task {
	t, err := h.s.Task(context.Background(), storage.DefaultCompany, id)
	if err != nil {
		h.t.Fatal(err)
	}
	return t
}

func (h projectHarness) feed(project string) []storage.FeedItem {
	page, err := h.s.ProjectFeed(context.Background(), storage.DefaultCompany, project, 0, 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return page.Items
}

func feedHas(items []storage.FeedItem, kind, text string) bool {
	for _, it := range items {
		if it.Type == kind && strings.Contains(it.Summary, text) {
			return true
		}
	}
	return false
}

// TestProjectCreateContract (project-creation 1.1, 1.2): workspace is optional; without workspace and repo_url
// the project is a local repository set up by the node; refusals are 4xx with the reason; a repeated request
// answers the same project; an existing workspace without repo_mode keeps today's behaviour; the feed names
// who created the project and its repository mode.
func TestProjectCreateContract(t *testing.T) {
	h := newProjectHarness(t)
	p, w := h.create(map[string]any{"id": "advent-gift", "name": "Advent gift", "description": "A gift", "request_id": "r1"})
	if w.Code != 200 || p.RepoMode != "local" || p.Workspace != "" || p.Setup == nil || p.Setup.State != "pending" || p.CreatedBy != "owner" {
		t.Fatal(w.Code, w.Body.String())
	}
	if again, w := h.create(map[string]any{"id": "advent-gift", "name": "Advent gift", "description": "A gift", "request_id": "r1"}); w.Code != 200 || again.CreatedAt != p.CreatedAt {
		t.Fatal("replay", w.Code, w.Body.String())
	}
	for _, c := range []struct {
		body map[string]any
		code int
		want string
	}{
		{map[string]any{"id": "advent-gift", "name": "x", "request_id": "r2"}, 409, "already exists"},
		{map[string]any{"id": "bad id", "name": "x"}, 400, "project id"},
		{map[string]any{"id": "x1", "name": " "}, 400, "name required"},
		{map[string]any{"id": "x2", "name": "x", "repo_mode": "svn"}, 400, "repo_mode must be"},
		{map[string]any{"id": "x3", "name": "x", "repo_mode": "local", "workspace": "/tmp/x3"}, 400, "creates the repository"},
		{map[string]any{"id": "x4", "name": "x", "repo_mode": "existing"}, 400, "needs workspace"},
		{map[string]any{"id": "x5", "name": "x", "workspace": "relative/path"}, 400, "absolute path"},
		{map[string]any{"id": "x6", "name": "x", "repo_mode": "github", "audit": true}, 400, "audit is for an existing repository"},
		{map[string]any{"id": "x7", "name": "x", "repo_mode": "existing", "workspace": "/tmp/x7", "audit": true}, 400, "product-manager"},
	} {
		if _, w := h.create(c.body); w.Code != c.code || !strings.Contains(w.Body.String(), c.want) {
			t.Fatal(c.body, w.Code, w.Body.String())
		}
	}
	legacy, w := h.create(map[string]any{"id": "app-ideas", "name": "Ideas", "workspace": "/Users/owner/projects/app-ideas"})
	if w.Code != 200 || legacy.Setup != nil || legacy.RepoMode != "" || legacy.Workspace != "/Users/owner/projects/app-ideas" {
		t.Fatal(w.Body.String())
	}
	if _, w = h.create(map[string]any{"id": "app-ideas-2", "name": "Ideas", "workspace": "/Users/owner/projects/app-ideas"}); w.Code != 409 || !strings.Contains(w.Body.String(), "belongs to project app-ideas") {
		t.Fatal(w.Code, w.Body.String())
	}
	// Two projects waiting for their repository may both have no workspace yet.
	if _, w = h.create(map[string]any{"id": "second", "name": "Second", "repo_mode": "github"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if !feedHas(h.feed("advent-gift"), "project.created", "created by owner; repository: new local git repository") {
		t.Fatal(h.feed("advent-gift"))
	}
	if !feedHas(h.feed("second"), "project.created", "private GitHub repository") {
		t.Fatal(h.feed("second"))
	}
}

// TestProjectSetupGate (project-creation 1.1): the first task run of a project without a repository carries
// the setup and no workspace; other tasks of the project wait (project_setup) until the node reports it; the
// report sets the workspace, the run's spec and the feed; a failure is recorded with its reason. A project
// with an existing workspace (no repo_mode) dispatches as before.
func TestProjectSetupGate(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	if _, w := h.create(map[string]any{"id": "fresh", "name": "Fresh"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	first := h.readyTask("fresh", "manager", "first")
	second := h.readyTask("fresh", "demo", "second")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	first, second = h.task(first.ID), h.task(second.ID)
	setupTask, waiting := first, second
	if first.CurrentRunID == "" {
		setupTask, waiting = second, first
	}
	if setupTask.CurrentRunID == "" || waiting.CurrentRunID != "" {
		t.Fatal("exactly one run sets the project up", first, second)
	}
	run, _ := h.s.Get(ctx, storage.DefaultCompany, setupTask.CurrentRunID)
	if run.Spec.ProjectSetup == nil || run.Spec.ProjectSetup.Mode != "local" || run.Spec.Workspace != "" || run.Spec.ProjectSetup.OpenSpecDir != "openspec" {
		t.Fatalf("%+v", run.Spec)
	}
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	if h.task(waiting.ID).CurrentRunID != "" {
		t.Fatal("a second run started during the setup")
	}
	snap, _ := h.s.TaskSnapshot(ctx, storage.DefaultCompany, waiting.ID)
	if v := h.c.evaluate(snap); v.Reason != tasks.ReasonCapacity && v.Reason != tasks.ReasonProjectSetup {
		t.Fatal(v)
	}
	lease, err := h.s.Claim(ctx, storage.DefaultCompany, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	report := protocol.ProjectSetupReport{Lease: lease, NodeID: "mini", Workspace: "/Users/owner/factory/projects/fresh"}
	if w := h.http("owner", "POST", "/runs/"+run.Spec.ID+"/project-setup", protocol.ProjectSetupReport{Lease: "wrong", Workspace: report.Workspace}); w.Code != 409 {
		t.Fatal("foreign lease", w.Code)
	}
	w := h.http("owner", "POST", "/runs/"+run.Spec.ID+"/project-setup", report)
	var answer protocol.ProjectSetupAnswer
	if json.Unmarshal(w.Body.Bytes(), &answer); w.Code != 200 || answer.Workspace != report.Workspace || answer.Materialize == nil || answer.Materialize.OpenSpecDir != "openspec" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = h.http("owner", "POST", "/runs/"+run.Spec.ID+"/project-setup", report); w.Code != 200 {
		t.Fatal("repeated report", w.Code, w.Body.String())
	}
	p, _ := h.s.Project(ctx, storage.DefaultCompany, "fresh")
	if p.Workspace != report.Workspace || p.Setup.State != "ready" || p.Setup.NodeID != "mini" || p.Setup.TaskID != setupTask.ID || p.Version != 2 {
		t.Fatalf("%+v %+v", p, p.Setup)
	}
	run, _ = h.s.Get(ctx, storage.DefaultCompany, run.Spec.ID)
	if run.Spec.Workspace != report.Workspace {
		t.Fatal(run.Spec)
	}
	if !feedHas(h.feed("fresh"), "project.provisioned", "ready on node mini: local git repository /Users/owner/factory/projects/fresh") {
		t.Fatal(h.feed("fresh"))
	}
	if snap, _ = h.s.TaskSnapshot(ctx, storage.DefaultCompany, waiting.ID); snap.ProjectSetup != "" {
		t.Fatal(snap.ProjectSetup)
	}

	// A failed setup: reason in the project and its feed.
	if _, w := h.create(map[string]any{"id": "hub", "name": "Hub", "repo_mode": "github"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	hub := h.readyTask("hub", "calorie-manager", "start")
	if err = h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	hub = h.task(hub.ID)
	if hub.CurrentRunID == "" {
		t.Fatal("not dispatched", hub)
	}
	lease, err = h.s.Claim(ctx, storage.DefaultCompany, hub.CurrentRunID)
	if err != nil {
		t.Fatal(err)
	}
	failed := protocol.ProjectSetupReport{Lease: lease, NodeID: "mini", Error: "gh is not authenticated on node mini (run gh auth login, then retry the task)"}
	if w = h.http("owner", "POST", "/runs/"+hub.CurrentRunID+"/project-setup", failed); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = h.s.Project(ctx, storage.DefaultCompany, "hub")
	if p.Setup.State != "failed" || !strings.Contains(p.Setup.Reason, "gh auth login") || p.Workspace != "" {
		t.Fatalf("%+v", p.Setup)
	}
	if !feedHas(h.feed("hub"), "project.setup_failed", "gh is not authenticated") {
		t.Fatal(h.feed("hub"))
	}

	// An existing workspace without repo_mode: no setup, the first run has the workspace.
	if _, w := h.create(map[string]any{"id": "app-ideas", "name": "Ideas", "workspace": "/Users/owner/projects/app-ideas"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	idea := h.readyTask("app-ideas", "manager-autonomous", "idea")
	if err = h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ = h.s.Get(ctx, storage.DefaultCompany, h.task(idea.ID).CurrentRunID)
	if run.Spec.ProjectSetup != nil || run.Spec.Workspace != "/Users/owner/projects/app-ideas" {
		t.Fatalf("%+v", run.Spec)
	}
}

// TestProjectAuditTask (project-creation 1.4): an existing repository with audit gets a ready audit task for
// the configured agent, once per request.
func TestProjectAuditTask(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	h.c.AuditAssignee = "manager"
	body := map[string]any{"id": "kickclimb", "name": "KickClimb", "repo_mode": "existing", "repo_url": "https://github.com/owner/kickclimb.git", "default_branch": "main", "audit": true, "request_id": "onboard"}
	p, w := h.create(body)
	if w.Code != 200 || p.Setup == nil || !p.Setup.Audit || p.Setup.AuditTaskID == "" || p.RepoURL != "https://github.com/owner/kickclimb.git" {
		t.Fatal(w.Code, w.Body.String())
	}
	audit := h.task(p.Setup.AuditTaskID)
	if audit.Assignee != "manager" || audit.Status != "READY" || audit.ProjectID != "kickclimb" || !strings.Contains(audit.Description, "project-audit skill") {
		t.Fatalf("%+v", audit)
	}
	if again, w := h.create(body); w.Code != 200 || again.Setup.AuditTaskID != audit.ID {
		t.Fatal(w.Code, w.Body.String())
	}
	list, _ := h.s.Tasks(ctx, storage.DefaultCompany, "kickclimb", "")
	if len(list) != 1 {
		t.Fatal(len(list))
	}
	// The audit is the project's first task: its run onboards the repository.
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ := h.s.Get(ctx, storage.DefaultCompany, h.task(audit.ID).CurrentRunID)
	if run.Spec.ProjectSetup == nil || run.Spec.ProjectSetup.Mode != "existing" || run.Spec.ProjectSetup.RepoURL != "https://github.com/owner/kickclimb.git" {
		t.Fatalf("%+v", run.Spec)
	}
}

// TestAgentProjectCreateNeedsApproval (project-creation 3.1): factory_project_create creates nothing before
// the owner approves the exact call, nothing after a rejection, exactly one project per approved request;
// the creator may then create tasks in it; an agent not allowed to create projects does not get the tool.
func TestAgentProjectCreateNeedsApproval(t *testing.T) {
	ctx := context.Background()
	h := newProjectHarness(t)
	if _, w := h.create(map[string]any{"id": "app-ideas", "name": "Ideas", "workspace": "/tmp/app-ideas"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	task := h.readyTask("app-ideas", "manager", "start the chosen idea")
	if err := h.c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	run, _ := h.s.Get(ctx, storage.DefaultCompany, h.task(task.ID).CurrentRunID)
	lease, err := h.s.Claim(ctx, storage.DefaultCompany, run.Spec.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := h.c.runToken(storage.DefaultCompany, run.Spec.ID, lease)
	call := func(c factorytools.Call) *httptest.ResponseRecorder { return h.http(token, "POST", "/agent/call", c) }
	exists := func(id string) bool {
		_, err := h.s.Project(ctx, storage.DefaultCompany, id)
		return err == nil
	}
	w := h.http(token, "GET", "/agent/tools", nil)
	if !strings.Contains(w.Body.String(), `"factory_project_create"`) {
		t.Fatal("the owner's direct report lacks the tool")
	}
	create := factorytools.Call{Name: "factory_project_create", Arguments: map[string]any{"project_id": "advent-gift", "name": "Advent gift", "description": "The idea the owner picked", "repo_mode": "local", "request_id": "start-advent"}}
	if w = call(create); w.Code != 423 || exists("advent-gift") {
		t.Fatal(w.Code, w.Body.String())
	}
	ask := func(c factorytools.Call, key string) approvals.Approval {
		w := call(factorytools.Call{Name: "factory_owner_request_approval", Arguments: map[string]any{"call": c, "reason": "Start the idea the owner picked", "blocking": false, "request_id": key}})
		var a approvals.Approval
		if json.Unmarshal(w.Body.Bytes(), &a); w.Code != 200 || a.Status != "PENDING" {
			t.Fatal(w.Code, w.Body.String())
		}
		return a
	}
	grant := ask(create, "ask-advent")
	if w = call(create); w.Code != 423 || exists("advent-gift") {
		t.Fatal("pending approval created the project", w.Code)
	}
	// A rejected call leaves nothing.
	rejected := factorytools.Call{Name: "factory_project_create", Arguments: map[string]any{"project_id": "rejected-idea", "name": "No", "request_id": "start-rejected"}}
	no := ask(rejected, "ask-rejected")
	if w = h.http("owner", "POST", "/approvals/"+no.ID+"/decision", approvals.Decision{Version: no.Version, Action: "reject", Reason: "not this one", RequestID: "d1"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(rejected); w.Code != 423 || exists("rejected-idea") {
		t.Fatal("rejected call created a project", w.Code)
	}
	if w = h.http("owner", "POST", "/approvals/"+grant.ID+"/decision", approvals.Decision{Version: grant.Version, Action: "approve", Reason: "go", RequestID: "d2"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p tasks.Project
	if w = call(create); w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &p) != nil || p.ID != "advent-gift" || p.CreatedBy != "manager" || p.ApprovalID != grant.ID || p.RepoMode != "local" {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call(create); w.Code != 200 {
		t.Fatal("approved retry", w.Code, w.Body.String())
	}
	all, _ := h.s.Projects(ctx, storage.DefaultCompany)
	if len(all) != 2 {
		t.Fatal("exactly one project per approved request", len(all))
	}
	if !feedHas(h.feed("advent-gift"), "project.created", "created by manager, approved by the owner (approval "+grant.ID+")") {
		t.Fatal(h.feed("advent-gift"))
	}
	// The creator starts the new project's work; other projects stay closed.
	w = call(factorytools.Call{Name: "factory_task_create", Arguments: map[string]any{"title": "Plan the MVP", "project_id": "advent-gift", "request_id": "plan"}})
	var created tasks.Task
	if json.Unmarshal(w.Body.Bytes(), &created); w.Code != 200 || created.ProjectID != "advent-gift" {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, w = h.create(map[string]any{"id": "owners", "name": "Owner's"}); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w = call(factorytools.Call{Name: "factory_task_create", Arguments: map[string]any{"title": "x", "project_id": "owners", "request_id": "x"}}); w.Code != 403 {
		t.Fatal("task in a project the agent did not create", w.Code)
	}
	// An agent below the owner's direct reports gets no factory_project_create, unless its config allows it.
	if h.c.Agents["demo"].CreatesProjects() || !h.c.Agents["manager"].CreatesProjects() {
		t.Fatal("default permission")
	}
	yes := true
	demo := h.c.Agents["demo"]
	demo.Spec.Communication.CanCreateProjects = &yes
	if !demo.CreatesProjects() {
		t.Fatal("explicit permission")
	}
}
