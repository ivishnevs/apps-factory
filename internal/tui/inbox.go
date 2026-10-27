package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/approvals"
	"appfactory/internal/messaging"
)

// inboxState is the owner's inbox: questions, escalations and approvals, answered in place.
type inboxState struct {
	cursor int
	option int
}

func (s *inboxState) clamp(m *Model) {
	if s.cursor >= len(m.ov.Inbox) {
		s.cursor = max(len(m.ov.Inbox)-1, 0)
	}
}

func (m *Model) approvalOf(msg messaging.Message) (approvals.Approval, bool) {
	for _, a := range m.ov.Approvals {
		if a.ID == msg.ApprovalID || msg.ApprovalID == "" && a.MessageID == msg.ID {
			return a, true
		}
	}
	return approvals.Approval{}, false
}

// choices are the options of a one-question form (an escalation, an ask with options); a form with more
// questions is answered in free text.
func choices(msg messaging.Message) []string {
	if len(msg.Questions) == 1 && msg.Questions[0].Type == "single_choice" {
		return msg.Questions[0].Options
	}
	return nil
}

// answerable: the item is a question (or an escalation) that takes an answer; notifications and reports
// are replied to in a conversation and marked read instead.
func answerable(msg messaging.Message) bool {
	return msg.MessageType == "question" && msg.ApprovalID == "" && (msg.Status == "PENDING" || msg.Status == "ESCALATED")
}

func (m *Model) inboxSelected() (messaging.Message, bool) {
	if len(m.ov.Inbox) == 0 {
		return messaging.Message{}, false
	}
	m.inbox.clamp(m)
	return m.ov.Inbox[m.inbox.cursor], true
}

// replyTo answers a notification: a direct report in the Chat tab (its conversation, input focused); any
// other agent with a directive on its task.
func (m *Model) replyTo(msg messaging.Message) tea.Cmd {
	for i, r := range m.chatReports() {
		if r != msg.Sender {
			continue
		}
		if msg.ProjectID != "" {
			m.project = msg.ProjectID
		}
		m.chat.init(m)
		m.chat.sel, m.chat.scroll, m.chat.typing = i, 0, true
		m.switchTab(tabChat)
		m.markSeen(ownerConversation(r, m.chatProject()))
		return m.chat.ti.Focus()
	}
	if t, ok := m.taskByID(msg.TaskID); ok && t.Kind == "" {
		return m.taskAction(t, "d")
	}
	m.setToast(msg.Sender+" is not your direct report and the message has no task to direct: answer in its conversation", true)
	return nil
}

// markRead marks a notification read (POST /inbox/{id}/read); it leaves the inbox.
func (m *Model) markRead(msg messaging.Message) tea.Cmd {
	a := Action{Kind: "read", MessageID: msg.ID, Version: msg.Version}
	m.setToast("… "+a.Describe(), false)
	return m.send(a)
}

func (s *inboxState) key(m *Model, key string) tea.Cmd {
	items := m.ov.Inbox
	if len(items) == 0 {
		return nil
	}
	s.clamp(m)
	msg := items[s.cursor]
	if !answerable(msg) && msg.ApprovalID == "" {
		switch key {
		case "j", "down", "k", "up", "t":
		case "r", "enter":
			return m.replyTo(msg)
		case "m":
			return m.markRead(msg)
		case "a", "y", "n":
			m.setToast("a "+msg.MessageType+" takes no answer: r replies, m marks it read", true)
			return nil
		default:
			return nil
		}
	}
	opts := choices(msg)
	ap, isApproval := m.approvalOf(msg)
	if msg.ApprovalID != "" {
		isApproval = true
	}
	switch key {
	case "j", "down":
		s.cursor, s.option = min(s.cursor+1, len(items)-1), 0
	case "k", "up":
		s.cursor, s.option = max(s.cursor-1, 0), 0
	case "h", "left":
		s.option = max(s.option-1, 0)
	case "l", "right":
		s.option = min(s.option+1, max(len(opts)-1, 0))
	case "enter":
		if isApproval {
			return nil
		}
		if len(opts) == 0 {
			return s.key(m, "a")
		}
		opt := opts[min(s.option, len(opts)-1)]
		m.askConfirm(Action{Kind: "answer", MessageID: msg.ID, Version: msg.Version, Text: opt, Answers: map[string]any{msg.Questions[0].ID: opt}})
	case "a":
		if isApproval {
			m.setToast("approvals are decided with y (approve) or n (reject)", true)
			return nil
		}
		return m.openInput("Answer "+msg.Sender+" (free text)", "", Action{Kind: "answer", MessageID: msg.ID, Version: msg.Version}, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("empty answer")
			}
			a.Text = text
			return nil
		})
	case "y", "n":
		if !isApproval {
			return nil
		}
		kind, initial := "approve", "approved by owner"
		if key == "n" {
			kind, initial = "reject", ""
		}
		return m.openInput(capitalize(kind)+" "+msg.ApprovalID+" — reason", initial, Action{Kind: kind, ApprovalID: msg.ApprovalID, Version: max(ap.Version, 1)}, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("a reason is required")
			}
			a.Text = text
			return nil
		})
	case "t":
		if msg.TaskID != "" {
			return m.openDetail(msg.TaskID)
		}
	case "v":
		if isApproval && ap.Artifact != nil {
			return m.previewArtifact(ap)
		}
		m.setToast("v previews an artifact approval", true)
	}
	return nil
}

