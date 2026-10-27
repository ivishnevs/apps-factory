package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/tasks"
)

func ctrlN(m *Model) tea.Cmd {
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	return cmd
}

// TestCreateProjectForm: ctrl+n opens the form; an empty form is refused at its fields; the ID follows the
// name; a filled form sends create_project with a request id and the project appears; an existing project
// needs a path or URL; Core's refusal keeps the form open with the reason.
func TestCreateProjectForm(t *testing.T) {
	m, c := demoModel(t, 120, 40)
	ctrlN(m)
	if m.projForm == nil || m.projForm.focus != pName {
		t.Fatal("no form")
	}
	run(m, key(m, "ctrl+s"))
	if len(c.Sent) != 0 || m.projForm.errs[pName] == "" || m.projForm.errs[pID] == "" {
		t.Fatalf("%+v", m.projForm.errs)
	}
	typeText(m, "Advent Gift!")
	if got := m.projForm.id.Value(); got != "advent-gift" {
		t.Fatal("id follows the name:", got)
	}
	press(m, "tab", "tab")
	typeText(m, "A gift calendar.")
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "New project") || !strings.Contains(s, "New local repository") || strings.Contains(s, "Path on node") {
		t.Fatal(s)
	}
	run(m, key(m, "ctrl+s"))
	a := lastSent(t, c)
	p := a.NewProject
	if a.Kind != "create_project" || p == nil || p.ID != "advent-gift" || p.Name != "Advent Gift!" || p.Description != "A gift calendar." || p.RepoMode != RepoLocal ||
		p.RequestID == "" || p.OpenSpec == nil || p.OpenSpec.Enabled == nil || !*p.OpenSpec.Enabled || p.Workspace != "" || p.Audit {
		t.Fatalf("%+v", p)
	}
	if m.projForm != nil || m.project != "advent-gift" || !strings.Contains(m.toast, "created project advent-gift") {
		t.Fatal("form still open", m.toast)
	}
	ov, _ := c.Overview(context.Background())
	if ov.Status[len(ov.Status)-1].Project.ID != "advent-gift" {
		t.Fatal("project not in the fixture")
	}
	// Its repository waits for the first task: no workspace yet; the board says so and offers the first task.
	if np := ov.Status[len(ov.Status)-1].Project; np.Workspace != "" || np.Setup == nil || np.Setup.State != tasks.SetupPending || np.RepoMode != RepoLocal {
		t.Fatalf("%+v", np)
	}
	if !strings.Contains(m.toast, "set up when the first task runs: n adds it") || m.tab != tabBoard {
		t.Fatal(m.toast)
	}
	m.ov = ov
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "the new local repository is set up when the first task runs · n adds the first task") {
		t.Fatal(s)
	}

	// An existing project: the path, URL, branch and audit fields appear; one of path or URL is required.
	ctrlN(m)
	typeText(m, "Old App")
	press(m, "tab", "tab", "tab", "space", "tab", "right", "right")
	if m.projForm.repoMode() != RepoExisting || m.projForm.openspec {
		t.Fatal(m.projForm.repoMode(), m.projForm.openspec)
	}
	run(m, key(m, "ctrl+s"))
	if m.projForm.errs[pPath] == "" {
		t.Fatalf("%+v", m.projForm.errs)
	}
	press(m, "tab")
	typeText(m, "relative/path")
	run(m, key(m, "ctrl+s"))
	if !strings.Contains(m.projForm.errs[pPath], "absolute") {
		t.Fatalf("%+v", m.projForm.errs)
	}
	for m.projForm.path.Value() != "" {
		press(m, "backspace")
	}
	press(m, "tab")
	typeText(m, "git@github.com:me/old-app.git")
	press(m, "tab")
	typeText(m, "develop")
	runDeep(m, key(m, "ctrl+s"), 3)
	p = lastSent(t, c).NewProject
	if p.ID != "old-app" || p.RepoMode != RepoExisting || p.RepoURL != "git@github.com:me/old-app.git" || p.DefaultBranch != "develop" || !p.Audit || *p.OpenSpec.Enabled {
		t.Fatalf("%+v", p)
	}
	if m.projForm != nil {
		t.Fatal("form open after success:", m.projForm.server)
	}
	ov, _ = c.Overview(context.Background())
	// As Core: the audit task is ready and runnable (its run sets the repository up), the setup waits for it.
	found := false
	for _, x := range ov.Tasks {
		found = found || x.ProjectID == "old-app" && x.Reason == tasks.ReasonRunnable && x.Status == "READY"
	}
	if !found {
		t.Fatal("no audit task in the demo")
	}
	np := ov.Status[len(ov.Status)-1].Project
	if np.RepoMode != RepoExisting || np.Setup == nil || np.Setup.State != tasks.SetupPending || !np.Setup.Audit || np.Setup.AuditTaskID != "old-app-1" || np.CreatedBy != "owner" {
		t.Fatalf("%+v %+v", np, np.Setup)
	}
	if !strings.Contains(m.toast, "audit task runs first") || m.tab != tabBoard || m.project != "old-app" {
		t.Fatal(m.toast, m.tab, m.project)
	}
	page, _ := c.Feed(context.Background(), FeedQuery{Project: "old-app"}, 0, 10)
	if len(page.Items) != 1 || page.Items[0].Type != "project.created" || !strings.Contains(page.Items[0].Summary, "(audit first)") {
		t.Fatalf("%+v", page.Items)
	}

	// A taken ID is refused locally; Core's refusal keeps the form and its values.
	ctrlN(m)
	typeText(m, "Old App")
	run(m, key(m, "ctrl+s"))
	if !strings.Contains(m.projForm.errs[pID], "exists") {
		t.Fatalf("%+v", m.projForm.errs)
	}
	press(m, "tab")
	typeText(m, "-2")
	m.c = failingClient{c}
	run(m, key(m, "ctrl+s"))
	if m.projForm == nil || m.projForm.name.Value() != "Old App" || !strings.Contains(m.projForm.server, "400 Bad Request") {
		t.Fatal("refusal not shown")
	}
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "Core refused: 400 Bad Request") {
		t.Fatal(s)
	}
	key(m, "esc")
	if m.projForm != nil {
		t.Fatal("esc keeps the form")
	}
}

