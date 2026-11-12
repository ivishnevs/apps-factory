package tui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/approvals"
)

// Artifact approvals in the inbox (artifact-choice-approvals, one approval model): the options as a list (label,
// note, recommendation), the options the delegate dropped collapsed below with its reasons and still choosable; ←/→
// focus an option, v previews it, enter chooses it, c asks for changes, n rejects all. One option is the same card
// shown compactly: the version, v previews it, y (or enter) approves it, c asks for changes, n rejects it.

// choiceOrder is the order the options show in: the kept ones, then the dropped ones (option numbers from 1).
func choiceOrder(ap approvals.Approval) []int {
	var kept, dropped []int
	for i, o := range ap.Options {
		if o.Dropped != "" {
			dropped = append(dropped, i+1)
		} else {
			kept = append(kept, i+1)
		}
	}
	return append(kept, dropped...)
}

// focusedOption is the option number the inbox cursor is on.
func (s *inboxState) focusedOption(ap approvals.Approval) int {
	order := choiceOrder(ap)
	return order[min(s.option, len(order)-1)]
}

// choiceKey handles a key on a choice approval; ok is false for keys it leaves to the inbox.
func (s *inboxState) choiceKey(m *Model, ap approvals.Approval, key string) (tea.Cmd, bool) {
	n := s.focusedOption(ap)
	name := ap.OptionName(n)
	base := Action{ApprovalID: ap.ID, Version: max(ap.Version, 1)}
	if ap.Single() {
		return s.singleKey(m, ap, key, base)
	}
	switch key {
	case "h", "left":
		s.option = max(s.option-1, 0)
	case "l", "right":
		s.option = min(s.option+1, len(ap.Options)-1)
	case "v":
		o := ap.Options[n-1].Artifact
		return m.previewVersion(ap.ProjectID, o, fmt.Sprintf("%s\x00%d", ap.ID, n), false), true
	case "enter":
		a := base
		a.Kind, a.Option = "choose", n
		return m.openInput("Choose "+name+" — comment (optional)", "", a, func(a *Action, text string) error {
			a.Text = text
			return nil
		}), true
	case "c":
		a := base
		a.Kind = "changes"
		return m.openInput("Ask for changes on "+ap.ID+" — what to change", "", a, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("say what to change")
			}
			a.Text = text
			return nil
		}), true
	case "n":
		a := base
		a.Kind = "reject"
		return m.openInput("Reject all options of "+ap.ID+" — reason", "", a, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("a reason is required")
			}
			a.Text = text
			return nil
		}), true
	case "y", "a":
		m.setToast("a choice: ←/→ pick an option, enter chooses it, c asks for changes, n rejects all", true)
		return nil, true
	default:
		return nil, false
	}
	return nil, true
}

// singleKey handles a key on a one-option approval: approving it is choosing its only option.
func (s *inboxState) singleKey(m *Model, ap approvals.Approval, key string, base Action) (tea.Cmd, bool) {
	name := ap.OptionName(1)
	switch key {
	case "h", "left", "l", "right":
	case "v":
		return m.previewArtifact(ap), true
	case "y", "a", "enter":
		a := base
		a.Kind = "approve"
		return m.openInput("Approve "+name+" — comment (optional)", "", a, func(a *Action, text string) error {
			a.Text = text
			return nil
		}), true
	case "c":
		a := base
		a.Kind = "changes"
		return m.openInput("Ask for changes on "+name+" — what to change", "", a, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("say what to change")
			}
			a.Text = text
			return nil
		}), true
	case "n":
		a := base
		a.Kind = "reject"
		return m.openInput("Reject "+name+" — reason", "", a, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("a reason is required")
			}
			a.Text = text
			return nil
		}), true
	default:
		return nil, false
	}
	return nil, true
}

// choiceLines renders an artifact approval's options and keys; one option compactly.
func (m *Model) choiceLines(ap approvals.Approval, w int) []string {
	if ap.Single() {
		return m.singleLines(ap, w)
	}
	s := &m.inbox
	focus := s.focusedOption(ap)
	lines := []string{"", stBold.Render(fmt.Sprintf("Choose one of %d versions", len(ap.Options)))}
	droppedHead := false
	for _, n := range choiceOrder(ap) {
		o := ap.Options[n-1]
		if o.Dropped != "" && !droppedHead {
			lines = append(lines, stFaint.Render("dropped by "+o.DroppedBy+" (still choosable):"))
			droppedHead = true
		}
		mark := "  "
		if n == focus {
			mark = stMagenta.Bold(true).Render("▸ ")
		}
		label := fmt.Sprintf("%d. %s", n, o.Label)
		if n == focus {
			label = lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(" " + label + " ")
		} else if o.Dropped != "" {
			label = stFaint.Render(label)
		} else {
			label = stBold.Render(label)
		}
		line := mark + label + stFaint.Render("  "+o.Artifact.ID+" "+o.Artifact.Version)
		if ap.Recommended == n {
			line += "  " + stGreen.Render("★ recommended")
		}
		lines = append(lines, line)
		if o.Dropped != "" {
			lines = append(lines, "    "+stFaint.Render(cut("✕ "+o.Dropped, w-6)))
		} else if o.Note != "" {
			lines = append(lines, "    "+stFaint.Render(cut(o.Note, w-6)))
		}
	}
	if ap.Reason != "" {
		lines = append(lines, "", stFaint.Render("reason   ")+ap.Reason)
	}
	lines = append(lines, stFaint.Render("expires  ")+"in "+until(m.now(), time.Unix(ap.ExpiresAt, 0)), "")
	key := func(bg color.Color, s string) string {
		return lipgloss.NewStyle().Background(bg).Foreground(theme.onAccent).Bold(true).Render(s)
	}
	lines = append(lines, strings.Join([]string{key(theme.magenta, " ←/→ option "), key(theme.magenta, " v  preview "), key(theme.green, " enter  choose "), key(theme.yellow, " c  changes "), key(theme.red, " n  reject all ")}, "  "))
	return lines
}

// singleLines renders a one-option approval compactly: the version, its note, the reason and Approve / Ask for
// changes / Reject.
func (m *Model) singleLines(ap approvals.Approval, w int) []string {
	o := ap.Options[0]
	a := o.Artifact
	head := stFaint.Render("artifact ") + stMagenta.Bold(true).Render(a.ID) + " " + stBold.Render(a.Version) + stFaint.Render(" · "+dash(a.Name))
	if o.Label != "" {
		head += "  " + stBold.Render(o.Label)
	}
	lines := []string{"", head}
	if a.Size > 0 {
		lines = append(lines, stFaint.Render("content  ")+fmt.Sprintf("%s · sha256 %s", byteSize(a.Size), clip(a.SHA256, 12)))
	} else {
		lines = append(lines, stFaint.Render("uri      ")+a.URI)
	}
	if o.Note != "" {
		lines = append(lines, stFaint.Render("note     ")+cut(o.Note, w-11))
	}
	if ap.Reason != "" {
		lines = append(lines, stFaint.Render("reason   ")+ap.Reason)
	}
	lines = append(lines, stFaint.Render("expires  ")+"in "+until(m.now(), time.Unix(ap.ExpiresAt, 0)), "")
	key := func(bg color.Color, s string) string {
		return lipgloss.NewStyle().Background(bg).Foreground(theme.onAccent).Bold(true).Render(s)
	}
	lines = append(lines, strings.Join([]string{key(theme.magenta, " v  preview "), key(theme.green, " y  approve "), key(theme.yellow, " c  changes "), key(theme.red, " n  reject ")}, "  "))
	return lines
}
