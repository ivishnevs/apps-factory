package tui

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Repository modes of a new project (project-creation): Core provisions a new local repository or a new
// private GitHub repository, or onboards an existing project from a path on the node and/or a git URL.
const (
	RepoLocal    = "local"
	RepoGitHub   = "github"
	RepoExisting = "existing"
)

var repoModes = []string{RepoLocal, RepoGitHub, RepoExisting}

var repoModeNames = map[string]string{
	RepoLocal:    "New local repository",
	RepoGitHub:   "New private GitHub repository",
	RepoExisting: "Existing project (path or URL)",
}

// projectID is Core's project ID rule (storage entityName).
var projectID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`)

// gitURL is a loose check of a repository URL; Core has the final word.
var gitURL = regexp.MustCompile(`^(https?://|ssh://|git://|file://|[\w.-]+@[\w.-]+:)\S+$`)

var branchChars = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// Slug turns a project name into a project ID: lower case, words joined by dashes.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		default:
			dash = true
		}
		if b.Len() >= 64 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// ValidateNewProject checks a new project before it is sent: errors by field (id, name, description,
// workspace, repo_url, default_branch). existing are the project IDs already taken.
func ValidateNewProject(p tasks.ProjectCreate, existing []string) map[string]string {
	errs := map[string]string{}
	switch {
	case p.ID == "":
		errs["id"] = "an ID is required"
	case !projectID.MatchString(p.ID):
		errs["id"] = "letters, digits, - and _ (start with a letter or digit, at most 96)"
	case slices.Contains(existing, p.ID):
		errs["id"] = "a project " + p.ID + " exists"
	}
	if strings.TrimSpace(p.Name) == "" {
		errs["name"] = "a name is required"
	} else if len(p.Name) > 200 {
		errs["name"] = "at most 200 bytes"
	}
	if len(p.Description) > 64*1024 {
		errs["description"] = "at most 64 KiB"
	}
	if p.RepoMode == RepoExisting {
		if p.Workspace == "" && p.RepoURL == "" {
			errs["workspace"] = "a path on the node or a repository URL is required"
		}
		if p.Workspace != "" && !strings.HasPrefix(p.Workspace, "/") {
			errs["workspace"] = "an absolute path on the node (/…)"
		}
		if p.RepoURL != "" && !gitURL.MatchString(p.RepoURL) {
			errs["repo_url"] = "an https, ssh or git@host:owner/repo URL"
		}
		if b := p.DefaultBranch; b != "" && (!branchChars.MatchString(b) || strings.Contains(b, "..") || strings.HasSuffix(b, "/") || strings.HasSuffix(b, ".lock")) {
			errs["default_branch"] = "not a valid branch name"
		}
	}
	return errs
}

// describeNewProject is a create_project action in words.
func describeNewProject(p *tasks.ProjectCreate) string {
	if p == nil {
		return "create a project"
	}
	how := repoModeNames[RepoLocal]
	if m, ok := repoModeNames[p.RepoMode]; ok {
		how = m
	}
	if p.RepoMode == RepoExisting {
		how = "existing project " + strings.Join(nonEmpty(p.Workspace, p.RepoURL), " / ")
		if p.Audit {
			how += ", audit first"
		}
	}
	return fmt.Sprintf("create project %s %q (%s)", p.ID, p.Name, strings.ToLower(how[:1])+how[1:])
}

