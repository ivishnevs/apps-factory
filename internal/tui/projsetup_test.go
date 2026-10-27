package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/messaging"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestProjectSetupViews (project-creation 2.3): a task waiting for its project's repository says why, the
// header chips mark projects whose repository is not ready, the setup states read as words, the feed marks
// project events, and an agent's factory_project_create approval reads as a project, not raw arguments.
func TestProjectSetupViews(t *testing.T) {
	m, _ := demoModel(t, 140, 40)
	p42, _ := m.taskByID("P4-2")
	if got := m.whyLine(p42); got != "↳ waits for the repository: setup failed (gh is not authenticated on node mac-studio: run gh auth login); P4-1 retries it" {
		t.Fatal(got)
	}
	head := ansi.Strip(m.Render())
	if !strings.Contains(head, "✗ project-4") || !strings.Contains(head, "◌ project-5") {
		t.Fatal(head)
	}

	p := tasks.Project{ID: "x", RepoMode: RepoGitHub, Setup: &tasks.ProjectSetup{State: tasks.SetupRunning, NodeID: "n3", TaskID: "X-1"}}
	if text, hint, bad := m.setupText(p); text != "setting up the new private GitHub repository on linux-1 (X-1's run)" || hint != "" || bad {
		t.Fatal(text, hint, bad)
	}
	p.Setup = &tasks.ProjectSetup{State: tasks.SetupPending, Audit: true, AuditTaskID: "x-1"}
	p.RepoMode = RepoExisting
	if text, _, _ := m.setupText(p); text != "the existing repository is onboarded when the audit x-1 runs" {
		t.Fatal(text)
	}
	p.Setup.State = tasks.SetupReady
	if text, _, _ := m.setupText(p); text != "" {
		t.Fatal(text)
	}
	m.ov.Tasks = append(m.ov.Tasks, Task{Task: tasks.Task{ID: "x-2", ProjectID: "project-5", Status: "READY"}, Reason: tasks.ReasonProjectSetup})
	if got := m.whyLine(m.ov.Tasks[len(m.ov.Tasks)-1]); got != "↳ waits for the repository: the project's first task sets it up" {
		t.Fatal(got)
	}

	for typ, icon := range map[string]string{"project.created": "+", "project.provisioned": "✓", "project.setup_failed": "✗"} {
		if got := ansi.Strip(feedIcon(storage.FeedItem{Type: typ})); got != icon {
			t.Fatal(typ, got)
		}
	}
	if r := feedActivity(storage.FeedItem{Type: "project.setup_failed", Actor: "pm", Summary: "failed"}); r.Tool != "setup failed" {
		t.Fatal(r.Tool)
	}

	args := m.ov.Approvals[2].Call.Arguments
	got := []string{}
	for _, kv := range projectCreateArgs(args) {
		got = append(got, kv[0]+"="+kv[1])
	}
	if strings.Join(got, "|") != "project=Project 6|id=project-6|about=Companion app for Project 1: reminders and offline sync.|repo=existing repository|url=https://github.com/company-1/project-6|branch=main|audit=yes, before any other work|openspec=on" {
		t.Fatal(got)
	}
	if b := projectCreateBrief(map[string]any{"project_id": "p", "name": "P", "openspec": false}); b != "create project P (new local repository)" {
		t.Fatal(b)
	}
	// The prompt bar asks about the project in words.
	m.ov.Inbox = []messaging.Message{m.ov.Inbox[5]}
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "pm wants to create project Project 6 (existing repository https://github.com/company-1/project-6) — approve?") {
		t.Fatal(s)
	}
}
