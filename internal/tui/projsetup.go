package tui

import (
	"cmp"
	"fmt"
	"strings"

	"appfactory/internal/tasks"
)

// Project setup in the owner's words (project-creation 2.3): a project created without a ready repository is
// set up on the node by its first task's run (tasks.Project.Setup, design D2).

// repoModeWords: what kind of repository a project has or gets.
func repoModeWords(mode string) string {
	switch mode {
	case RepoLocal:
		return "new local repository"
	case RepoGitHub:
		return "new private GitHub repository"
	case RepoExisting:
		return "existing repository"
	}
	return ""
}

// nodeName is a node's name, or its ID when the node is unknown.
func (m *Model) nodeName(id string) string {
	for _, n := range m.ov.Nodes {
		if n.NodeID == id {
			return cmp.Or(n.Name, id)
		}
	}
	return id
}

// projectOf is a project of the overview.
func (m *Model) projectOf(id string) (tasks.Project, bool) {
	for _, st := range m.ov.Status {
		if st.Project.ID == id {
			return st.Project, true
		}
	}
	return tasks.Project{}, false
}

// setupText says where a project's repository setup stands; "" when it is ready or none is needed. hint is
// what the owner can do; bad is a failed setup (its reason is the point).
func (m *Model) setupText(p tasks.Project) (text, hint string, bad bool) {
	s := p.Setup
	if s == nil || s.State == tasks.SetupReady {
		return "", "", false
	}
	repo := cmp.Or(repoModeWords(p.RepoMode), "repository")
	switch s.State {
	case tasks.SetupRunning:
		text = "setting up the " + repo
		if s.NodeID != "" {
			text += " on " + m.nodeName(s.NodeID)
		}
		if s.TaskID != "" {
			text += " (" + s.TaskID + "'s run)"
		}
		return text, "", false
	case tasks.SetupFailed:
		text = "repository setup failed"
		if s.NodeID != "" {
			text += " on " + m.nodeName(s.NodeID)
		}
		if s.Reason != "" {
			text += ": " + oneLine(s.Reason)
		}
		task := cmp.Or(s.TaskID, "the task")
		return text, "fix it on the node; " + task + " retries on its own, r on it retries now", true
	}
	text = "the " + repo + " is set up when the first task runs"
	if s.Audit && s.AuditTaskID != "" {
		text = "the " + repo + " is onboarded when the audit " + s.AuditTaskID + " runs"
	}
	return text, "", false
}

// projectSetupLines are the board's lines about the filtered project's repository while it is not ready; a
// project without tasks is pointed at its first task, which sets the repository up.
func (m *Model) projectSetupLines(w int) []string {
	p, ok := m.projectOf(m.project)
	if !ok {
		return nil
	}
	text, hint, bad := m.setupText(p)
	if text == "" {
		return nil
	}
	empty := true
	for _, t := range m.ov.Tasks {
		if t.ProjectID == p.ID && t.Kind == "" {
			empty = false
			break
		}
	}
	sign, st := "◌ ", stYellow
	if bad {
		sign, st = "✗ ", stRedBold
	}
	line := " " + st.Render(sign+text)
	if empty {
		line += stFaint.Render(" · ") + stAccent.Render("n") + stFaint.Render(" adds the first task")
	}
	out := []string{cut(line, w)}
	if hint != "" {
		out = append(out, cut("   "+stFaint.Render(hint), w))
	}
	return out
}

// projectSetupWhy is the why line of a task waiting for its project's repository (tasks.ReasonProjectSetup).
func (m *Model) projectSetupWhy(t Task) string {
	p, _ := m.projectOf(t.ProjectID)
	if p.Setup != nil && p.Setup.State == tasks.SetupFailed {
		why := "↳ waits for the repository: setup failed"
		if p.Setup.Reason != "" {
			why += " (" + oneLine(p.Setup.Reason) + ")"
		}
		if p.Setup.TaskID != "" {
			why += "; " + p.Setup.TaskID + " retries it"
		}
		return why
	}
	if p.Setup != nil && p.Setup.TaskID != "" {
		return "↳ waits for the repository: " + p.Setup.TaskID + " sets it up first"
	}
	return "↳ waits for the repository: the project's first task sets it up"
}

// projectCreateArgs are the arguments of a factory_project_create call as labelled lines, in a fixed order.
func projectCreateArgs(a map[string]any) [][2]string {
	str := func(k string) string {
		switch v := a[k].(type) {
		case string:
			return v
		case nil:
			return ""
		default:
			return fmt.Sprint(v)
		}
	}
	on := func(k string, def bool) bool {
		if v, ok := a[k].(bool); ok {
			return v
		}
		return def
	}
	out := [][2]string{{"project", cmp.Or(str("name"), str("project_id"))}, {"id", str("project_id")}}
	if d := str("description"); d != "" {
		out = append(out, [2]string{"about", d})
	}
	mode := str("repo_mode")
	if mode == "" {
		switch {
		case str("workspace") == "" && str("repo_url") == "":
			mode = RepoLocal
		default:
			mode = RepoExisting
		}
	}
	repo := cmp.Or(repoModeWords(mode), mode)
	out = append(out, [2]string{"repo", repo})
	for _, k := range [][2]string{{"workspace", "folder"}, {"repo_url", "url"}, {"default_branch", "branch"}} {
		if v := str(k[0]); v != "" {
			out = append(out, [2]string{k[1], v})
		}
	}
	if mode == RepoExisting && on("audit", false) {
		out = append(out, [2]string{"audit", "yes, before any other work"})
	}
	openspec := "on"
	if !on("openspec", true) {
		openspec = "off"
	}
	out = append(out, [2]string{"openspec", openspec})
	return out
}

// projectCreateBrief is a factory_project_create call in one line: "create project Project 6 (existing repository /path)".
func projectCreateBrief(a map[string]any) string {
	var name, repo, where string
	for _, kv := range projectCreateArgs(a) {
		switch kv[0] {
		case "project":
			name = kv[1]
		case "repo":
			repo = kv[1]
		case "folder", "url":
			if where == "" {
				where = kv[1]
			}
		}
	}
	return strings.TrimSpace("create project " + name + " (" + strings.TrimSpace(repo+" "+where) + ")")
}
