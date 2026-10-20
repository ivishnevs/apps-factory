package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Colors are the 16 ANSI colors only: the terminal's theme maps them, so they stay readable on dark and
// light backgrounds alike. Selection is reverse video, secondary text faint.
var (
	stBold      = lipgloss.NewStyle().Bold(true)
	stFaint     = lipgloss.NewStyle().Faint(true)
	stSelected  = lipgloss.NewStyle().Reverse(true)
	stTitle     = lipgloss.NewStyle().Bold(true).Underline(true)
	stTabActive = lipgloss.NewStyle().Bold(true).Reverse(true)
	stTab       = lipgloss.NewStyle()
	stRed       = lipgloss.NewStyle().Foreground(lipgloss.Red)
	stRedBold   = lipgloss.NewStyle().Foreground(lipgloss.Red).Bold(true)
	stGreen     = lipgloss.NewStyle().Foreground(lipgloss.Green)
	stYellow    = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	stBlue      = lipgloss.NewStyle().Foreground(lipgloss.Blue)
	stMagenta   = lipgloss.NewStyle().Foreground(lipgloss.Magenta)
	stCyan      = lipgloss.NewStyle().Foreground(lipgloss.Cyan)
	stBar       = lipgloss.NewStyle().Reverse(true)
	stBarAlert  = lipgloss.NewStyle().Reverse(true).Foreground(lipgloss.Red).Bold(true)
)

// statusStyle colors a task or run status.
func statusStyle(s string) lipgloss.Style {
	switch s {
	case "IN_PROGRESS", "RUNNING", "CLAIMED", "running":
		return stGreen
	case "IN_REVIEW", "queued", "QUEUED":
		return stCyan
	case "READY", "idle":
		return stBlue
	case "BLOCKED", "paused", "CANCELLING", "STOPPED":
		return stYellow
	case "DONE", "ENDED", "CANCELLED", "BACKLOG":
		return stFaint
	}
	return lipgloss.NewStyle()
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
		return stGreen
	case lineRefusal, lineError:
		return stRedBold
	case lineEnd:
		return stYellow
	case lineCore, lineInfo, lineTool:
		return stFaint
	}
	return lipgloss.NewStyle()
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
		out = append(out, strings.Split(ansi.Wordwrap(para, w, ""), "\n")...)
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

// col is a table cell: s cut to w-1 cells, then a space, so columns never touch.
func col(s string, w int) string { return fit(s, w-1) + " " }

// until is a short duration to t: 23h, 5m; overdue once t passed.
func until(now, t time.Time) string {
	if !t.After(now) {
		return "overdue"
	}
	return ago(now, now.Add(-t.Sub(now)))
}
func ansiWidth(s string) int { return ansi.StringWidth(s) }
