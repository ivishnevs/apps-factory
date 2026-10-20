package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"appfactory/internal/storage"
)

type tab int

const (
	tabDashboard tab = iota
	tabTasks
	tabRun
	tabFlow
	tabFeed
	tabInbox
	tabCount
)

var tabNames = [tabCount]string{"Dashboard", "Tasks", "Run", "Flow", "Feed", "Inbox"}

// feedKeep is how many feed rows the UI keeps (feed view and flow).
const feedKeep = 3000

// Options configure the UI.
type Options struct {
	Client Client
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Location formats times; nil means Local.
	Location *time.Location
	// Refresh is the overview refresh period; 0 means 3 s.
	Refresh time.Duration
	// NoStreams keeps the model from starting streams and ticks (model tests drive it with messages).
	NoStreams bool
}

// Messages.
type (
	overviewMsg struct {
		ov  Overview
		err error
	}
	tickMsg   time.Time
	detailMsg struct {
		id  string
		d   TaskDetail
		err error
	}
	runMsg struct {
		id    string
		run   storage.Run
		calls []storage.ToolCall
		err   error
	}
	streamMsg StreamEvent
	actionMsg struct {
		text string
		err  error
	}
	runTickMsg struct{ id string }
)

type streamState struct {
	connected bool
	err       string
	seen      bool
}

// Model is the root of the UI.
type Model struct {
	c       Client
	opts    Options
	ctx     context.Context
	cancel  context.CancelFunc
	bus     chan tea.Msg
	width   int
	height  int
	tab     tab
	prevTab tab

	ov      Overview
	ovErr   error
	loaded  bool
	loading bool // an overview read is in flight
	lastOK  time.Time
	fails   int
	feed    []storage.FeedItem
	feedSeq int64
	feedSt  streamState

	// agentLast is the last activity line of each agent's live run (run tails).
	agentLast map[string]LogLine
	runTails  map[string]context.CancelFunc

	dash  dashState
	tasks tasksState
	run   runState
	flow  flowState
	feedv feedViewState
	inbox inboxState

	help    bool
	confirm *Action
	input   *inputState
	toast   string
	toastAt time.Time
	toastEr bool
}

type inputState struct {
	ti     textinput.Model
	label  string
	action Action
	// apply puts the typed text into the action; an error keeps the input open.
	apply func(a *Action, text string) error
	// local: a view setting; nothing to confirm or send.
	local bool
}

// New builds the UI model.
func New(o Options) *Model {
	if o.Refresh <= 0 {
		o.Refresh = 3 * time.Second
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Model{c: o.Client, opts: o, ctx: ctx, cancel: cancel, bus: make(chan tea.Msg, 256), width: 80, height: 24,
		agentLast: map[string]LogLine{}, runTails: map[string]context.CancelFunc{}}
	m.run.follow = true
	m.feedv.follow = true
	m.flow.follow = true
	return m
}

func (m *Model) now() time.Time {
	if m.opts.Now != nil {
		return m.opts.Now()
	}
	return time.Now()
}

func (m *Model) clock(t time.Time) string { return t.In(m.opts.Location).Format("15:04:05") }

// Init loads the overview and starts the feed stream and the refresh tick.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadOverview()}
	if !m.opts.NoStreams {
		cmds = append(cmds, m.listen(), m.tick(), m.stream(func(ctx context.Context, out func(StreamEvent)) {
			m.c.StreamFeed(ctx, FeedQuery{}, 0, out)
		}, m.ctx))
	}
	return tea.Batch(cmds...)
}

// Close stops the streams.
func (m *Model) Close() { m.cancel() }

func (m *Model) listen() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.bus:
			return msg
		case <-m.ctx.Done():
			return nil
		}
	}
}

func (m *Model) tick() tea.Cmd {
	return tea.Tick(m.opts.Refresh, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// stream runs a stream until ctx ends, delivering its events through the bus.
func (m *Model) stream(run func(ctx context.Context, out func(StreamEvent)), ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		run(ctx, func(e StreamEvent) {
			select {
			case m.bus <- streamMsg(e):
			case <-ctx.Done():
			}
		})
		return nil
	}
}

func (m *Model) loadOverview() tea.Cmd {
	m.loading = true
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ov, err := c.Overview(rctx)
		return overviewMsg{ov, err}
	}
}

func (m *Model) send(a Action) tea.Cmd {
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		text, err := c.Do(rctx, a)
		return actionMsg{text, err}
	}
}

