package tui

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/approvals"
	"appfactory/internal/storage"
)

// External links: a task's pull request (o opens it in the browser) and an artifact awaiting approval (v
// previews it: markdown and text in a viewport inside the UI, anything else downloaded to a temp file and
// opened with the system viewer).

// openCommand is the system opener: open on macOS, xdg-open elsewhere.
func openCommand() string {
	if runtime.GOOS == "darwin" {
		return "open"
	}
	return "xdg-open"
}

type openedMsg struct {
	what string
	err  error
}

// openExternal opens a URL or a file with the system opener.
func (m *Model) openExternal(target, what string) tea.Cmd {
	run := m.opts.Run
	if run == nil {
		run = execRunner
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return openedMsg{what: what, err: run(ctx, openCommand(), target)}
	}
}

// taskPR is the pull request of a task's latest exit that names one.
func taskPR(d TaskDetail) *storage.PRFacts {
	for i := len(d.Exits) - 1; i >= 0; i-- {
		if f := d.Exits[i].Facts; f != nil && f.PR != nil && f.PR.Number > 0 {
			return f.PR
		}
	}
	return nil
}

var scpRepo = regexp.MustCompile(`^[\w.-]+@([\w.-]+):(.+?)(\.git)?$`)

// prURL is the pull request's URL: the one the forge reported, else derived from a GitHub or GitLab
// repository URL of the project.
func (m *Model) prURL(project string, pr *storage.PRFacts) string {
	if pr == nil {
		return ""
	}
	if pr.URL != "" {
		return pr.URL
	}
	repo := ""
	for _, p := range m.ov.Status {
		if p.Project.ID == project {
			repo = p.Project.RepoURL
		}
	}
	host, path := "", ""
	if mm := scpRepo.FindStringSubmatch(repo); mm != nil {
		host, path = mm[1], mm[2]
	} else if u, err := url.Parse(repo); err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "ssh") {
		host, path = u.Hostname(), strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	}
	switch {
	case host == "" || path == "":
		return ""
	case strings.Contains(host, "gitlab"):
		return fmt.Sprintf("https://%s/%s/-/merge_requests/%d", host, path, pr.Number)
	}
	return fmt.Sprintf("https://%s/%s/pull/%d", host, path, pr.Number)
}

// openTaskPR opens the open task's pull request.
func (m *Model) openTaskPR() tea.Cmd {
	d := m.board.detail
	if d == nil {
		return nil
	}
	pr := taskPR(*d)
	if pr == nil {
		m.setToast(d.Task.ID+" has no pull request", true)
		return nil
	}
	u := m.prURL(d.Task.ProjectID, pr)
	if u == "" {
		m.setToast(fmt.Sprintf("PR #%d: no URL known for this repository", pr.Number), true)
		return nil
	}
	m.setToast("↗ opening "+u, false)
	return m.openExternal(u, fmt.Sprintf("PR #%d", pr.Number))
}

// previewState is an artifact preview modal: the rendered text in a viewport.
type previewState struct {
	title   string
	art     approvals.Approval
	loading bool
	err     error
	text    []string
	vp      viewport.Model
	ready   bool
	raw     []byte
	name    string
}

type previewMsg struct {
	id   string
	data []byte
	err  error
}

// textual: content to show inside the UI (valid UTF-8 without NUL bytes, by extension or by content).
func textual(name string, b []byte) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".txt", ".json", ".yaml", ".yml", ".csv", ".go", ".ts", ".js", ".html", ".css", ".svg", ".xml", ".toml", "":
	default:
		return false
	}
	if len(b) > 4<<20 || !utf8.Valid(b) {
		return false
	}
	return !strings.ContainsRune(string(b), 0)
}

