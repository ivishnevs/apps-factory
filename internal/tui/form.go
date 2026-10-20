package tui

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// formState is the create task form (n on the board): project, title, description (a textarea, or ctrl+e
// for $EDITOR), assignee, priority, dependencies, ready now. It sends POST /tasks with a request_id; errors
// are shown at their field, Core's refusal under the form.
type formState struct {
	focus    int
	project  int
	title    textinput.Model
	desc     textarea.Model
	assignee int // 0 is unassigned, then the catalog agents
	prio     textinput.Model
	deps     map[string]bool
	depAt    int
	ready    bool
	errs     map[int]string
	server   string
	sending  bool
	agents   []string
	projects []string
}

const (
	fProject = iota
	fTitle
	fDesc
	fAssignee
	fPriority
	fDeps
	fReady
	fSubmit
	fCount
)

var formLabels = [fCount]string{"Project", "Title", "Description", "Assignee", "Priority", "Depends on", "Ready now", ""}

type (
	formDoneMsg struct {
		text string
		err  error
	}
	formEditorMsg struct {
		text string
		err  error
	}
)

// openForm opens the create task form in the selected project.
func (m *Model) openForm() tea.Cmd {
	f := &formState{deps: map[string]bool{}, errs: map[int]string{}, projects: m.projects(), focus: fTitle, ready: true}
	for _, a := range m.ov.Agents {
		f.agents = append(f.agents, a.Name)
	}
	for i, p := range f.projects {
		if p == m.project {
			f.project = i
		}
	}
	f.title = textinput.New()
	f.title.Prompt = ""
	f.title.Placeholder = "what needs doing"
	f.title.CharLimit = 1024
	f.desc = textarea.New()
	f.desc.Placeholder = "details, acceptance criteria (ctrl+e opens $EDITOR)"
	f.desc.ShowLineNumbers = false
	f.desc.Prompt = ""
	f.desc.CharLimit = 64 * 1024
	f.desc.SetHeight(4)
	f.prio = textinput.New()
	f.prio.Prompt = ""
	f.prio.SetValue("5")
	f.prio.CharLimit = 5
	m.form = f
	return f.focusField()
}

func (f *formState) projectID() string {
	if f.project < len(f.projects) {
		return f.projects[f.project]
	}
	return ""
}

func (f *formState) assigneeName() string {
	if f.assignee > 0 && f.assignee <= len(f.agents) {
		return f.agents[f.assignee-1]
	}
	return ""
}

// depCandidates are the open tasks of the form's project.
func (m *Model) depCandidates(f *formState) []Task {
	out := []Task{}
	for _, t := range m.ov.Tasks {
		if t.ProjectID == f.projectID() && t.Kind != "conversation" && !terminal(t.Status) {
			out = append(out, t)
		}
	}
	return out
}

func (f *formState) focusField() tea.Cmd {
	f.title.Blur()
	f.desc.Blur()
	f.prio.Blur()
	switch f.focus {
	case fTitle:
		return f.title.Focus()
	case fDesc:
		return f.desc.Focus()
	case fPriority:
		return f.prio.Focus()
	}
	return nil
}

// validate checks the fields; errors are shown at their field.
func (m *Model) validateForm(f *formState) (Action, bool) {
	f.errs = map[int]string{}
	a := Action{Kind: "create", Project: f.projectID(), Title: strings.TrimSpace(f.title.Value()), Text: strings.TrimSpace(f.desc.Value()), Assignee: f.assigneeName(), Ready: f.ready}
	if a.Project == "" {
		f.errs[fProject] = "no project"
	}
	if a.Title == "" {
		f.errs[fTitle] = "a title is required"
	} else if len(a.Title) > 1024 {
		f.errs[fTitle] = "at most 1024 bytes"
	}
	if len(a.Text) > 64*1024 {
		f.errs[fDesc] = "at most 64 KiB"
	}
	p, err := strconv.Atoi(strings.TrimSpace(f.prio.Value()))
	switch {
	case err != nil:
		f.errs[fPriority] = "a number"
	case p < -1000 || p > 1000:
		f.errs[fPriority] = "-1000..1000"
	}
	a.Priority = p
	if a.Ready && a.Assignee == "" {
		f.errs[fAssignee] = "a ready task needs an assignee"
	}
	for _, t := range m.depCandidates(f) {
		if f.deps[t.ID] {
			a.Deps = append(a.Deps, t.ID)
		}
	}
	return a, len(f.errs) == 0
}