func (m *Model) setToast(s string, isErr bool) {
	m.toast, m.toastAt, m.toastEr = oneLine(s), m.now(), isErr
}

// Update handles a message.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tickMsg:
		if m.loading {
			return m, m.tick() // a slow Core: no second read on top of the first
		}
		cmds := []tea.Cmd{m.loadOverview(), m.tick()}
		if m.tab == tabTasks && m.tasks.detail != nil && (m.tasks.open || m.wideTasks()) {
			cmds = append(cmds, m.loadDetail(m.tasks.detailID)) // the open detail stays current
		}
		return m, tea.Batch(cmds...)
	case overviewMsg:
		m.loading = false
		return m, m.onOverview(msg)
	case streamMsg:
		return m, tea.Batch(m.onStream(StreamEvent(msg)), m.listen())
	case detailMsg:
		m.onDetail(msg)
		return m, nil
	case runMsg:
		return m, m.onRun(msg)
	case runTickMsg:
		return m, m.onRunTick(msg)
	case actionMsg:
		if msg.err != nil {
			m.setToast("failed: "+msg.err.Error(), true)
		} else {
			m.setToast(msg.text, false)
		}
		cmds := []tea.Cmd{m.loadOverview()}
		if m.tasks.detailID != "" {
			cmds = append(cmds, m.loadDetail(m.tasks.detailID))
		}
		return m, tea.Batch(cmds...)
	case tea.PasteMsg:
		if m.input != nil {
			var cmd tea.Cmd
			m.input.ti, cmd = m.input.ti.Update(msg)
			return m, cmd
		}
	case tea.KeyPressMsg:
		return m, m.onKey(msg)
	}
	return m, nil
}

func (m *Model) onOverview(msg overviewMsg) tea.Cmd {
	if msg.err != nil {
		m.ovErr = msg.err
		m.fails++
		return nil
	}
	m.ov, m.ovErr, m.loaded, m.lastOK, m.fails = msg.ov, nil, true, m.now(), 0
	m.tasks.clamp(m)
	m.inbox.clamp(m)
	return m.syncRunTails()
}

// syncRunTails follows the last event of every live run (dashboard activity) and stops the tails of runs
// that ended.
func (m *Model) syncRunTails() tea.Cmd {
	if m.opts.NoStreams {
		return nil
	}
	live := map[string]bool{}
	cmds := []tea.Cmd{}
	for _, p := range m.ov.Status {
		for _, r := range p.Runs {
			live[r.ID] = true
			if _, ok := m.runTails[r.ID]; ok {
				continue
			}
			ctx, cancel := context.WithCancel(m.ctx)
			m.runTails[r.ID] = cancel
			id := r.ID
			cmds = append(cmds, m.stream(func(ctx context.Context, out func(StreamEvent)) {
				m.c.StreamRun(ctx, id, 0, 3, func(e StreamEvent) {
					e.Stream = "tail:" + id
					out(e)
				})
			}, ctx))
		}
	}
	for id, cancel := range m.runTails {
		if !live[id] {
			cancel()
			delete(m.runTails, id)
		}
	}
	return tea.Batch(cmds...)
}

func (m *Model) onStream(e StreamEvent) tea.Cmd {
	switch {
	case e.Stream == "feed":
		if e.State {
			m.feedSt = streamState{connected: e.Connected, err: e.Err, seen: true}
			return nil
		}
		if e.Feed != nil {
			m.addFeed(*e.Feed)
		}
	case strings.HasPrefix(e.Stream, "tail:"):
		if e.Run != nil {
			lines := describeRunEvent(e.Run.Envelope)
			if len(lines) > 0 {
				id := strings.TrimPrefix(e.Stream, "tail:")
				if agent := m.runAgent(id); agent != "" {
					m.agentLast[agent] = lines[len(lines)-1]
				}
			}
		}
	case strings.HasPrefix(e.Stream, "run:"):
		return m.run.onStream(m, e)
	}
	return nil
}

func (m *Model) addFeed(item storage.FeedItem) {
	if item.Sequence <= m.feedSeq {
		return
	}
	m.feedSeq = item.Sequence
	m.feed = append(m.feed, item)
	if len(m.feed) > feedKeep {
		m.feed = append([]storage.FeedItem(nil), m.feed[len(m.feed)-feedKeep:]...)
	}
}

// runAgent is the agent of a live run.
func (m *Model) runAgent(id string) string {
	for _, p := range m.ov.Status {
		for _, r := range p.Runs {
			if r.ID == id {
				return r.Agent
			}
		}
	}
	return ""
}

