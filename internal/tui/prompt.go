package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/messaging"
)

// The prompt bar: the oldest thing waiting on the owner, answerable from any screen (Y/N for an approval,
// A to answer a question).

func (m *Model) promptItem() (messaging.Message, bool) {
	if len(m.ov.Inbox) == 0 {
		return messaging.Message{}, false
	}
	oldest := m.ov.Inbox[0]
	for _, msg := range m.ov.Inbox[1:] {
		if msg.CreatedAt < oldest.CreatedAt {
			oldest = msg
		}
	}
	return oldest, true
}

func (m *Model) promptLine(w int) string {
	msg, ok := m.promptItem()
	if !ok || !m.loaded {
		return ""
	}
	bar := lipgloss.NewStyle().Background(theme.yellow).Foreground(theme.onAccent)
	at := time.Unix(msg.CreatedAt, 0).In(m.opts.Location).Format("15:04:05")
	var text, keys string
	if ap, isApproval := m.approvalOf(msg); isApproval || msg.ApprovalID != "" {
		text = fmt.Sprintf("%s wants %s %s — approve?", msg.Sender, ap.Call.Name, brief(ap.Call.Arguments))
		keys = "[Y/N]"
	} else if !answerable(msg) {
		// A notification or report: it takes no answer (POST answer would be refused).
		text = msg.Sender + " says: " + oneLine(msg.Body)
		keys = "[R reply · M read]"
	} else {
		verb := "asks"
		if msg.EscalationReason != "" {
			verb = "escalates"
		}
		text = msg.Sender + " " + verb + ": " + oneLine(msg.Body)
		keys = "[A answer]"
		if opts := choices(msg); len(opts) > 0 {
			keys = "[A " + strings.Join(opts, " / ") + "]"
		}
	}
	if w-lipgloss.Width(keys) < 70 && strings.HasPrefix(keys, "[A ") {
		keys = "[A answer]"
	}
	more := ""
	if n := len(m.ov.Inbox) - 1; n > 0 {
		more = fmt.Sprintf("  +%d in inbox", n)
	}
	right := bar.Bold(true).Render(keys) + bar.Render(more+" ")
	left := bar.Bold(true).Render(" → "+at+" ") + bar.Render(text)
	room := w - lipgloss.Width(right) - 1
	return bar.Render(fit(cut(left, room), room)) + bar.Render(" ") + right
}

// promptKey handles Y, N and A for the prompt bar; ok is false when the key is not for it.
func (m *Model) promptKey(key string) (tea.Cmd, bool) {
	msg, ok := m.promptItem()
	if !ok {
		return nil, false
	}
	ap, isApproval := m.approvalOf(msg)
	isApproval = isApproval || msg.ApprovalID != ""
	switch {
	case isApproval && (key == "Y" || key == "N"):
		kind, initial := "approve", "approved by owner"
		if key == "N" {
			kind, initial = "reject", ""
		}
		version := max(ap.Version, 1)
		return m.openInput(capitalize(kind)+" "+ap.Call.Name+" for "+msg.Sender+" — reason", initial, Action{Kind: kind, ApprovalID: msg.ApprovalID, Version: version}, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("a reason is required")
			}
			a.Text = text
			return nil
		}), true
	case !isApproval && !answerable(msg) && key == "R":
		return m.replyTo(msg), true
	case !isApproval && !answerable(msg) && key == "M":
		return m.markRead(msg), true
	case !isApproval && !answerable(msg) && key == "A":
		m.setToast("a "+msg.MessageType+" takes no answer: R replies, M marks it read", true)
		return nil, true
	case !isApproval && key == "A":
		return m.answer(msg), true
	}
	return nil, false
}

// answer asks for an answer to a question: a chooser of its options (and free text), or free text.
func (m *Model) answer(msg messaging.Message) tea.Cmd {
	free := func() tea.Cmd {
		return m.openInput("Answer "+msg.Sender+": "+oneLine(msg.Body), "", Action{Kind: "answer", MessageID: msg.ID, Version: msg.Version}, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("empty answer")
			}
			a.Text = text
			return nil
		})
	}
	opts := choices(msg)
	if len(opts) == 0 {
		return free()
	}
	q := msg.Questions[0]
	m.chooser = &chooserState{title: q.Question, note: msg.Sender + ": " + oneLine(msg.Body), options: append(append([]string{}, opts...), "✎ answer in free text…"),
		pick: func(i int) tea.Cmd {
			if i == len(opts) {
				return free()
			}
			m.askConfirm(Action{Kind: "answer", MessageID: msg.ID, Version: msg.Version, Text: opts[i], Answers: map[string]any{q.ID: opts[i]}})
			return nil
		}}
	return nil
}

// chooserState is a modal list to pick one option from.
type chooserState struct {
	title, note string
	options     []string
	cursor      int
	pick        func(i int) tea.Cmd
}

func (c *chooserState) key(m *Model, key string) tea.Cmd {
	switch key {
	case "esc", "q":
		m.chooser = nil
	case "j", "down":
		c.cursor = min(c.cursor+1, len(c.options)-1)
	case "k", "up":
		c.cursor = max(c.cursor-1, 0)
	case "enter", "space":
		m.chooser = nil
		return c.pick(c.cursor)
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			if i := int(key[0] - '1'); i < len(c.options) {
				m.chooser = nil
				return c.pick(i)
			}
		}
	}
	return nil
}

func (m *Model) chooserLines(w int) []string {
	c := m.chooser
	lines := []string{}
	for _, l := range wrap(c.note, w-4) {
		lines = append(lines, stFaint.Render(l))
	}
	lines = append(lines, "")
	for i, o := range c.options {
		label := fmt.Sprintf(" %d  %s ", i+1, o)
		if i == c.cursor {
			label = lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(fit("▸"+label, w-4))
		} else {
			label = " " + label
		}
		lines = append(lines, label)
	}
	return frame(stTitle.Render(c.title), lines, w, len(lines)+2, theme.accent, true, false)
}