func nonEmpty(s ...string) []string {
	out := []string{}
	for _, x := range s {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// projFormState is the new project form (ctrl+n anywhere, or the palette): name, ID (follows the name until
// edited), description, OpenSpec, repository mode and, for an existing project, its path, URL, branch and
// the audit. It sends POST /projects with a request id per submit; errors are shown at their field, Core's
// refusal under the form, which stays open.
type projFormState struct {
	focus    int
	name     textinput.Model
	id       textinput.Model
	idEdited bool
	desc     textinput.Model
	openspec bool
	mode     int
	path     textinput.Model
	url      textinput.Model
	branch   textinput.Model
	audit    bool
	errs     map[int]string
	server   string
	sending  bool
}

const (
	pName = iota
	pID
	pDesc
	pOpenSpec
	pRepo
	pPath
	pURL
	pBranch
	pAudit
	pSubmit
	pCount
)

var projLabels = [pCount]string{"Name", "ID", "Description", "OpenSpec", "Repository", "Path on node", "Repository URL", "Branch", "Audit first", ""}

var projErrField = map[string]int{"id": pID, "name": pName, "description": pDesc, "workspace": pPath, "repo_url": pURL, "default_branch": pBranch}

type projFormDoneMsg struct {
	text string
	err  error
}

func newInput(placeholder string, limit int) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.CharLimit = limit
	return ti
}

// openProjectForm opens the new project form.
func (m *Model) openProjectForm() tea.Cmd {
	f := &projFormState{openspec: true, audit: true, errs: map[int]string{}}
	f.name = newInput("Advent gift", 200)
	f.id = newInput("follows the name", 96)
	f.desc = newInput("what it is, for whom", 64*1024)
	f.path = newInput("/Users/me/projects/app", 4096)
	f.url = newInput("git@github.com:me/app.git", 2048)
	f.branch = newInput("main (the remote's default when empty)", 255)
	m.projForm = f
	return f.focusField()
}

func (f *projFormState) repoMode() string { return repoModes[f.mode] }

// visible are the fields shown: the existing-project fields only for that mode.
func (f *projFormState) visible() []int {
	out := []int{pName, pID, pDesc, pOpenSpec, pRepo}
	if f.repoMode() == RepoExisting {
		out = append(out, pPath, pURL, pBranch, pAudit)
	}
	return append(out, pSubmit)
}

func (f *projFormState) move(d int) tea.Cmd {
	v := f.visible()
	i := max(slices.Index(v, f.focus), 0)
	f.focus = v[(i+d+len(v))%len(v)]
	return f.focusField()
}

func (f *projFormState) inputs() map[int]*textinput.Model {
	return map[int]*textinput.Model{pName: &f.name, pID: &f.id, pDesc: &f.desc, pPath: &f.path, pURL: &f.url, pBranch: &f.branch}
}

func (f *projFormState) focusField() tea.Cmd {
	var cmd tea.Cmd
	for i, ti := range f.inputs() {
		if i == f.focus {
			cmd = ti.Focus()
		} else {
			ti.Blur()
		}
	}
	return cmd
}

// body is the POST /projects body of the form's values (without a request id).
func (f *projFormState) body() tasks.ProjectCreate {
	on := f.openspec
	p := tasks.ProjectCreate{ID: strings.TrimSpace(f.id.Value()), Name: strings.TrimSpace(f.name.Value()), Description: strings.TrimSpace(f.desc.Value()),
		RepoMode: f.repoMode(), OpenSpec: &tasks.ProjectOpenSpec{Enabled: &on}}
	if p.RepoMode == RepoExisting {
		p.Workspace, p.RepoURL, p.DefaultBranch, p.Audit = strings.TrimSpace(f.path.Value()), strings.TrimSpace(f.url.Value()), strings.TrimSpace(f.branch.Value()), f.audit
	}
	return p
}

func (m *Model) submitProjectForm() tea.Cmd {
	f := m.projForm
	if f.sending {
		return nil
	}
	p := f.body()
	f.errs = map[int]string{}
	for k, e := range ValidateNewProject(p, m.projects()) {
		f.errs[projErrField[k]] = e
	}
	if len(f.errs) > 0 {
		return nil
	}
	p.RequestID = protocol.ID() // one per submit: a retried submit after a refusal is a new request
	f.sending, f.server = true, ""
	c, ctx := m.c, m.ctx
	a := Action{Kind: "create_project", NewProject: &p}
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 60*time.Second) // provisioning a GitHub repository takes a while
		defer cancel()
		text, err := c.Do(rctx, a)
		return projFormDoneMsg{text, err}
	}
}

func (m *Model) onProjectFormDone(msg projFormDoneMsg) tea.Cmd {
	f := m.projForm
	if f == nil {
		return nil
	}
	f.sending = false
	if msg.err != nil {
		f.server = msg.err.Error()
		return nil
	}
	m.projForm = nil
	b := f.body()
	m.project = b.ID // show the new project on the board, where n adds its first task
	m.switchTab(tabBoard)
	next := " · its repository is set up when the first task runs: n adds it"
	if b.RepoMode == RepoExisting && b.Audit {
		next = " · the audit task runs first and onboards the repository"
	}
	m.setToast("✓ "+msg.text+next, false)
	return m.loadOverview()
}