// companyStopped is the company pause (the kill switch or a company pause), if any.
func (m *Model) companyStopped() (storage.Pause, bool) {
	for _, p := range m.ov.Pauses {
		if p.Scope == "company" {
			return p, true
		}
	}
	return storage.Pause{}, false
}

func (m *Model) openInput(label, initial string, a Action, apply func(a *Action, text string) error) tea.Cmd {
	ti := textinput.New()
	ti.Prompt = "› "
	ti.SetValue(initial)
	ti.CursorEnd()
	ti.SetWidth(max(m.width-4, 10))
	m.input = &inputState{ti: ti, label: label, action: a, apply: apply}
	return m.input.ti.Focus()
}

// openSetting asks for a view setting (a filter): enter applies it, no confirmation, nothing is sent.
func (m *Model) openSetting(label, initial string, set func(text string)) tea.Cmd {
	cmd := m.openInput(label, initial, Action{}, func(_ *Action, text string) error {
		set(text)
		return nil
	})
	m.input.local = true
	return cmd
}

func (m *Model) askConfirm(a Action) {
	m.confirm = &a
}

func (m *Model) switchTab(t tab) tea.Cmd {
	if t == m.tab {
		return nil
	}
	m.prevTab, m.tab = m.tab, t
	return nil
}

func (m *Model) onKey(k tea.KeyPressMsg) tea.Cmd {
	key := k.String()
	if key == "ctrl+c" {
		m.cancel()
		return tea.Quit
	}
	if m.input != nil {
		switch key {
		case "esc":
			m.input = nil
			return nil
		case "enter":
			in := m.input
			a := in.action
			if err := in.apply(&a, strings.TrimSpace(in.ti.Value())); err != nil {
				m.setToast(err.Error(), true)
				return nil
			}
			m.input = nil
			if in.local {
				return nil // a view setting, applied by apply
			}
			m.askConfirm(a)
			return nil
		}
		var cmd tea.Cmd
		m.input.ti, cmd = m.input.ti.Update(k)
		return cmd
	}
	if m.confirm != nil {
		a := *m.confirm
		switch key {
		case "y", "Y", "enter":
			m.confirm = nil
			m.setToast("sending: "+a.Describe(), false)
			return m.send(a)
		case "n", "N", "esc", "q":
			m.confirm = nil
			m.setToast("cancelled", false)
		}
		return nil
	}
	if m.help {
		m.help = false
		return nil
	}
	if m.tab == tabTasks && m.tasks.filtering() || m.tab == tabFeed && m.feedv.filtering() {
		// The view's filter input has the keys.
	} else {
		switch key {
		case "q":
			m.cancel()
			return tea.Quit
		case "?":
			m.help = true
			return nil
		case "tab":
			return m.switchTab((m.tab + 1) % tabCount)
		case "shift+tab":
			return m.switchTab((m.tab + tabCount - 1) % tabCount)
		case "1", "2", "3", "4", "5", "6":
			return m.switchTab(tab(key[0] - '1'))
		case "ctrl+r", "R":
			m.setToast("refreshing", false)
			return m.loadOverview()
		case "K":
			if p, stopped := m.companyStopped(); stopped {
				m.askConfirm(Action{Kind: "start", Text: p.Reason})
				return nil
			}
			return m.openInput("Kill switch: stop the company. Reason", "", Action{Kind: "stop"}, func(a *Action, text string) error {
				a.Text = text
				return nil
			})
		}
	}
	switch m.tab {
	case tabDashboard:
		return m.dash.key(m, key)
	case tabTasks:
		return m.tasks.key(m, k)
	case tabRun:
		return m.run.key(m, key)
	case tabFlow:
		return m.flow.key(m, k)
	case tabFeed:
		return m.feedv.key(m, k)
	case tabInbox:
		return m.inbox.key(m, key)
	}
	return nil
}

// View renders the screen: tabs, the active view, overlays and the status bar.
func (m *Model) View() tea.View {
	v := tea.NewView(m.Render())
	v.AltScreen = true
	v.WindowTitle = "factory ui"
	return v
}