func (m *Model) submitForm() tea.Cmd {
	f := m.form
	a, ok := m.validateForm(f)
	if !ok || f.sending {
		return nil
	}
	f.sending, f.server = true, ""
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		text, err := c.Do(rctx, a)
		return formDoneMsg{text, err}
	}
}

func (m *Model) onFormDone(msg formDoneMsg) tea.Cmd {
	f := m.form
	if f == nil {
		return nil
	}
	f.sending = false
	if msg.err != nil {
		f.server = msg.err.Error()
		return nil
	}
	m.form = nil
	m.setToast("✓ "+msg.text, false)
	return m.loadOverview()
}

// editDescription opens $EDITOR (vi without one) on the description.
func (m *Model) editDescription() tea.Cmd {
	f := m.form
	tmp, err := os.CreateTemp("", "factory-task-*.md")
	if err != nil {
		f.server = err.Error()
		return nil
	}
	_, _ = tmp.WriteString(f.desc.Value())
	tmp.Close()
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], tmp.Name())...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer os.Remove(tmp.Name())
		if err != nil {
			return formEditorMsg{err: err}
		}
		b, err := os.ReadFile(tmp.Name())
		return formEditorMsg{text: strings.TrimRight(string(b), "\n"), err: err}
	})
}

func (m *Model) onFormEditor(msg formEditorMsg) {
	if m.form == nil {
		return
	}
	if msg.err != nil {
		m.form.server = "editor: " + msg.err.Error()
		return
	}
	m.form.desc.SetValue(msg.text)
}

func (f *formState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	switch key {
	case "esc":
		m.form = nil
		return nil
	case "ctrl+s":
		return m.submitForm()
	case "ctrl+e":
		if f.focus == fDesc {
			return m.editDescription()
		}
	case "tab":
		f.focus = (f.focus + 1) % fCount
		return f.focusField()
	case "shift+tab":
		f.focus = (f.focus + fCount - 1) % fCount
		return f.focusField()
	}
	cands := m.depCandidates(f)
	switch f.focus {
	case fTitle, fPriority:
		if key == "enter" || key == "down" {
			f.focus++
			return f.focusField()
		}
		if key == "up" {
			f.focus--
			return f.focusField()
		}
		var cmd tea.Cmd
		if f.focus == fTitle {
			f.title, cmd = f.title.Update(k)
		} else {
			f.prio, cmd = f.prio.Update(k)
		}
		return cmd
	case fDesc:
		var cmd tea.Cmd
		f.desc, cmd = f.desc.Update(k)
		return cmd
	}
	switch key {
	case "down", "j":
		f.focus = min(f.focus+1, fCount-1)
		return f.focusField()
	case "up", "k":
		f.focus = max(f.focus-1, 0)
		return f.focusField()
	case "left", "h", "right", "l":
		d := 1
		if key == "left" || key == "h" {
			d = -1
		}
		switch f.focus {
		case fProject:
			if n := len(f.projects); n > 0 {
				f.project = (f.project + d + n) % n
				f.deps, f.depAt = map[string]bool{}, 0
			}
		case fAssignee:
			n := len(f.agents) + 1
			f.assignee = (f.assignee + d + n) % n
		case fDeps:
			f.depAt = min(max(f.depAt+d, 0), max(len(cands)-1, 0))
		case fReady:
			f.ready = !f.ready
		}
	case "space", " ", "x":
		switch f.focus {
		case fDeps:
			if f.depAt < len(cands) {
				id := cands[f.depAt].ID
				f.deps[id] = !f.deps[id]
			}
		case fReady:
			f.ready = !f.ready
		}
	case "enter":
		if f.focus == fSubmit {
			return m.submitForm()
		}
		f.focus = min(f.focus+1, fCount-1)
		return f.focusField()
	}
	return nil
}

