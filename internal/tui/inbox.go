package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

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

func (s *inboxState) key(m *Model, key string) tea.Cmd {
	items := m.ov.Inbox
	if len(items) == 0 {
		return nil
	}
	s.clamp(m)
	msg := items[s.cursor]
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
		if isApproval || len(opts) == 0 {
			if !isApproval {
				return s.key(m, "a")
			}
			return nil
		}
		opt := opts[min(s.option, len(opts)-1)]
		m.askConfirm(Action{Kind: "answer", MessageID: msg.ID, Version: msg.Version, Text: opt, Answers: map[string]any{msg.Questions[0].ID: opt}})
	case "a":
		if isApproval {
			m.setToast("approvals are decided with y (approve) or n (reject)", true)
			return nil
		}
		return m.openInput("Answer "+msg.ID+" (free text)", "", Action{Kind: "answer", MessageID: msg.ID, Version: msg.Version}, func(a *Action, text string) error {
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
		version := ap.Version
		if version == 0 {
			version = 1
		}
		return m.openInput(strings.ToUpper(kind[:1])+kind[1:]+" "+msg.ApprovalID+": reason", initial, Action{Kind: kind, ApprovalID: msg.ApprovalID, Version: version}, func(a *Action, text string) error {
			if text == "" {
				return fmt.Errorf("a reason is required")
			}
			a.Text = text
			return nil
		})
	case "t":
		if msg.TaskID != "" {
			m.tasks.detailID, m.tasks.loading, m.tasks.scroll, m.tasks.open, m.tasks.detail = msg.TaskID, true, 0, !m.wideTasks(), nil
			m.switchTab(tabTasks)
			return m.loadDetail(msg.TaskID)
		}
	}
	return nil
}

func (m *Model) inboxLines(w, h int) []string {
	s := &m.inbox
	items := m.ov.Inbox
	now := m.now()
	lines := []string{cut(stTitle.Render("Inbox")+stFaint.Render(fmt.Sprintf("  %d waiting on you · ←/→ option · enter answer · a free text · y approve · n reject · t task", len(items))), w)}
	if len(items) == 0 {
		return append(lines, "", stFaint.Render("  nothing waits on you"))
	}
	s.clamp(m)
	listH := min(len(items), max(h/3, 3))
	start := 0
	if s.cursor >= listH {
		start = s.cursor - listH + 1
	}
	for i := start; i < len(items) && i < start+listH; i++ {
		msg := items[i]
		kind := msg.MessageType
		switch {
		case msg.ApprovalID != "":
			kind = "approval"
		case msg.EscalationReason != "":
			kind = "escalation"
		case msg.Status == "ESCALATED":
			kind = "escalated"
		}
		line := "  " + stRedBold.Render(fit(kind, 11)) + fit(msg.Sender, 17) + fit(dash(msg.TaskID), 8) + stFaint.Render(fit(ago(now, time.Unix(msg.CreatedAt, 0)), 5)) + oneLine(msg.Body)
		if i == s.cursor {
			line = stSelected.Render(fit(stripStyles(line), w))
		}
		lines = append(lines, cut(line, w))
	}
	lines = append(lines, stFaint.Render(strings.Repeat("─", w)))
	msg := items[s.cursor]
	lines = append(lines, cut(stBold.Render(msg.ID)+fmt.Sprintf("  from %s to %s · project %s · task %s · v%d", msg.Sender, msg.Recipient, msg.ProjectID, dash(msg.TaskID), msg.Version), w))
	if msg.Deadline > 0 {
		lines = append(lines, stFaint.Render(cut("answer due in "+until(now, time.Unix(msg.Deadline, 0)), w)))
	}
	if msg.MayBeOutdated {
		lines = append(lines, stYellow.Render("the task changed since this was asked: it may be outdated"))
	}
	for _, l := range wrap(msg.Body, w-2) {
		lines = append(lines, " "+l)
	}
	if ap, ok := m.approvalOf(msg); ok {
		args, _ := json.Marshal(ap.Call.Arguments)
		lines = append(lines, "", stBold.Render(" approval "+ap.ID)+" "+ap.Status+" · agent "+ap.Agent+" · expires in "+until(now, time.Unix(ap.ExpiresAt, 0)))
		lines = append(lines, cut("  call: "+stMagenta.Render(ap.Call.Name)+" "+string(args), w))
		if ap.Reason != "" {
			lines = append(lines, cut("  reason: "+ap.Reason, w))
		}
		lines = append(lines, "  "+stGreen.Render("y approve")+" · "+stRed.Render("n reject"))
	}
	for _, q := range msg.Questions {
		lines = append(lines, "", cut(" "+stBold.Render(q.Question)+stFaint.Render(" ("+q.Type+")"), w))
		if len(msg.Questions) == 1 && q.Type == "single_choice" {
			opts := []string{}
			for i, o := range q.Options {
				label := " " + o + " "
				if i == s.option {
					label = stSelected.Render(label)
				}
				opts = append(opts, label)
			}
			lines = append(lines, cut("  "+strings.Join(opts, " "), w))
		} else if len(q.Options) > 0 {
			lines = append(lines, cut("  options: "+strings.Join(q.Options, ", ")+stFaint.Render(" (answer in free text: a)"), w))
		}
	}
	return lines
}