// Render is the screen as a string (golden tests).
func (m *Model) Render() string {
	w, h := m.width, m.height
	if w < 20 || h < 6 {
		return "terminal too small"
	}
	bodyH := h - 2
	var body []string
	switch {
	case m.help:
		body = m.helpLines(w)
	case m.tab == tabDashboard:
		body = m.dashboardLines(w, bodyH)
	case m.tab == tabTasks:
		body = m.tasksLines(w, bodyH)
	case m.tab == tabRun:
		body = m.runLines(w, bodyH)
	case m.tab == tabFlow:
		body = m.flowLines(w, bodyH)
	case m.tab == tabFeed:
		body = m.feedLines(w, bodyH)
	case m.tab == tabInbox:
		body = m.inboxLines(w, bodyH)
	}
	if overlay := m.overlayLines(w); len(overlay) > 0 {
		if len(body) > bodyH-len(overlay) {
			body = body[:max(bodyH-len(overlay), 0)]
		}
		for len(body) < bodyH-len(overlay) {
			body = append(body, "")
		}
		body = append(body, overlay...)
	}
	return m.headerLine(w) + "\n" + block(body, w, bodyH) + "\n" + m.statusLine(w)
}

func (m *Model) headerLine(w int) string {
	parts := []string{}
	for i, name := range tabNames {
		label := fmt.Sprintf(" %d %s", i+1, name)
		if tab(i) == tabInbox && len(m.ov.Inbox) > 0 {
			label += fmt.Sprintf("(%d)", len(m.ov.Inbox))
		}
		label += " "
		if tab(i) == m.tab {
			parts = append(parts, stTabActive.Render(label))
		} else {
			parts = append(parts, stTab.Render(label))
		}
	}
	return fit(strings.Join(parts, "")+stFaint.Render("  ? help"), w)
}

func (m *Model) overlayLines(w int) []string {
	switch {
	case m.input != nil:
		return []string{
			stFaint.Render(strings.Repeat("─", w)),
			stBold.Render(m.input.label) + "  " + stFaint.Render("enter ok · esc cancel"),
			m.input.ti.View(),
		}
	case m.confirm != nil:
		prompt := m.confirm.Describe()
		lines := []string{stFaint.Render(strings.Repeat("─", w))}
		for _, l := range wrap(prompt+"?", w-2) {
			lines = append(lines, stBold.Render(l))
		}
		return append(lines, stYellow.Render("y")+" confirm · "+stYellow.Render("n")+" cancel")
	}
	return nil
}

func (m *Model) statusLine(w int) string {
	core := "● " + m.c.Describe()
	style := stBar
	switch {
	case m.ovErr != nil && !m.loaded:
		core = "○ " + m.c.Describe() + " unreachable: " + m.ovErr.Error()
		style = stBarAlert
	case m.ovErr != nil:
		core = "○ " + m.c.Describe() + " retrying (" + ago(m.now(), m.lastOK) + " stale)"
		style = stBarAlert
	case !m.loaded:
		core = "… " + m.c.Describe() + " connecting"
	}
	parts := []string{core}
	if m.ov.Health.Company != "" {
		parts = append(parts, "company "+m.ov.Health.Company)
	}
	if p, stopped := m.companyStopped(); stopped {
		parts = append([]string{"■ STOPPED " + p.Reason}, parts...) // first: never cut off
		style = stBarAlert
	} else if m.loaded {
		parts = append(parts, "▶ running")
	}
	switch {
	case !m.feedSt.seen:
	case m.feedSt.connected:
		parts = append(parts, "feed live")
	default:
		parts = append(parts, "feed reconnecting")
	}
	left := " " + strings.Join(parts, " │ ")
	right := ""
	if m.toast != "" && m.now().Sub(m.toastAt) < 8*time.Second {
		right = m.toast
		if m.toastEr {
			style = stBarAlert
		}
	}
	if right != "" {
		lw := w - min(ansiWidth(right)+3, w/2)
		return style.Render(fit(left, lw) + fit(" "+right+" ", w-lw))
	}
	return style.Render(fit(left, w))
}

// projects are the project IDs, sorted.
func (m *Model) projects() []string {
	out := []string{}
	for _, p := range m.ov.Status {
		out = append(out, p.Project.ID)
	}
	sort.Strings(out)
	return out
}

// nextProject cycles "" (all) → each project → "".
func (m *Model) nextProject(cur string) string {
	ps := m.projects()
	if cur == "" {
		if len(ps) > 0 {
			return ps[0]
		}
		return ""
	}
	for i, p := range ps {
		if p == cur && i+1 < len(ps) {
			return ps[i+1]
		}
	}
	return ""
}

func (m *Model) taskByID(id string) (Task, bool) {
	for _, t := range m.ov.Tasks {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}