// previewArtifact downloads an approval's artifact: text is previewed here, anything else opened.
func (m *Model) previewArtifact(ap approvals.Approval) tea.Cmd {
	a := ap.Artifact
	if a == nil {
		m.setToast("this approval is not about an artifact", true)
		return nil
	}
	if a.SHA256 == "" {
		// An external reference (git:, https:): only a web URL can be opened.
		if strings.HasPrefix(a.URI, "https://") || strings.HasPrefix(a.URI, "http://") {
			return m.openExternal(a.URI, a.ID+" "+a.Version)
		}
		m.setToast("artifact "+a.ID+" "+a.Version+" is an external reference without content in Factory: "+a.URI, true)
		return nil
	}
	name := a.Name
	if name == "" {
		name = a.ID + "-" + a.Version
	}
	m.preview = &previewState{title: a.ID + " " + a.Version + " · " + name, art: ap, loading: true, name: name}
	c, ctx, id := m.c, m.ctx, ap.ID
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		b, err := c.ArtifactContent(rctx, ap.ProjectID, a.ID, a.Version)
		return previewMsg{id: id, data: b, err: err}
	}
}

func (m *Model) onPreview(msg previewMsg) tea.Cmd {
	p := m.preview
	if p == nil || p.art.ID != msg.id {
		return nil
	}
	p.loading, p.err, p.raw = false, msg.err, msg.data
	if msg.err != nil {
		return nil
	}
	if !textual(p.name, msg.data) {
		m.preview = nil
		return m.openPreviewFile(p)
	}
	return nil
}

// openPreviewFile writes the content to a private temp file and opens it with the system viewer.
func (m *Model) openPreviewFile(p *previewState) tea.Cmd {
	dir, err := os.MkdirTemp("", "factory-artifact-")
	if err == nil {
		path := filepath.Join(dir, filepath.Base(p.name))
		if err = os.WriteFile(path, p.raw, 0o600); err == nil {
			m.setToast("↗ opening "+path, false)
			return m.openExternal(path, p.name)
		}
	}
	m.setToast("✗ "+err.Error(), true)
	return nil
}

func (p *previewState) key(m *Model, key string) tea.Cmd {
	switch key {
	case "esc", "q", "v":
		m.preview = nil
	case "j", "down":
		p.vp.ScrollDown(1)
	case "k", "up":
		p.vp.ScrollUp(1)
	case "pgdown", "space":
		p.vp.PageDown()
	case "pgup":
		p.vp.PageUp()
	case "g", "home":
		p.vp.GotoTop()
	case "G", "end":
		p.vp.GotoBottom()
	case "o":
		if len(p.raw) > 0 {
			return m.openPreviewFile(p)
		}
	case "y", "n":
		// Decide from the preview: the same reason input as in the inbox.
		m.preview = nil
		return m.inbox.key(m, key)
	}
	return nil
}

func (m *Model) previewLines(w, h int) []string {
	p := m.preview
	mw, mh := min(w-4, 110), h-1
	inner := mw - 4
	vh := max(mh-4, 1)
	if !p.ready {
		p.vp = viewport.New(viewport.WithWidth(inner), viewport.WithHeight(vh))
		p.ready = true
	}
	var lines []string
	switch {
	case p.loading:
		lines = []string{"", m.spin.View() + " downloading " + p.name + "…"}
	case p.err != nil:
		lines = []string{"", stRed.Render("✗ " + p.err.Error())}
	default:
		if p.text == nil || p.vp.Width() != inner {
			if strings.HasSuffix(strings.ToLower(p.name), ".md") || strings.HasSuffix(strings.ToLower(p.name), ".markdown") {
				p.text = renderMarkdown(string(p.raw), inner)
			} else {
				p.text = wrap(string(p.raw), inner)
			}
			p.vp.SetWidth(inner)
			p.vp.SetContentLines(p.text)
		}
		p.vp.SetHeight(vh)
		lines = strings.Split(p.vp.View(), "\n")
	}
	for len(lines) < vh {
		lines = append(lines, "")
	}
	pct := ""
	if p.text != nil {
		pct = stFaint.Render(fmt.Sprintf("%d lines · %3.0f%%", len(p.text), p.vp.ScrollPercent()*100))
	}
	hint := stAccent.Render("j/k") + stFaint.Render(" scroll ") + stAccent.Render("o") + stFaint.Render(" open in viewer ") +
		stAccent.Render("y") + stFaint.Render(" approve ") + stAccent.Render("n") + stFaint.Render(" reject ") + stAccent.Render("esc") + stFaint.Render(" close")
	lines = append(lines[:vh], rule(inner), rightAlign(hint, pct, inner))
	return frame(stMagenta.Bold(true).Render("Preview · ")+stBold.Render(p.title), lines, mw, mh, theme.magenta, false, false)
}

