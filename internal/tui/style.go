package tui

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The theme: one palette with a light and a dark variant of every color, picked when the terminal reports its
// background (tea.BackgroundColorMsg; dark until then). Bubble Tea downsamples the colors to what the
// terminal supports.
type palette struct {
	text, dim, faint, accent, surface, surfaceHi, onAccent color.Color
	green, yellow, red, blue, cyan, magenta, orange        color.Color
	agents                                                 []color.Color
}

var (
	theme  palette
	isDark = true

	stBold, stFaint, stDim, stSelected, stTitle, stAccent          lipgloss.Style
	stRed, stRedBold, stGreen, stYellow, stBlue, stMagenta, stCyan lipgloss.Style
	stOrange, stCode, stBar, stBarAlert, stChip                    lipgloss.Style
)

func init() { applyTheme(true) }

// applyTheme builds the styles for a dark or a light background.
func applyTheme(dark bool) {
	isDark = dark
	ld := lipgloss.LightDark(dark)
	c := func(light, dark string) color.Color { return ld(lipgloss.Color(light), lipgloss.Color(dark)) }
	theme = palette{
		text: c("#1F2937", "#E5E7EB"), dim: c("#6B7280", "#9CA3AF"), faint: c("#9CA3AF", "#4B5563"),
		accent: c("#6D28D9", "#A78BFA"), surface: c("#F3F4F6", "#1F2937"), surfaceHi: c("#E5E7EB", "#374151"),
		onAccent: c("#FFFFFF", "#111827"),
		green:    c("#047857", "#34D399"), yellow: c("#B45309", "#FBBF24"), red: c("#B91C1C", "#F87171"),
		blue: c("#1D4ED8", "#60A5FA"), cyan: c("#0E7490", "#22D3EE"), magenta: c("#BE185D", "#F472B6"),
		orange: c("#C2410C", "#FB923C"),
	}
	for _, p := range [][2]string{{"#7C3AED", "#C4B5FD"}, {"#0369A1", "#7DD3FC"}, {"#B45309", "#FCD34D"}, {"#047857", "#6EE7B7"},
		{"#BE185D", "#F9A8D4"}, {"#C2410C", "#FDBA74"}, {"#4338CA", "#A5B4FC"}, {"#0F766E", "#5EEAD4"}, {"#A21CAF", "#F0ABFC"}, {"#4D7C0F", "#BEF264"}} {
		theme.agents = append(theme.agents, c(p[0], p[1]))
	}
	fg := func(col color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(col) }
	stBold = fg(theme.text).Bold(true)
	stFaint = fg(theme.dim)
	stDim = fg(theme.faint)
	stSelected = lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true)
	stTitle = fg(theme.accent).Bold(true)
	stAccent = fg(theme.accent)
	stRed, stRedBold = fg(theme.red), fg(theme.red).Bold(true)
	stGreen, stYellow, stBlue = fg(theme.green), fg(theme.yellow), fg(theme.blue)
	stMagenta, stCyan, stOrange = fg(theme.magenta), fg(theme.cyan), fg(theme.orange)
	stCode = lipgloss.NewStyle().Background(theme.surface).Foreground(theme.cyan)
	stBar = lipgloss.NewStyle().Background(theme.surface).Foreground(theme.text)
	stBarAlert = lipgloss.NewStyle().Background(theme.red).Foreground(theme.onAccent).Bold(true)
	stChip = lipgloss.NewStyle().Background(theme.surfaceHi).Foreground(theme.text)
}