func (m *Model) formLines(w, h int) []string {
	f := m.form
	mw := min(w-4, 90)
	inner := mw - 4
	lw := 15
	fw := inner - lw
	f.title.SetWidth(max(fw-3, 10))
	f.prio.SetWidth(8)
	f.desc.SetWidth(max(fw-3, 10))
	descH := 4
	if h < 26 {
		descH = 2
	}
	f.desc.SetHeight(descH)
	lines := []string{}
	label := func(i int) string {
		st := stFaint
		mark := "  "
		if f.focus == i {
			st, mark = stTitle, stAccent.Render("▸ ")
		}
		return mark + st.Render(fit(formLabels[i], lw-2))
	}
	errLine := func(i int) {
		if e, ok := f.errs[i]; ok {
			lines = append(lines, fit("", lw)+" "+stRedBold.Render("✗ "+e))
		}
	}
	choice := func(i int, v string) string {
		if f.focus == i {
			return stDim.Render("◀ ") + lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(" "+v+" ") + stDim.Render(" ▶")
		}
		return stChip.Render(" " + v + " ")
	}
	lines = append(lines, label(fProject)+choice(fProject, dash(f.projectID())))
	errLine(fProject)
	lines = append(lines, label(fTitle)+" "+f.title.View())
	errLine(fTitle)
	for i, l := range strings.Split(f.desc.View(), "\n") {
		if i == 0 {
			lines = append(lines, label(fDesc)+" "+l)
		} else {
			lines = append(lines, fit("", lw)+" "+l)
		}
	}
	errLine(fDesc)
	who := "unassigned"
	if a := f.assigneeName(); a != "" {
		who = a
		if r := roleOf(m, a); r != "" {
			who += " · " + r
		}
	}
	lines = append(lines, label(fAssignee)+choice(fAssignee, who))
	errLine(fAssignee)
	lines = append(lines, label(fPriority)+" "+f.prio.View()+stDim.Render("  -1000..1000, higher runs first"))
	errLine(fPriority)
	cands := m.depCandidates(f)
	depLine := []string{}
	for i, t := range cands {
		box := "☐"
		if f.deps[t.ID] {
			box = stGreen.Render("☑")
		}
		item := box + " " + t.ID
		if f.focus == fDeps && i == f.depAt {
			item = lipgloss.NewStyle().Background(theme.surfaceHi).Bold(true).Render(" " + item + " ")
		} else {
			item = " " + item + " "
		}
		depLine = append(depLine, item)
	}
	if len(cands) == 0 {
		depLine = append(depLine, stDim.Render("no open tasks in "+dash(f.projectID())))
	}
	dl := strings.Join(depLine, "")
	if f.focus == fDeps && f.depAt < len(cands) {
		// Keep the cursor in view in a long list.
		for ansiWidth(dl) > fw && f.depAt > 0 && len(depLine) > 1 {
			depLine = depLine[1:]
			dl = stDim.Render("… ") + strings.Join(depLine, "")
			if strings.Contains(ansiStrip(depLine[0]), cands[f.depAt].ID) {
				break
			}
		}
	}
	lines = append(lines, label(fDeps)+cut(dl, fw))
	if f.focus == fDeps && f.depAt < len(cands) {
		lines = append(lines, fit("", lw)+stFaint.Render(cut(cands[f.depAt].Title+" · "+cands[f.depAt].Status, fw)))
	}
	toggle := stDim.Render("○ no — stays in the backlog")
	if f.ready {
		toggle = stGreen.Render("● yes — released to the board")
	}
	lines = append(lines, label(fReady)+" "+toggle)
	submit := lipgloss.NewStyle().Background(theme.surfaceHi).Foreground(theme.text).Render(" Create task ")
	if f.focus == fSubmit {
		submit = lipgloss.NewStyle().Background(theme.green).Foreground(theme.onAccent).Bold(true).Render(" ⏎ Create task ")
	}
	if f.sending {
		submit = stFaint.Render(m.spin.View() + " creating…")
	}
	lines = append(lines, "", fit("", lw)+submit)
	if f.server != "" {
		for _, l := range wrap("Core refused: "+f.server, inner-2) {
			lines = append(lines, stRedBold.Render(l))
		}
	}
	hint := stAccent.Render("tab") + stFaint.Render(" next ") + stAccent.Render("←→") + stFaint.Render(" choose ") + stAccent.Render("space") + stFaint.Render(" toggle ") +
		stAccent.Render("ctrl+e") + stFaint.Render(" $EDITOR ") + stAccent.Render("ctrl+s") + stFaint.Render(" create ") + stAccent.Render("esc") + stFaint.Render(" cancel")
	lines = append(lines, rule(inner), hint)
	return frame(stTitle.Render("New task"), lines, mw, min(len(lines)+2, h), theme.accent, true, false)
}

// formHint names the form keys in the footer.
func formHint() [][2]string {
	return [][2]string{{"tab", "next field"}, {"ctrl+s", "create"}, {"esc", "cancel"}}
}
