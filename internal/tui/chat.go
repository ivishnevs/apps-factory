package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/messaging"
)

// chatState is the Chat tab: the owner's direct reports in the selected project, a conversation per report
// (the owner conversation owner-<agent>-<project>, both directions), and an input box. Enter sends through
// POST /messages: to the selected report (an @name prefix is added when it is not the default report), or to
// the report a leading @name addresses.
type chatState struct {
	sel    int
	typing bool
	ti     textinput.Model
	ready  bool
	msgs   map[string][]messaging.Message
	errs   map[string]string
	// seen is the newest message time the owner has seen in each conversation.
	seen    map[string]int64
	scroll  int // lines above the newest
	sending bool
}

type (
	chatMsg struct {
		conv string
		msgs []messaging.Message
		err  error
	}
	chatSentMsg struct {
		conv, project, text string
		err                 error
	}
)

// chatProject is the project the chat shows: the selected one, else the first.
func (m *Model) chatProject() string {
	if m.project != "" {
		return m.project
	}
	if ps := m.projects(); len(ps) > 0 {
		return ps[0]
	}
	return ""
}

func (m *Model) chatReports() []string { return directReports(m.ov.Agents, defaultReportOf(m.ov)) }

// chatAgent is the selected report.
func (m *Model) chatAgent() string {
	r := m.chatReports()
	if len(r) == 0 {
		return ""
	}
	m.chat.sel = min(max(m.chat.sel, 0), len(r)-1)
	return r[m.chat.sel]
}

func (m *Model) loadChat(conv string) tea.Cmd {
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		msgs, err := c.Messages(rctx, conv)
		return chatMsg{conv: conv, msgs: msgs, err: err}
	}
}

