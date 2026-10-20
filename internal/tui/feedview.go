package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"appfactory/internal/storage"
)

// feedViewState is the live feed: attention rows highlighted, filters by project, attention and text.
type feedViewState struct {
	attention bool
	project   string
	filter    string
	editing   bool
	ti        textinput.Model
	scroll    int
	follow    bool
}

func (f *feedViewState) filtering() bool { return f.editing }

func (f *feedViewState) matches(item storage.FeedItem) bool {
	if f.attention && !item.Attention || f.project != "" && item.ProjectID != f.project {
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
	case "p":
		f.project, f.scroll = m.nextProject(f.project), 0
	case "/":
		f.ti = textinput.New()
		f.ti.Prompt = "filter › "
		f.ti.Placeholder = "words in type, task, agent or summary"
		f.ti.SetValue(f.filter)
		f.ti.CursorEnd()
		f.ti.SetWidth(max(m.width-12, 10))
		f.editing = true
		return f.ti.Focus()
	case "x", "esc":
		f.attention, f.project, f.filter, f.scroll = false, "", "", 0
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

func (m *Model) feedLines(w, h int) []string {
	f := &m.feedv
	state := stFaint.Render("connecting")
	switch {
	case m.feedSt.connected:
		state = stGreen.Render("live")
	case m.feedSt.seen:
		state = stRed.Render("reconnecting: " + m.feedSt.err)
	}
	filters := []string{"project " + orAll(f.project)}
	if f.attention {
		filters = append(filters, stRedBold.Render("attention only"))
	}
	if f.filter != "" {
		filters = append(filters, "filter "+stYellow.Render(f.filter))
	}
	follow := "follow"
	if !f.follow {
		follow = stYellow.Render("paused (G)")
	}
	lines := []string{
		cut(stTitle.Render("Feed")+" "+state+" · "+follow+" · "+strings.Join(filters, " · "), w),
		cut(stFaint.Render("! attention only · p project · / filter · x clear · j/k scroll · G newest"), w),
	}
	if f.editing {
		lines = append(lines, f.ti.View())
	}
	rows := []string{}
	for _, item := range m.feed {
		if !f.matches(item) {
			continue
		}
		mark := " "
		if item.Attention {
			mark = stRedBold.Render("!")
		}
		where := item.ProjectID + "/" + item.TaskID
		if item.TaskID == "" {
			where = dash(item.ProjectID)
		}
		who := item.Agent
		if item.Actor != "" && item.Actor != item.Agent {
			who = strings.TrimSpace(who + " by " + item.Actor)
		}
		typ := col(item.Type, 25)
		if item.Attention {
			typ = stRedBold.Render(typ)
		}
		var row string
		if w >= 120 {
			row = stFaint.Render(m.clock(item.CreatedAt)) + " " + mark + " " + typ + col(where, 15) + col(who, 24) + oneLine(item.Summary)
		} else {
			// Narrow: the summary says most; who leads it.
			if who != "" && !strings.HasPrefix(item.Summary, item.Agent+" ") && !strings.HasPrefix(item.Summary, item.Actor+" ") {
				who = stBold.Render(who) + ": "
			} else {
				who = ""
			}
			row = stFaint.Render(m.clock(item.CreatedAt)[:5]) + " " + mark + " " + typ + col(where, 14) + who + oneLine(item.Summary)
		}
		rows = append(rows, cut(row, w))
	}
	if len(rows) == 0 {
		return append(lines, "", stFaint.Render(fmt.Sprintf("  no feed rows (%d received)", len(m.feed))))
	}
	room := max(h-len(lines), 1)
	end := len(rows) - f.scroll
	if end < room {
		end = min(room, len(rows))
		f.scroll = len(rows) - end
	}
	return append(lines, rows[max(end-room, 0):end]...)
}