// renderMarkdown is a small markdown renderer for previews: headings, emphasis, lists, quotes, code blocks
// and rules styled; everything else wrapped as text.
func renderMarkdown(src string, w int) []string {
	out := []string{}
	code := false
	for _, line := range strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "```") {
			code = !code
			if code {
				lang := strings.TrimPrefix(trim, "```")
				out = append(out, stDim.Render("┌─ "+lang))
			} else {
				out = append(out, stDim.Render("└─"))
			}
			continue
		}
		if code {
			out = append(out, stDim.Render("│ ")+stCode.Render(cut(line, w-2)))
			continue
		}
		switch {
		case trim == "":
			out = append(out, "")
		case strings.HasPrefix(trim, "#"):
			level := len(trim) - len(strings.TrimLeft(trim, "#"))
			text := strings.TrimSpace(trim[level:])
			style := stTitle
			if level == 1 {
				style = lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true)
				text = " " + text + " "
			} else if level > 2 {
				style = stBold
			}
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "")
			}
			out = append(out, style.Render(text))
		case trim == "---" || trim == "***" || trim == "___":
			out = append(out, rule(w))
		case strings.HasPrefix(trim, "> "):
			for _, l := range wrap(trim[2:], w-2) {
				out = append(out, stDim.Render("▌ ")+stFaint.Italic(true).Render(inlineMD(l)))
			}
		case strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") || strings.HasPrefix(trim, "+ "):
			indent := strings.Repeat(" ", (len(line)-len(strings.TrimLeft(line, " ")))/2*2)
			text := strings.TrimSpace(trim[2:])
			mark := stAccent.Render("• ")
			if strings.HasPrefix(text, "[ ] ") || strings.HasPrefix(text, "[x] ") {
				mark = stAccent.Render(map[bool]string{true: "☑ ", false: "☐ "}[text[1] == 'x'])
				text = text[4:]
			}
			for i, l := range wrap(text, w-len(indent)-2) {
				if i == 0 {
					out = append(out, indent+mark+inlineMD(l))
				} else {
					out = append(out, indent+"  "+inlineMD(l))
				}
			}
		default:
			if n := orderedPrefix(trim); n > 0 {
				for i, l := range wrap(trim[n:], w-4) {
					if i == 0 {
						out = append(out, stAccent.Render(fit(trim[:n], 4))+inlineMD(l))
					} else {
						out = append(out, "    "+inlineMD(l))
					}
				}
				continue
			}
			for _, l := range wrap(trim, w) {
				out = append(out, inlineMD(l))
			}
		}
	}
	return out
}

func orderedPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i+1 < len(s) && s[i] == '.' && s[i+1] == ' ' {
		return i + 2
	}
	return 0
}

var (
	mdBold = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdCode = regexp.MustCompile("`([^`]+)`")
	mdLink = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
)

// inlineMD styles **bold**, `code` and [links](url) of one line.
func inlineMD(s string) string {
	s = mdLink.ReplaceAllStringFunc(s, func(x string) string {
		mm := mdLink.FindStringSubmatch(x)
		return stBlue.Underline(true).Render(mm[1]) + stDim.Render(" ("+mm[2]+")")
	})
	s = mdBold.ReplaceAllStringFunc(s, func(x string) string { return stBold.Render(mdBold.FindStringSubmatch(x)[1]) })
	return mdCode.ReplaceAllStringFunc(s, func(x string) string { return stCode.Render(mdCode.FindStringSubmatch(x)[1]) })
}
