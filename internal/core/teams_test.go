package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/storage/storagetest"
	"appfactory/internal/tasks"
)

// teamsCore serves the repository's catalog (teams core: manager, manager-autonomous, demo, claude-demo;
// calorie: calorie-manager, calorie-ios, calorie-designer, calorie-qa) from storage.
func teamsCore(t *testing.T) (*Core, func(method, path string, body any) *httptest.ResponseRecorder) {
	t.Helper()
	s, err := storage.Open(storagetest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.DB.Close() })
	c := &Core{Store: s, Root: "../..", Token: "owner"}
	if err = c.LoadCatalogs(context.Background()); err != nil {
		t.Fatal(err)
	}
	h := c.Handler()
	return c, func(method, path string, body any) *httptest.ResponseRecorder {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer owner")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
}

// TestProjectTeams (company-teams 4.1, 4.2, 8.1): a project names its team (required with several); tasks of a
// project go to its team's agents only, created or reassigned; agents of other teams get no conversation
// there; the team of a project with live tasks stays; a project an agent creates is its team's.
func TestProjectTeams(t *testing.T) {
	c, call := teamsCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	if w := call("POST", "/projects", map[string]any{"id": "games", "name": "Games", "workspace": "/tmp/teams-games"}); w.Code != 400 || !strings.Contains(w.Body.String(), "team required") {
		t.Fatal("project without a team", w.Code, w.Body.String())
	}
	if w := call("POST", "/projects", map[string]any{"id": "games", "name": "Games", "workspace": "/tmp/teams-games", "team": "nope"}); w.Code != 400 || !strings.Contains(w.Body.String(), "unknown team nope") {
		t.Fatal(w.Code, w.Body.String())
	}
	for id, team := range map[string]string{"games": "calorie", "saas": "core"} {
		if w := call("POST", "/projects", map[string]any{"id": id, "name": id, "workspace": "/tmp/teams-" + id, "team": team}); w.Code != 200 || !strings.Contains(w.Body.String(), `"team":"`+team+`"`) {
			t.Fatal(id, w.Code, w.Body.String())
		}
	}
	// Assignment across teams.
	if w := call("POST", "/tasks", map[string]any{"project_id": "games", "title": "level 1", "assignee": "demo"}); w.Code != 409 || !strings.Contains(w.Body.String(), "team_mismatch") {
		t.Fatal("cross-team creation", w.Code, w.Body.String())
	}
	w := call("POST", "/tasks", map[string]any{"project_id": "games", "title": "level 1", "assignee": "calorie-ios"})
	var task tasks.Task
	if json.Unmarshal(w.Body.Bytes(), &task); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("PATCH", "/tasks/"+task.ID, map[string]any{"version": task.Version, "assignee": "manager"}); w.Code != 409 || !strings.Contains(w.Body.String(), "team_mismatch") {
		t.Fatal("cross-team reassignment", w.Code, w.Body.String())
	}
	if w = call("PATCH", "/tasks/"+task.ID, map[string]any{"version": task.Version, "assignee": "calorie-qa"}); w.Code != 200 {
		t.Fatal("reassignment in the team", w.Code, w.Body.String())
	}
	if _, err := c.Store.CreateTask(ctx, co, tasks.Create{ProjectID: "saas", Title: "delegated", Assignee: "calorie-ios"}); !errors.Is(err, storage.ErrTeamMismatch) {
		t.Fatal("delegation across teams", err)
	}
	// Conversations: only the project's team.
	if err := c.EnsureConversations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Store.Task(ctx, co, storage.ConversationID("games", "calorie-manager")); err != nil {
		t.Fatal("team conversation", err)
	}
	if _, err := c.Store.Task(ctx, co, storage.ConversationID("games", "demo")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("another team's conversation", err)
	}
	// The team of a project with live tasks stays.
	p, _ := c.Store.Project(ctx, co, "games")
	if w = call("PATCH", "/projects/games", map[string]any{"version": p.Version, "name": p.Name, "team": "core"}); w.Code != 409 || !strings.Contains(w.Body.String(), "live tasks") {
		t.Fatal("team change with live tasks", w.Code, w.Body.String())
	}
	// A project an agent creates belongs to its team.
	if team, err := c.projectTeam(co, tasks.ProjectCreate{CreatedBy: "manager"}); err != nil || team != "core" {
		t.Fatal(team, err)
	}
	if _, err := c.projectTeam(co, tasks.ProjectCreate{CreatedBy: "manager", Team: "calorie"}); !errors.Is(err, storage.ErrTeamMismatch) {
		t.Fatal("agent project in another team", err)
	}
	// Onboarding audit: the team's audit assignee.
	if w = call("POST", "/projects", map[string]any{"id": "old", "name": "Old", "repo_mode": "existing", "workspace": "/tmp/teams-old", "team": "calorie", "audit": true}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	old, _ := c.Store.Project(ctx, co, "old")
	if audit, _ := c.Store.Task(ctx, co, old.Setup.AuditTaskID); audit.Assignee != "calorie-manager" {
		t.Fatalf("audit %+v", audit)
	}
}

// TestOwnerMessageTeams (company-teams 5.1, 8.1): the owner's unaddressed message goes to the lead of the
// project's team; outside a team's project to the lead of the team named; @name to that lead.
func TestOwnerMessageTeams(t *testing.T) {
	c, call := teamsCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	for id, team := range map[string]string{"games": "calorie", "saas": "core", "plain": ""} {
		if _, err := c.Store.CreateProject(ctx, co, tasks.ProjectCreate{ID: id, Name: id, Workspace: "/tmp/owner-teams-" + id, Team: team}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		project, team, body, want string
	}{
		{"games", "", "Plan the next level", "calorie-manager"},
		{"saas", "", "Ship billing", "manager"},
		{"games", "core", "The project's team wins", "calorie-manager"},
		{"plain", "calorie", "Try something", "calorie-manager"},
		{"plain", "", "@manager status?", "manager"},
		{"games", "", "@manager help the games team", ""},
		{"plain", "", "Nobody named", ""},
	} {
		w := call("POST", "/messages", map[string]any{"project_id": tc.project, "team": tc.team, "body": tc.body, "request_id": tc.body})
		var m messaging.Message
		_ = json.Unmarshal(w.Body.Bytes(), &m)
		if tc.want == "" {
			if w.Code != 400 {
				t.Errorf("%q: %d %s", tc.body, w.Code, w.Body.String())
			}
			continue
		}
		if w.Code != 200 || m.Recipient != tc.want {
			t.Errorf("%q: %d %s", tc.body, w.Code, w.Body.String())
		}
	}
}

// TestTeamRemovalInUse (company-teams 4.1, 8.1): a catalog version that removes a team a project belongs to is
// refused naming the project; after moving the project it saves.
func TestTeamRemovalInUse(t *testing.T) {
	c, call := teamsCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	if _, err := c.Store.CreateProject(ctx, co, tasks.ProjectCreate{ID: "experiment", Name: "Experiment", Workspace: "/tmp/teams-experiment", Team: "calorie"}); err != nil {
		t.Fatal(err)
	}
	remove := CatalogChange{Base: c.catalogVersion(co), Delete: []string{"configs/teams/calorie.yaml", "configs/agents/calorie-manager.yaml", "configs/agents/calorie-ios.yaml",
		"configs/agents/calorie-designer.yaml", "configs/agents/calorie-qa.yaml", "configs/policies/calorie-project.yaml"}}
	if w := call("POST", "/catalog", remove); w.Code == 200 || !strings.Contains(w.Body.String(), "team calorie is used by project experiment") {
		t.Fatal("removing a used team", w.Code, w.Body.String())
	}
	p, _ := c.Store.Project(ctx, co, "experiment")
	if w := call("PATCH", "/projects/experiment", map[string]any{"version": p.Version, "name": p.Name, "team": "core"}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/catalog", remove); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var teams struct {
		Teams []TeamView `json:"teams"`
	}
	if w := call("GET", "/catalog/teams", nil); json.Unmarshal(w.Body.Bytes(), &teams) != nil || len(teams.Teams) != 1 || teams.Teams[0].Name != "core" || teams.Teams[0].Lead != "manager" || strings.Join(teams.Teams[0].Projects, ",") != "experiment" || len(teams.Teams[0].Agents) != 4 {
		t.Fatal(w.Body.String())
	}
}

// TestTwoTeamsOneNode (company-teams 8.1): the company's node serves both teams: ready tasks of both teams'
// projects are dispatched to the same queue, with nothing per team in the way.
func TestTwoTeamsOneNode(t *testing.T) {
	c, _ := teamsCore(t)
	ctx := context.Background()
	co := storage.DefaultCompany
	for id, assignee := range map[string]string{"games": "calorie-ios", "saas": "demo"} {
		team := map[string]string{"games": "calorie", "saas": "core"}[id]
		if _, err := c.Store.CreateProject(ctx, co, tasks.ProjectCreate{ID: id, Name: id, Workspace: "/tmp/one-node-" + id, Team: team}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Store.CreateTask(ctx, co, tasks.Create{ID: "t-" + id, ProjectID: id, Title: "work", Assignee: assignee, Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Schedule(ctx); err != nil {
		t.Fatal(err)
	}
	runs, err := c.Store.List(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	projects := map[string]bool{}
	for _, r := range runs {
		projects[r.Spec.Project] = true
	}
	if !projects["games"] || !projects["saas"] {
		t.Fatalf("both teams' tasks dispatched: %v", projects)
	}
}