// loadChats reads every owner conversation (each direct report in each project): unread markers count all.
func (m *Model) loadChats() tea.Cmd {
	cmds := []tea.Cmd{}
	for _, p := range m.projects() {
		for _, a := range m.chatReports() {
			cmds = append(cmds, m.loadChat(ownerConversation(a, p)))
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) onChat(msg chatMsg) {
	s := &m.chat
	if msg.err != nil {
		s.errs[msg.conv] = msg.err.Error()
		return
	}
	delete(s.errs, msg.conv)
	sort.SliceStable(msg.msgs, func(i, j int) bool { return msg.msgs[i].CreatedAt < msg.msgs[j].CreatedAt })
	s.msgs[msg.conv] = msg.msgs
	if m.tab == tabChat && msg.conv == ownerConversation(m.chatAgent(), m.chatProject()) {
		m.markSeen(msg.conv)
	}
}

func (m *Model) markSeen(conv string) {
	list := m.chat.msgs[conv]
	if len(list) > 0 {
		m.chat.seen[conv] = max(m.chat.seen[conv], list[len(list)-1].CreatedAt)
	}
}

// unread counts the report's messages the owner has not seen: newer than its own last message and than
// what this session showed.
func (m *Model) unread(conv string) int {
	list := m.chat.msgs[conv]
	floor := m.chat.seen[conv]
	for _, x := range list {
		if x.Sender == "owner" {
			floor = max(floor, x.CreatedAt)
		}
	}
	n := 0
	for _, x := range list {
		if x.Sender != "owner" && x.CreatedAt > floor {
			n++
		}
	}
	return n
}

func (m *Model) unreadTotal() int {
	n := 0
	for conv := range m.chat.msgs {
		if m.tab == tabChat && conv == ownerConversation(m.chatAgent(), m.chatProject()) {
			continue
		}
		n += m.unread(conv)
	}
	return n
}

// replying: the agent's conversation task in the project has a live run.
func (m *Model) replying(agent, project string) bool {
	for _, t := range m.ov.Tasks {
		if t.Kind == "conversation" && t.Assignee == agent && t.ProjectID == project {
			if _, ok := m.liveRunOf(t.ID); ok {
				return true
			}
		}
	}
	return false
}

func (s *chatState) init(m *Model) {
	if s.ready {
		return
	}
	s.ti = textinput.New()
	s.ti.Prompt = "› "
	s.ti.Placeholder = "message (enter sends · @name addresses another report)"
	s.ti.CharLimit = 16 * 1024
	s.ready = true
}

func (s *chatState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	s.init(m)
	key := k.String()
	reports := m.chatReports()
	if s.typing {
		switch key {
		case "esc":
			s.typing = false
			s.ti.Blur()
			return nil
		case "enter":
			return m.sendChat(strings.TrimSpace(s.ti.Value()))
		}
		var cmd tea.Cmd
		s.ti, cmd = s.ti.Update(k)
		return cmd
	}
	switch key {
	case "j", "down":
		s.sel, s.scroll = min(s.sel+1, max(len(reports)-1, 0)), 0
		m.markSeen(ownerConversation(m.chatAgent(), m.chatProject()))
	case "k", "up":
		s.sel, s.scroll = max(s.sel-1, 0), 0
		m.markSeen(ownerConversation(m.chatAgent(), m.chatProject()))
	case "pgup", "ctrl+u":
		s.scroll += max(m.height/2, 1)
	case "pgdown", "ctrl+d":
		s.scroll = max(s.scroll-max(m.height/2, 1), 0)
	case "G", "end":
		s.scroll = 0
	case "enter", "i", "@":
		s.typing = true
		if key == "@" {
			s.ti.SetValue("@")
			s.ti.CursorEnd()
		}
		return s.ti.Focus()
	case "t":
		for _, t := range m.ov.Tasks {
			if t.Kind == "conversation" && t.Assignee == m.chatAgent() && t.ProjectID == m.chatProject() {
				if id, ok := m.liveRunOf(t.ID); ok {
					return m.openTranscript(id)
				}
			}
		}
		m.setToast(m.chatAgent()+" is not replying now", true)
	}
	return nil
}

// sendChat sends the typed message to the selected report (or the @name it starts with).
func (m *Model) sendChat(text string) tea.Cmd {
	s := &m.chat
	if text == "" {
		return nil
	}
	project, agent := m.chatProject(), m.chatAgent()
	reports, def := m.chatReports(), defaultReportOf(m.ov)
	body := text
	if !strings.HasPrefix(text, "@") && agent != def {
		body = "@" + agent + " " + text
	}
	to, ok := addressee(body, reports, def)
	if !ok {
		m.setToast("✗ @name must be one of your direct reports: "+strings.Join(reports, ", "), true)
		return nil
	}
	for i, r := range reports {
		if r == to {
			s.sel = i
		}
	}
	s.ti.SetValue("")
	s.sending, s.scroll = true, 0
	conv := ownerConversation(to, project)
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		_, err := c.Do(rctx, Action{Kind: "say", Project: project, Text: body})
		return chatSentMsg{conv: conv, project: project, text: body, err: err}
	}
}

func (m *Model) onChatSent(msg chatSentMsg) tea.Cmd {
	m.chat.sending = false
	if msg.err != nil {
		m.setToast("✗ "+msg.err.Error(), true)
		m.chat.ti.SetValue(msg.text) // nothing is lost
		return nil
	}
	m.setToast("✓ sent", false)
	return tea.Batch(m.loadChat(msg.conv), m.loadOverview())
}

func (m *Model) chatLines(w, h int) []string {
	if !m.loaded {
		return m.notLoaded()
	}
	s := &m.chat
	s.init(m)
	project := m.chatProject()
	reports := m.chatReports()
	def := defaultReportOf(m.ov)
	listW := min(max(w/4, 26), 34)
	list := []string{stFaint.Render("project ") + projectChip(project), stDim.Render("[ ] switches project"), ""}
	if len(reports) == 0 {
		list = append(list, stFaint.Render("no agent reports to the owner"))
	}
	agent := m.chatAgent()
	for i, a := range reports {
		conv := ownerConversation(a, project)
		name := lipgloss.NewStyle().Foreground(agentColor(a)).Bold(i == s.sel).Render("● " + a)
		right := ""
		if n := m.unread(conv); n > 0 && !(i == s.sel && m.tab == tabChat) {
			right = stRedBold.Render(fmt.Sprintf("•%d", n))
		}
		line := name
		if a == def {
			line += stYellow.Render(" ★")
		}
		line = rightAlign(line, right, listW-4)
		sub := "  " + stDim.Render(roleOf(m, a))
		if m.replying(a, project) {
			sub = "  " + stGreen.Render(m.spin.View()+" replying…")
		}
		if i == s.sel {
			bg := lipgloss.NewStyle().Background(theme.surfaceHi)
			line, sub = bg.Render(fit(line, listW-4)), bg.Render(fit(sub, listW-4))
		}
		list = append(list, line, sub)
	}
	list = append(list, "", stDim.Render("★ default report"))
	left := panel("Direct reports", list, listW, h, !s.typing)

	cw := w - listW
	inner := cw - 4
	conv := ownerConversation(agent, project)
	msgs := m.chat.msgs[conv]
	content := []string{}
	if e, ok := s.errs[conv]; ok {
		content = append(content, stRed.Render("✗ "+e))
	}
	if len(msgs) == 0 && agent != "" {
		content = append(content, "", stFaint.Render("  no messages with "+agent+" in "+project+" yet: enter to write one"))
	}
	for _, x := range msgs {
		content = append(content, m.chatBubble(x, inner)...)
	}
	inputH := 3
	status := ""
	if agent != "" && m.replying(agent, project) {
		status = lipgloss.NewStyle().Foreground(agentColor(agent)).Render(m.spin.View() + " " + agent + " is replying" + strings.Repeat(".", m.frameN%4))
	} else if s.sending {
		status = stFaint.Render(m.spin.View() + " sending…")
	}
	viewH := max(h-2-inputH-1, 1)
	end := max(len(content)-s.scroll, 0)
	s.scroll = len(content) - end
	start := max(end-viewH, 0)
	shown := append([]string{}, content[start:end]...)
	for len(shown) < viewH {
		shown = append([]string{""}, shown...)
	}
	more := ""
	if start > 0 {
		more = stDim.Render(fmt.Sprintf("↑ %d more lines · pgup", start))
	}
	shown = append(shown, rightAlign(" "+status, more, inner))
	s.ti.SetWidth(max(inner-10, 10))
	in := s.ti.View()
	if !s.typing {
		in = stDim.Render("enter to write to " + dash(agent) + " · @name addresses another report")
	}
	box := frame("", []string{in}, inner, inputH, theme.faint, s.typing, false)
	shown = append(shown, box...)
	title := lipgloss.NewStyle().Foreground(agentColor(agent)).Bold(true).Render(dash(agent)) + stFaint.Render(" · "+conv)
	right := panel(title, shown, cw, h, s.typing)
	return hjoin(0, left, right)
}

func roleOf(m *Model, name string) string {
	for _, a := range m.ov.Agents {
		if a.Name == name {
			return a.Role
		}
	}
	return ""
}

// chatBubble is one message: the agent's on the left in its color, the owner's on the right in the accent.
func (m *Model) chatBubble(x messaging.Message, w int) []string {
	bw := min(max(w*3/4, 20), w)
	body := wrap(x.Body, bw-4)
	for _, q := range x.Questions {
		line := "? " + q.Question
		if len(q.Options) > 0 {
			line += "  [" + strings.Join(q.Options, " / ") + "]"
		}
		body = append(body, wrap(line, bw-4)...)
	}
	if len(x.Answers) > 0 {
		keys := []string{}
		for k := range x.Answers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			body = append(body, stFaint.Render(fmt.Sprintf("↳ %s: %v", k, x.Answers[k])))
		}
	}
	when := stDim.Render(time.Unix(x.CreatedAt, 0).In(m.opts.Location).Format("01-02 15:04"))
	kind := ""
	if x.MessageType != "" && x.MessageType != "question" && x.MessageType != "answer" {
		kind = stFaint.Render(" · " + x.MessageType)
	}
	pending := ""
	if x.Sender != "owner" && x.MessageType == "question" && x.Status == "PENDING" {
		pending = stRedBold.Render(" · waits on you")
	}
	// The bubble fits its text (and its title).
	need := ansiWidth(x.Sender+" 01-02 15:04"+ansiStrip(kind+pending)) + 6
	for _, l := range body {
		need = max(need, ansiWidth(l)+4)
	}
	bw = min(bw, need)
	if x.Sender == "owner" {
		title := stTitle.Render("you") + " " + when + kind
		out := []string{""}
		for _, l := range frame(title, body, bw, len(body)+2, theme.accent, false, false) {
			out = append(out, strings.Repeat(" ", max(w-bw, 0))+l)
		}
		return out
	}
	ac := agentColor(x.Sender)
	title := lipgloss.NewStyle().Foreground(ac).Bold(true).Render(x.Sender) + " " + when + kind + pending
	return append([]string{""}, frame(title, body, bw, len(body)+2, ac, false, false)...)
}