func (f *projFormState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	switch key {
	case "esc":
		m.projForm = nil
		return nil
	case "ctrl+s":
		return m.submitProjectForm()
	case "tab", "down":
		return f.move(1)
	case "shift+tab", "up":
		return f.move(-1)
	}
	if ti, ok := f.inputs()[f.focus]; ok {
		if key == "enter" {
			return f.move(1)
		}
		var cmd tea.Cmd
		*ti, cmd = ti.Update(k)
		f.afterInput()
		return cmd
	}
	switch key {
	case "j":
		return f.move(1)
	case "k":
		return f.move(-1)
	case "left", "h", "right", "l":
		d := 1
		if key == "left" || key == "h" {
			d = -1
		}
		f.choose(d)
	case "space", " ", "x":
		f.choose(1)
	case "enter":
		if f.focus == pSubmit {
			return m.submitProjectForm()
		}
		return f.move(1)
	}
	return nil
}

// afterInput keeps the ID following the name until the owner edits the ID.
func (f *projFormState) afterInput() {
	switch f.focus {
	case pID:
		f.idEdited = f.id.Value() != ""
	case pName:
		if !f.idEdited {
			f.id.SetValue(Slug(f.name.Value()))
		}
	}
}

func (f *projFormState) choose(d int) {
	switch f.focus {
	case pOpenSpec:
		f.openspec = !f.openspec
	case pAudit:
		f.audit = !f.audit
	case pRepo:
		f.mode = (f.mode + d + len(repoModes)) % len(repoModes)
	}
}

// paste puts pasted text into the focused input.
func (f *projFormState) paste(msg tea.PasteMsg) tea.Cmd {
	ti, ok := f.inputs()[f.focus]
	if !ok {
		return nil
	}
	var cmd tea.Cmd
	*ti, cmd = ti.Update(msg)
	f.afterInput()
	return cmd
}

func (m *Model) projFormLines(w, h int) []string {
	f := m.projForm
	mw := min(w-4, 90)
	inner := mw - 4
	lw := 17
	fw := inner - lw
	for _, ti := range f.inputs() {
		ti.SetWidth(max(fw-3, 10))
	}
	lines := []string{}
	label := func(i int) string {
		st := stFaint
		mark := "  "
		if f.focus == i {
			st, mark = stTitle, stAccent.Render("▸ ")
		}
		return mark + st.Render(fit(projLabels[i], lw-2))
	}
	errLine := func(i int) {
		if e, ok := f.errs[i]; ok {
			lines = append(lines, fit("", lw)+" "+stRedBold.Render("✗ "+e))
		}
	}
	toggle := func(on bool, yes, no string) string {
		if on {
			return stGreen.Render("● " + yes)
		}
		return stDim.Render("○ " + no)
	}
	for _, i := range f.visible() {
		switch i {
		case pOpenSpec:
			lines = append(lines, label(i)+" "+toggle(f.openspec, "on — specs and changes kept in Core", "off"))
		case pAudit:
			lines = append(lines, label(i)+" "+toggle(f.audit, "yes — a read-only audit task for the product manager", "no"))
		case pRepo:
			v := repoModeNames[f.repoMode()]
			if f.focus == pRepo {
				v = stDim.Render("◀ ") + lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(" "+v+" ") + stDim.Render(" ▶")
			} else {
				v = stChip.Render(" " + v + " ")
			}
			lines = append(lines, label(i)+v)
			if f.repoMode() == RepoExisting {
				lines = append(lines, fit("", lw)+stFaint.Render(cut("a path on the node, a URL the node clones, or both", fw)))
			}
		case pSubmit:
			submit := lipgloss.NewStyle().Background(theme.surfaceHi).Foreground(theme.text).Render(" Create project ")
			if f.focus == pSubmit {
				submit = lipgloss.NewStyle().Background(theme.green).Foreground(theme.onAccent).Bold(true).Render(" ⏎ Create project ")
			}
			if f.sending {
				submit = stFaint.Render(m.spin.View() + " creating…")
			}
			lines = append(lines, "", fit("", lw)+submit)
		default:
			lines = append(lines, label(i)+" "+f.inputs()[i].View())
		}
		errLine(i)
	}
	if f.server != "" {
		for _, l := range wrap("Core refused: "+f.server, inner-2) {
			lines = append(lines, stRedBold.Render(l))
		}
	}
	hint := stAccent.Render("tab") + stFaint.Render(" next ") + stAccent.Render("←→") + stFaint.Render(" choose ") + stAccent.Render("space") + stFaint.Render(" toggle ") +
		stAccent.Render("ctrl+s") + stFaint.Render(" create ") + stAccent.Render("esc") + stFaint.Render(" cancel")
	lines = append(lines, rule(inner), hint)
	return frame(stTitle.Render("New project"), lines, mw, min(len(lines)+2, h), theme.accent, true, false)
}