func inboxKind(msg messaging.Message) (string, lipgloss.Style) {
	switch {
	case msg.ApprovalID != "":
		return "approval", stMagenta
	case msg.EscalationReason != "":
		return "escalation", stRedBold
	case msg.Status == "ESCALATED":
		return "escalated", stOrange
	}
	return msg.MessageType, stYellow
}

func (m *Model) inboxLines(w, h int) []string {
	if !m.loaded {
		return m.notLoaded()
	}
	s := &m.inbox
	items := m.ov.Inbox
	now := m.now()
	if len(items) == 0 {
		return panel("Inbox", []string{"", stGreen.Render("  ✓ nothing waits on you")}, w, h, true)
	}
	s.clamp(m)
	listW := w * 2 / 5
	if w < 100 {
		listW = w
	}
	list := []string{}
	for i, msg := range items {
		kind, ks := inboxKind(msg)
		head := ks.Render("● "+kind) + "  " + lipgloss.NewStyle().Foreground(agentColor(msg.Sender)).Render(msg.Sender)
		head = rightAlign(head, stFaint.Render(ago(now, time.Unix(msg.CreatedAt, 0))+" ago"), listW-4)
		body := "  " + stFaint.Render(cut(dash(msg.TaskID)+" · "+oneLine(msg.Body), listW-6))
		if i == s.cursor {
			bg := lipgloss.NewStyle().Background(theme.surfaceHi)
			head, body = bg.Render(fit(head, listW-4)), bg.Render(fit(body, listW-4))
		}
		list = append(list, head, body, "")
	}
	listPanel := panel(fmt.Sprintf("Inbox · %d", len(items)), list, listW, h, true)
	if w < 100 {
		// Narrow: the list on top, the selected item below.
		top := min(len(items)*3+2, h/2)
		return append(panel(fmt.Sprintf("Inbox · %d", len(items)), list, w, top, true), panel("Item", m.inboxItemLines(items[s.cursor], w-4), w, h-top, false)...)
	}
	return hjoin(0, listPanel, panel("Item", m.inboxItemLines(items[s.cursor], w-listW-4), w-listW, h, false))
}