// agentColor is a stable color per name (agents, projects).
func agentColor(name string) color.Color {
	if name == "owner" {
		return theme.accent
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return theme.agents[h.Sum32()%uint32(len(theme.agents))]
}

// agentChip is "● name" in the agent's color.
func agentChip(name string) string {
	if name == "" {
		return stDim.Render("○ unassigned")
	}
	return lipgloss.NewStyle().Foreground(agentColor(name)).Render("● " + name)
}

// projectChip is the project name on a tinted background.
func projectChip(p string) string {
	return lipgloss.NewStyle().Background(theme.surfaceHi).Foreground(agentColor("project:" + p)).Render(" " + p + " ")
}

// statusStyle colors a task, run or agent state.
func statusStyle(s string) lipgloss.Style {
	switch s {
	case "IN_PROGRESS", "RUNNING", "CLAIMED", "running", "working":
		return stGreen
	case "IN_REVIEW", "queued", "QUEUED":
		return stCyan
	case "READY", "idle":
		return stBlue
	case "BLOCKED", "paused", "CANCELLING", "STOPPED", "stopping":
		return stYellow
	case "DONE", "ENDED", "CANCELLED", "BACKLOG":
		return stFaint
	}
	return lipgloss.NewStyle().Foreground(theme.text)
}

// lineKindStyle colors a run activity line kind.
func lineKindStyle(kind string) lipgloss.Style {
	switch kind {
	case lineSay:
		return stBold
	case lineShell:
		return stCyan
	case lineMCP:
		return stMagenta
	case lineExit:
		return stGreen.Bold(true)
	case lineRefusal, lineError:
		return stRedBold
	case lineEnd:
		return stYellow.Bold(true)
	}
	return stFaint
}

// kindIcon is the glyph of an activity line kind.
func kindIcon(kind string) string {
	switch kind {
	case lineSay:
		return "›"
	case lineShell:
		return "▸"
	case lineMCP:
		return "⇄"
	case lineExit:
		return "⏏"
	case lineRefusal:
		return "✗"
	case lineError:
		return "⚠"
	case lineEnd:
		return "■"
	case lineCore:
		return "◇"
	case lineTool:
		return "✎"
	}
	return "·"
}

// activityText is a live activity line with its icon, as cards and agent cards show it.
func activityText(l LogLine) string {
	return kindIcon(l.Kind) + " " + l.Text
}

// fit cuts s (which may hold ANSI styles) to w cells and pads it to exactly w.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	if n := w - ansi.StringWidth(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// cut cuts s to w cells without padding.
func cut(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s
}

// block makes lines exactly w wide and h high: the screen never shifts, so nothing flickers.
func block(lines []string, w, h int) string {
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = fit(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", max(w, 0))
		}
	}
	return strings.Join(out, "\n")
}

// wrap word-wraps plain text to w cells.
func wrap(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	out := []string{}
	for _, para := range strings.Split(strings.TrimRight(s, "\n"), "\n") {
		if strings.TrimSpace(para) == "" {
			out = append(out, "")
			continue
		}
		out = append(out, strings.Split(ansi.Wrap(para, w, ""), "\n")...)
	}
	return out
}

// hjoin places blocks of lines side by side with gap spaces between them.
func hjoin(gap int, blocks ...[]string) []string {
	h := 0
	widths := make([]int, len(blocks))
	for i, b := range blocks {
		h = max(h, len(b))
		for _, l := range b {
			widths[i] = max(widths[i], ansi.StringWidth(l))
		}
	}
	out := make([]string, h)
	for r := range out {
		parts := []string{}
		for i, b := range blocks {
			l := ""
			if r < len(b) {
				l = b[r]
			}
			parts = append(parts, fit(l, widths[i]))
		}
		out[r] = strings.Join(parts, strings.Repeat(" ", gap))
	}
	return out
}

// ago is a short age: 5s, 3m, 2h, 4d.
func ago(now, t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func money(v float64) string {
	if v == 0 {
		return "-"
	}
	return fmt.Sprintf("$%.2f", v)
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func ansiStrip(s string) string { return ansi.Strip(s) }

func ansiWidth(s string) int { return ansi.StringWidth(s) }

// col is a table cell: s cut to w-1 cells, then a space, so columns never touch.
func col(s string, w int) string { return fit(s, w-1) + " " }

// until is a short duration to t: 23h, 5m; overdue once t passed.
func until(now, t time.Time) string {
	if !t.After(now) {
		return "overdue"
	}
	return ago(now, now.Add(-t.Sub(now)))
}

// rule is a horizontal line of w cells.
func rule(w int) string { return stDim.Render(strings.Repeat("─", max(w, 0))) }
