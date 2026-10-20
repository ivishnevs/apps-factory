package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"appfactory/internal/storage"
)

// feedViewState is the live feed: attention rows highlighted, filters by attention and words (the project
// is the header selection).
type feedViewState struct {
	attention bool
	filter    string
	editing   bool
	ti        textinput.Model
	scroll    int
	follow    bool
}

func (m *Model) feedMatches(item storage.FeedItem) bool {
	f := &m.feedv
	if f.attention && !item.Attention || m.project != "" && item.ProjectID != "" && item.ProjectID != m.project {
		return false
	}
	if f.filter == "" {
		return true
	}
	text := strings.ToLower(strings.Join([]string{item.Type, item.TaskID, item.RunID, item.Agent, item.Actor, item.Summary, item.ProjectID}, " "))
	for _, w := range strings.Fields(strings.ToLower(f.filter)) {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

func (f *feedViewState) key(m *Model, k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if f.editing {
		switch key {
		case "enter", "esc":
			f.editing = false
			f.ti.Blur()
			return nil
		}
		var cmd tea.Cmd
		f.ti, cmd = f.ti.Update(k)
		f.filter, f.scroll = f.ti.Value(), 0
		return cmd
	}
	switch key {
	case "!", "a":
		f.attention, f.scroll = !f.attention, 0
	case "/":
		f.ti = textinput.New()
		f.ti.Prompt = "filter › "
		f.ti.Placeholder = "words in type, task, agent or summary"
		f.ti.SetValue(f.filter)
		f.ti.CursorEnd()
		f.ti.SetWidth(max(m.width-16, 10))
		f.editing = true
		return f.ti.Focus()
	case "x", "esc":
		f.attention, f.filter, f.scroll = false, "", 0
	case "k", "up":
		f.scroll++
		f.follow = false
	case "j", "down":
		f.scroll = max(f.scroll-1, 0)
		f.follow = f.scroll == 0
	case "pgup":
		f.scroll += m.height / 2
		f.follow = false
	case "pgdown", "space":
		f.scroll = max(f.scroll-m.height/2, 0)
		f.follow = f.scroll == 0
	case "G", "end":
		f.scroll, f.follow = 0, true
	}
	return nil
}

// feedIcon is the glyph of a feed event.
func feedIcon(item storage.FeedItem) string {
	t := item.Type
	switch {
	case item.Attention:
		return stRedBold.Render("⚠")
	case strings.HasSuffix(t, ".done"):
		return stGreen.Render("✓")
	case t == "task.created":
		return stCyan.Render("+")
	case strings.HasPrefix(t, "task.exit."), strings.HasPrefix(t, "message."):
		return stMagenta.Render("→")
	case t == "task.started":
		return stGreen.Render("▶")
	case strings.Contains(t, "refused"), t == "task.retry":
		return stRed.Render("✗")
	case strings.Contains(t, "paused"), strings.Contains(t, "parked"):
		return stYellow.Render("⏸")
	case strings.HasPrefix(t, "task.trigger."), t == "task.commented":
		return stYellow.Render("✎")
	case strings.HasPrefix(t, "node."):
		return stBlue.Render("◇")
	}
	return stFaint.Render("·")
}

func (m *Model) feedLines(w, h int) []string {
	f := &m.feedv
	inner := w - 4
	state := stFaint.Render("◌ connecting")
	switch {
	case m.feedSt.connected:
		state = stGreen.Render("● live")
	case m.feedSt.seen:
		state = stRed.Render("◐ reconnecting " + m.feedSt.err)
	}
	head := []string{}
	if f.editing {
		head = append(head, f.ti.View())
	}
	rows := []string{}
	for _, item := range m.feed {
		if !m.feedMatches(item) {
			continue
		}
		who := item.Actor
		if who == "" {
			who = item.Agent
		}
		where := item.TaskID
		if where == "" {
			where = "·"
		}
		typ := strings.TrimPrefix(item.Type, "task.")
		ts := stMagenta
		if item.Attention {
			ts = stRedBold
		}
		summary := oneLine(item.Summary)
		row := stDim.Render(m.clock(item.CreatedAt)) + " " + feedIcon(item) + " " + ts.Render(fit(typ, 22)) + stFaint.Render(fit(where, 7)) +
			lipgloss.NewStyle().Foreground(agentColor(who)).Render(fit(dash(who), 17)) + summary
		if item.Attention {
			row = lipgloss.NewStyle().Background(theme.surface).Render(fit(row, inner))
		}
		rows = append(rows, cut(row, inner))
	}
	room := max(h-2-len(head), 1)
	if len(rows) == 0 {
		rows = append(rows, "", stFaint.Render(fmt.Sprintf("  no feed rows (%d received)", len(m.feed))))
	}
	end := len(rows) - f.scroll
	if end < room {
		end = min(room, len(rows))
		f.scroll = len(rows) - end
	}
	follow := stGreen.Render("follow")
	if !f.follow {
		follow = stYellow.Render("paused · G")
	}
	title := "Feed " + state + stFaint.Render(" · ") + follow
	if f.attention {
		title += stFaint.Render(" · ") + stRedBold.Render("attention only")
	}
	if f.filter != "" {
		title += stFaint.Render(" · filter ") + stYellow.Render(f.filter)
	}
	return panel(title, append(head, rows[max(end-room, 0):end]...), w, h, true)
}