func (m *Model) inboxItemLines(msg messaging.Message, w int) []string {
	s := &m.inbox
	now := m.now()
	kind, ks := inboxKind(msg)
	lines := []string{ks.Render("● "+strings.ToUpper(kind)) + "  " + stFaint.Render("from ") + agentChip(msg.Sender) + stFaint.Render("  to ") + msg.Recipient + "  " + projectChip(msg.ProjectID) + stFaint.Render("  "+dash(msg.TaskID))}
	if msg.Deadline > 0 {
		lines = append(lines, stFaint.Render("answer due in "+until(now, time.Unix(msg.Deadline, 0))))
	}
	if msg.MayBeOutdated {
		lines = append(lines, stYellow.Render("◐ the task changed since this was asked: it may be outdated"))
	}
	lines = append(lines, "")
	for _, l := range wrap(msg.Body, w-2) {
		lines = append(lines, stBold.Render(l))
	}
	if ap, ok := m.approvalOf(msg); ok && ap.Artifact != nil {
		a := ap.Artifact
		lines = append(lines, "", stFaint.Render("artifact ")+stMagenta.Bold(true).Render(a.ID)+" "+stBold.Render(a.Version)+stFaint.Render(" · "+dash(a.Name)))
		if a.Size > 0 {
			lines = append(lines, stFaint.Render("content  ")+fmt.Sprintf("%s · sha256 %s", byteSize(a.Size), clip(a.SHA256, 12)))
		} else {
			lines = append(lines, stFaint.Render("uri      ")+a.URI)
		}
		if ap.Reason != "" {
			lines = append(lines, stFaint.Render("reason   ")+ap.Reason)
		}
		lines = append(lines, stFaint.Render("expires  ")+"in "+until(now, time.Unix(ap.ExpiresAt, 0)), "")
		view := lipgloss.NewStyle().Background(theme.magenta).Foreground(theme.onAccent).Bold(true).Render(" v  preview ")
		yes := lipgloss.NewStyle().Background(theme.green).Foreground(theme.onAccent).Bold(true).Render(" y  approve ")
		no := lipgloss.NewStyle().Background(theme.red).Foreground(theme.onAccent).Bold(true).Render(" n  reject ")
		lines = append(lines, view+"  "+yes+"  "+no)
	} else if ap, ok := m.approvalOf(msg); ok {
		lines = append(lines, "", stFaint.Render("call     ")+stMagenta.Bold(true).Render(ap.Call.Name))
		if ap.Call.Name == "factory_project_create" {
			// The project the agent asks to create, field by field (project-creation D6).
			for _, kv := range projectCreateArgs(ap.Call.Arguments) {
				for i, l := range wrap(kv[1], max(w-11, 20)) {
					k := ""
					if i == 0 {
						k = kv[0]
					}
					st := lipgloss.NewStyle()
					if kv[0] == "project" {
						st = stBold
					}
					lines = append(lines, stFaint.Render(fmt.Sprintf("%-9s", k))+st.Render(l))
				}
			}
		} else {
			lines = append(lines, stFaint.Render("args     ")+brief(ap.Call.Arguments))
		}
		if ap.Reason != "" {
			lines = append(lines, stFaint.Render("reason   ")+ap.Reason)
		}
		lines = append(lines, stFaint.Render("expires  ")+"in "+until(now, time.Unix(ap.ExpiresAt, 0)), "")
		yes := lipgloss.NewStyle().Background(theme.green).Foreground(theme.onAccent).Bold(true).Render(" y  approve ")
		no := lipgloss.NewStyle().Background(theme.red).Foreground(theme.onAccent).Bold(true).Render(" n  reject ")
		lines = append(lines, yes+"  "+no)
	}
	for _, q := range msg.Questions {
		lines = append(lines, "", stTitle.Render("? ")+stBold.Render(q.Question)+stFaint.Render("  "+q.Type))
		if len(msg.Questions) == 1 && q.Type == "single_choice" {
			opts := []string{}
			for i, o := range q.Options {
				label := " " + o + " "
				if i == s.option {
					label = lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(label)
				} else {
					label = stChip.Render(label)
				}
				opts = append(opts, label)
			}
			lines = append(lines, "  "+strings.Join(opts, " "), stFaint.Render("  ←/→ choose · enter answer · a free text"))
		} else if len(q.Options) > 0 {
			lines = append(lines, "  options: "+strings.Join(q.Options, ", ")+stFaint.Render(" (answer in free text: a)"))
		}
	}
	switch {
	case !answerable(msg) && msg.ApprovalID == "":
		how := "r  reply in the chat"
		if !slicesContains(m.chatReports(), msg.Sender) {
			how = "r  reply with a directive on " + dash(msg.TaskID)
		}
		lines = append(lines, "", stFaint.Render(how+"   m  mark read"))
	case len(msg.Questions) == 0 && msg.ApprovalID == "":
		lines = append(lines, "", stFaint.Render("a  answer in free text"))
	}
	return lines
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// byteSize is a size in B, KiB or MiB.
func byteSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