// TestHTTPClientCreateProject: create_project is POST /projects with the body and a request id.
func TestHTTPClientCreateProject(t *testing.T) {
	fc, _, c := newFakeCore(t)
	on := true
	text, err := c.Do(context.Background(), Action{Kind: "create_project", NewProject: &tasks.ProjectCreate{ID: "advent-gift", Name: "Advent gift", RepoMode: RepoGitHub, OpenSpec: &tasks.ProjectOpenSpec{Enabled: &on}}})
	if err != nil || text != "created project advent-gift: Advent gift" {
		t.Fatal(text, err)
	}
	b := fc.bodies["/projects"]
	if b["id"] != "advent-gift" || b["name"] != "Advent gift" || b["repo_mode"] != "github" || b["request_id"] == "" || b["openspec"].(map[string]any)["enabled"] != true {
		t.Fatal(b)
	}
	if _, err = c.Do(context.Background(), Action{Kind: "create_project", NewProject: &tasks.ProjectCreate{ID: "x", Name: "x", RequestID: "r-1"}}); err != nil || fc.bodies["/projects"]["request_id"] != "r-1" {
		t.Fatal(err, fc.bodies["/projects"])
	}
	fc.fail["/projects"] = 400
	if _, err = c.Do(context.Background(), Action{Kind: "create_project", NewProject: &tasks.ProjectCreate{ID: "y", Name: "y"}}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatal(err)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"Advent Gift!": "advent-gift", "  Mój  app 2 ": "m-j-app-2", "": "", "---": ""} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}
