package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

type tab int

const (
	tabDashboard tab = iota
	tabBoard
	tabFlow
	tabFeed
	tabInbox
	tabChat
	tabNodes
	tabCosts
	tabCount
	// tabRun is the live run view: opened from a card, a task or an agent, not a tab of its own.
	tabRun tab = 100
	// tabTranscript is a run's full transcript (t on a run).
	tabTranscript tab = 101
)

var tabNames = [tabCount]string{"Dashboard", "Board", "Flow", "Feed", "Inbox", "Chat", "Nodes", "Costs"}

// tabShort are the tab names on a narrow terminal.
var tabShort = [tabCount]string{"Dash", "Board", "Flow", "Feed", "Inbox", "Chat", "Nodes", "Costs"}

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
	// NoStreams keeps the model from starting streams, ticks and animation (tests drive it with messages).
	NoStreams bool
	// Local finds runs' session files on this machine (the transcript's local source).
	Local LocalTranscripts
	// Notify is desktop (default), bell or off: how a new attention item for the owner notifies.
	Notify string
	// Run runs external commands (osascript, open); nil runs them.
	Run CommandRunner
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

// toastItem is an attention event sliding in at the top right.
type toastItem struct {
	text string
	at   time.Time
}

// zone is a clickable rectangle of the last rendered screen.
type zone struct {
	x0, y0, x1, y1 int
	click          func(x, y int) tea.Cmd
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
	spin    spinner.Model
	frameN  int
	startAt time.Time

	ov      Overview
	ovErr   error
	loaded  bool
	loading bool // an overview read is in flight
	lastOK  time.Time
	fails   int
	feed    []storage.FeedItem
	feedSeq int64
	feedSt  streamState
	// project is the selected project ("" = all): the chips in the header.
	project string

	// agentLast is the last activity line of each agent's live run (run tails); activity merges the feed
	// and every live run's steps.
	agentLast map[string]LogLine
	runTails  map[string]context.CancelFunc
	// runCtx is each tailed run's context (its last model call), for the Stats panel.
	runCtx   map[string]ctxUsage
	activity []actRow
	actSeen  map[string]bool

	dash  dashState
	board boardState
	run   runState
	flow  flowState
	feedv feedViewState
	inbox inboxState
	chat  chatState
	tx    txState
	nodes nodesState
	costs costsState
	// spinning: an animation tick is pending (only while something live is visible).
	spinning bool

	// errs are the runs' tool error counts; errAt when each was read.
	errs  map[string]errTally
	errAt map[string]time.Time
	notif *notifier
	form  *formState
	// projForm is the new project form (ctrl+n).
	projForm *projFormState
	// preview is an artifact preview modal.
	preview *previewState

	help    bool
	confirm *Action
	input   *inputState
	palette *paletteState
	chooser *chooserState
	toast   string
	toastAt time.Time
	toastEr bool
	toasts  []toastItem
	zones   []zone
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
	m := &Model{c: o.Client, opts: o, ctx: ctx, cancel: cancel, bus: make(chan tea.Msg, 512), width: 100, height: 30,
		agentLast: map[string]LogLine{}, runTails: map[string]context.CancelFunc{}, runCtx: map[string]ctxUsage{}, actSeen: map[string]bool{},
		errs: map[string]errTally{}, errAt: map[string]time.Time{}, notif: newNotifier(o.Notify, o.Run),
		chat: chatState{msgs: map[string][]messaging.Message{}, errs: map[string]string{}, seen: map[string]int64{}},
		spin: spinner.New(spinner.WithSpinner(spinner.MiniDot))}
	m.startAt = m.now()
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

// Init loads the overview, asks the terminal for its background (light or dark theme) and starts the feed
// stream, the refresh tick and the animation.
func (m *Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadOverview(), tea.RequestBackgroundColor}
	if !m.opts.NoStreams {
		m.spinning = true // the loading spinner
		cmds = append(cmds, m.listen(), m.tick(), m.spin.Tick, m.stream(func(ctx context.Context, out func(StreamEvent)) {
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

// Update handles a message. The animation (spinners, pulses, timers) ticks only while something live is
// visible: an idle screen gets no frames and is redrawn only when data changes.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if t, ok := msg.(spinner.TickMsg); ok {
		if !m.wantsAnim() {
			m.spinning = false
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(t)
		m.frameN++
		return m, cmd
	}
	model, cmd := m.update(msg)
	return model, tea.Batch(cmd, m.animCmd())
}

// animCmd starts the animation when something live became visible and no tick is pending.
func (m *Model) animCmd() tea.Cmd {
	if m.spinning || m.opts.NoStreams || !m.wantsAnim() {
		return nil
	}
	m.spinning = true
	return m.spin.Tick
}

// wantsAnim: something on the screen moves: a loading spinner, a sliding toast, a reply or a create in
// flight, or a live run on a screen that shows live runs.
func (m *Model) wantsAnim() bool {
	if !m.loaded || m.chat.sending || m.form != nil && m.form.sending || m.projForm != nil && m.projForm.sending || m.preview != nil && m.preview.loading {
		return true
	}
	if m.tab == tabBoard && m.board.open && m.board.detail == nil {
		return true
	}
	for _, t := range m.toasts {
		if m.now().Sub(t.at) < 400*time.Millisecond {
			return true
		}
	}
	switch m.tab {
	case tabDashboard, tabBoard:
		return m.anyRunning()
	case tabRun:
		return m.run.run == nil || !m.run.ended
	case tabTranscript:
		return m.tx.run == nil || !m.tx.ended
	case tabChat:
		return m.replying(m.chatAgent(), m.chatProject())
	}
	return false
}

// anyRunning: a run is claimed, running or stopping (a QUEUED run does not move).
func (m *Model) anyRunning() bool {
	for _, p := range m.ov.Status {
		for _, r := range p.Runs {
			if r.Status != "QUEUED" {
				return true
			}
		}
	}
	return false
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		applyTheme(msg.IsDark())
		return m, nil
	case costsMsg:
		m.onCosts(msg)
		return m, nil
	case tickMsg:
		if m.loading {
			return m, m.tick() // a slow Core: no second read on top of the first
		}
		cmds := []tea.Cmd{m.loadOverview(), m.tick()}
		if m.board.open && m.board.detail != nil {
			cmds = append(cmds, m.loadDetail(m.board.detailID)) // the open task stays current
		}
		if m.tab == tabCosts && !m.costs.loading && m.now().Sub(m.costs.at) > costsRefresh {
			cmds = append(cmds, m.loadCosts())
		}
		return m, tea.Batch(cmds...)
	case overviewMsg:
		m.loading = false
		return m, m.onOverview(msg)
	case streamMsg:
		return m, tea.Batch(m.onStream(StreamEvent(msg)), m.listen())
	case detailMsg:
		return m, m.onDetail(msg)
	case runMsg:
		return m, m.onRun(msg)
	case runTickMsg:
		return m, m.onRunTick(msg)
	case txLoadMsg:
		return m, m.onTxLoad(msg)
	case txLocalMsg:
		m.onTxLocal(msg)
		return m, nil
	case txTickMsg:
		return m, m.onTxTick(msg)
	case errsMsg:
		m.onErrs(msg)
		return m, nil
	case chatMsg:
		m.onChat(msg)
		return m, nil
	case chatSentMsg:
		return m, m.onChatSent(msg)
	case formDoneMsg:
		return m, m.onFormDone(msg)
	case projFormDoneMsg:
		return m, m.onProjectFormDone(msg)
	case formEditorMsg:
		m.onFormEditor(msg)
		return m, nil
	case previewMsg:
		return m, m.onPreview(msg)
	case openedMsg:
		if msg.err != nil {
			m.setToast("✗ could not open "+msg.what+": "+msg.err.Error(), true)
		}
		return m, nil
	case bellMsg:
		return m, tea.Raw("\a")
	case actionMsg:
		if msg.err != nil {
			m.setToast("✗ "+msg.err.Error(), true)
		} else {
			m.setToast("✓ "+msg.text, false)
		}
		cmds := []tea.Cmd{m.loadOverview()}
		if m.board.detailID != "" {
			cmds = append(cmds, m.loadDetail(m.board.detailID))
		}
		return m, tea.Batch(cmds...)
	case tea.PasteMsg:
		if m.projForm != nil {
			return m, m.projForm.paste(msg)
		}
		if m.form != nil {
			var cmd tea.Cmd
			switch m.form.focus {
			case fTitle:
				m.form.title, cmd = m.form.title.Update(msg)
			case fDesc:
				m.form.desc, cmd = m.form.desc.Update(msg)
			}
			return m, cmd
		}
		if m.tab == tabChat && m.chat.typing {
			var cmd tea.Cmd
			m.chat.ti, cmd = m.chat.ti.Update(msg)
			return m, cmd
		}
		if m.input != nil {
			var cmd tea.Cmd
			m.input.ti, cmd = m.input.ti.Update(msg)
			return m, cmd
		}
		if m.palette != nil {
			var cmd tea.Cmd
			m.palette.ti, cmd = m.palette.ti.Update(msg)
			return m, cmd
		}
	case tea.MouseClickMsg:
		return m, m.onClick(msg.Mouse())
	case tea.MouseWheelMsg:
		k := "j"
		if msg.Mouse().Button == tea.MouseWheelUp {
			k = "k"
		}
		return m, m.onKey(keyPress(k))
	case tea.KeyPressMsg:
		return m, m.onKey(msg)
	}
	return m, nil
}

func keyPress(k string) tea.KeyPressMsg {
	r := []rune(k)
	return tea.KeyPressMsg{Code: r[0], Text: k}
}

func (m *Model) onOverview(msg overviewMsg) tea.Cmd {
	if msg.err != nil {
		m.ovErr = msg.err
		m.fails++
		return nil
	}
	m.ov, m.ovErr, m.loaded, m.lastOK, m.fails = msg.ov, nil, true, m.now(), 0
	m.board.clamp(m)
	m.inbox.clamp(m)
	return tea.Batch(m.syncRunTails(), m.syncErrs(), m.loadChats())
}

// syncRunTails follows the steps of every live run (cards, the agent tree and the activity stream) and
// stops the tails of runs that ended.
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
				m.c.StreamRun(ctx, id, 0, 20, func(e StreamEvent) {
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
			delete(m.runCtx, id)
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
			return m.addFeed(*e.Feed)
		}
	case strings.HasPrefix(e.Stream, "tail:"):
		if e.Run != nil {
			id := strings.TrimPrefix(e.Stream, "tail:")
			key := fmt.Sprintf("%s:%d", id, e.Run.Seq)
			if m.actSeen[key] {
				return nil
			}
			m.actSeen[key] = true
			c := m.runCtx[id]
			c.observe(e.Run.Envelope)
			m.runCtx[id] = c
			lines := describeRunEvent(e.Run.Envelope)
			agent := m.runAgent(id)
			task := ""
			for _, p := range m.ov.Status {
				for _, r := range p.Runs {
					if r.ID == id {
						task = r.TaskID
					}
				}
			}
			project := ""
			if t, ok := m.taskByID(task); ok {
				project = t.ProjectID
			}
			var recount tea.Cmd
			for _, l := range lines {
				if l.Kind == lineInfo {
					continue
				}
				if (l.Kind == lineRefusal || l.Kind == lineError) && recount == nil && m.now().Sub(m.errAt[id]) > 2*time.Second {
					recount = m.loadErrs(id) // a new error: the badge follows at once
				}
				if agent != "" {
					m.agentLast[agent] = l
				}
				m.addActivity(actRow{At: l.At, Agent: agent, Tool: l.Tool, Arg: l.Arg, Kind: l.Kind, Project: project, Task: task, Run: id})
			}
			return recount
		}
	case strings.HasPrefix(e.Stream, "run:"):
		return m.run.onStream(m, e)
	case strings.HasPrefix(e.Stream, "tx:"):
		return m.tx.onStream(m, e)
	}
	return nil
}

// addFeed keeps a feed row; a fresh attention row shows a toast and notifies.
func (m *Model) addFeed(item storage.FeedItem) tea.Cmd {
	if item.Sequence <= m.feedSeq {
		return nil
	}
	m.feedSeq = item.Sequence
	m.feed = append(m.feed, item)
	if len(m.feed) > feedKeep {
		m.feed = append([]storage.FeedItem(nil), m.feed[len(m.feed)-feedKeep:]...)
	}
	m.addActivity(feedActivity(item))
	// A fresh attention event (not the replay of history) slides in as a toast.
	if item.Attention && m.now().Sub(item.CreatedAt) < 2*time.Minute && m.now().Sub(m.startAt) > 3*time.Second {
		text := "⚠ " + strings.TrimPrefix(item.Type, "task.") + " · " + oneLine(item.Summary)
		m.toasts = append(m.toasts, toastItem{text: text, at: m.now()})
		if len(m.toasts) > 3 {
			m.toasts = m.toasts[len(m.toasts)-3:]
		}
		return m.notif.item(item, m.now())
	}
	return nil
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

// liveRunOf is the live run of a task, if any.
func (m *Model) liveRunOf(task string) (string, bool) {
	for _, p := range m.ov.Status {
		for _, r := range p.Runs {
			if r.TaskID == task {
				return r.ID, true
			}
		}
	}
	return "", false
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
	ti.SetWidth(max(min(m.width-14, 66), 10))
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
	if t == tabCosts && m.costs.view == nil && !m.costs.loading {
		return m.loadCosts()
	}
	return nil
}

// filtering: a view's own text input has the keys.
func (m *Model) filtering() bool {
	return m.tab == tabBoard && m.board.editing || m.tab == tabFeed && m.feedv.editing || m.tab == tabChat && m.chat.typing
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
				m.setToast("✗ "+err.Error(), true)
				return nil
			}
			if m.input == in {
				m.input = nil
			}
			if in.local {
				return nil // a view setting, applied by apply (which may open the next input)
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
			m.setToast("… "+a.Describe(), false)
			return m.send(a)
		case "n", "N", "esc", "q":
			m.confirm = nil
			m.setToast("cancelled", false)
		}
		return nil
	}
	if m.form != nil {
		return m.form.key(m, k)
	}
	if m.projForm != nil {
		return m.projForm.key(m, k)
	}
	if m.preview != nil {
		return m.preview.key(m, key)
	}
	if m.palette != nil {
		return m.palette.key(m, k)
	}
	if m.chooser != nil {
		return m.chooser.key(m, key)
	}
	if m.help {
		m.help = false
		return nil
	}
	if !m.filtering() {
		switch key {
		case "q":
			if m.tab == tabBoard && m.board.open {
				break
			}
			m.cancel()
			return tea.Quit
		case "?":
			m.help = true
			return nil
		case "ctrl+n":
			return m.openProjectForm()
		case "ctrl+k", ":":
			m.openPalette()
			return m.palette.ti.Focus()
		case "tab":
			return m.switchTab((m.baseTab() + 1) % tabCount)
		case "shift+tab":
			return m.switchTab((m.baseTab() + tabCount - 1) % tabCount)
		case "1", "2", "3", "4", "5", "6", "7", "8":
			return m.switchTab(tab(key[0] - '1'))
		case "]":
			m.project = m.nextProject(m.project)
			m.board.clamp(m)
			return nil
		case "[":
			m.project = m.prevProject(m.project)
			m.board.clamp(m)
			return nil
		case "ctrl+r", "R":
			// R replies to a notification in the prompt bar; otherwise (and ctrl+r always) it refreshes.
			if key == "R" {
				if cmd, ok := m.promptKey(key); ok {
					return cmd
				}
			}
			m.setToast("↻ refreshing", false)
			return m.loadOverview()
		case "Y", "N", "A", "M":
			if cmd, ok := m.promptKey(key); ok {
				return cmd
			}
		case "K":
			if p, stopped := m.companyStopped(); stopped {
				m.askConfirm(Action{Kind: "start", Text: p.Reason})
				return nil
			}
			return m.openInput("⏻ Kill switch — stop the company. Reason", "", Action{Kind: "stop"}, func(a *Action, text string) error {
				a.Text = text
				return nil
			})
		}
	}
	switch m.tab {
	case tabDashboard:
		return m.dash.key(m, key)
	case tabBoard:
		return m.board.key(m, k)
	case tabRun:
		return m.run.key(m, key)
	case tabFlow:
		return m.flow.key(m, k)
	case tabFeed:
		return m.feedv.key(m, k)
	case tabInbox:
		return m.inbox.key(m, key)
	case tabChat:
		return m.chat.key(m, k)
	case tabTranscript:
		return m.tx.key(m, key)
	case tabNodes:
		return m.nodes.key(m, key)
	case tabCosts:
		return m.costs.key(m, key)
	}
	return nil
}

// baseTab is the tab under the run and transcript views.
func (m *Model) baseTab() tab {
	t := m.tab
	if t == tabTranscript {
		t = m.tx.back
	}
	if t == tabRun {
		t = m.prevTab
	}
	if t >= tabCount {
		return tabDashboard
	}
	return t
}

// View renders the screen.
func (m *Model) View() tea.View {
	v := tea.NewView(m.Render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "App Factory"
	return v
}

// Render is the screen as a string (golden tests): header, tabs, the view, modals, the prompt bar and keys.
func (m *Model) Render() string {
	w, h := m.width, m.height
	if w < 60 || h < 16 {
		return "terminal too small (60×16 at least)"
	}
	m.zones = m.zones[:0]
	footer := []string{m.footerLine(w)}
	if p := m.promptLine(w); p != "" {
		footer = append([]string{p}, footer...)
	}
	bodyH := h - 2 - len(footer)
	var body []string
	switch m.tab {
	case tabDashboard:
		body = m.dashboardLines(w, bodyH)
		m.dashZones(w, bodyH)
	case tabBoard:
		body = m.boardLines(w, bodyH)
	case tabRun:
		body = m.runLines(w, bodyH)
	case tabFlow:
		body = m.flowLines(w, bodyH)
	case tabFeed:
		body = m.feedLines(w, bodyH)
	case tabInbox:
		body = m.inboxLines(w, bodyH)
	case tabChat:
		body = m.chatLines(w, bodyH)
	case tabTranscript:
		body = m.txLines(w, bodyH)
	case tabNodes:
		body = m.nodesLines(w, bodyH)
	case tabCosts:
		body = m.costsLines(w, bodyH)
	}
	for len(body) < bodyH {
		body = append(body, "")
	}
	body = body[:bodyH]
	if modal := m.modalLines(w, bodyH); len(modal) > 0 {
		body = overlay(body, modal, w)
	}
	if t := m.toastLines(w); len(t) > 0 {
		x := w - ansi.StringWidth(t[0]) - 1
		body = spliceLines(body, t, max(x, 0), 0, w)
	}
	return m.headerLine(w) + "\n" + m.tabsLine(w) + "\n" + block(body, w, bodyH) + "\n" + strings.Join(footer, "\n")
}

// overlay draws modal centered over body.
func overlay(body, modal []string, w int) []string {
	mw := 0
	for _, l := range modal {
		mw = max(mw, ansi.StringWidth(l))
	}
	top := max((len(body)-len(modal))/2, 0)
	return spliceLines(body, modal, max((w-mw)/2, 0), top, w)[:len(body)]
}

// toastLines are the attention toasts: they slide in from the right edge and stay 6 s.
func (m *Model) toastLines(w int) []string {
	now := m.now()
	live := []toastItem{}
	for _, t := range m.toasts {
		if now.Sub(t.at) < 6*time.Second {
			live = append(live, t)
		}
	}
	m.toasts = live
	if len(live) == 0 {
		return nil
	}
	tw := min(52, w/2)
	lines := []string{}
	for _, t := range live {
		lines = append(lines, cut(stRedBold.Render(t.text), tw-4))
	}
	out := frame(stRedBold.Render("Attention"), lines, tw, len(lines)+2, theme.red, false, false)
	// Slide: the first 400 ms the toast enters from the right.
	slide := 1 - float64(now.Sub(live[len(live)-1].at))/float64(400*time.Millisecond)
	if slide > 0 {
		shift := int(slide * float64(tw))
		for i := range out {
			out[i] = ansi.Truncate(out[i], tw-shift, "")
		}
	}
	return out
}

func (m *Model) headerLine(w int) string {
	bar := lipgloss.NewStyle().Background(theme.surface)
	logo := lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(" ◆ APP FACTORY ")
	// Minutes only: an idle screen is not redrawn every second (owner-ui 6.6).
	clock := bar.Foreground(theme.dim).Render(m.now().In(m.opts.Location).Format("15:04") + " ")
	room := w - ansi.StringWidth(logo) - ansi.StringWidth(clock) - 2
	// Chips: full, then compact (no brackets, no share), then names only, until they fit.
	var chips []string
	for mode := 0; mode < 3; mode++ {
		chips = []string{m.projectChipLabel("", "all", mode)}
		for _, p := range m.projects() {
			chips = append(chips, m.projectChipLabel(p, p, mode))
		}
		if ansi.StringWidth(strings.Join(chips, " ")) <= room {
			break
		}
	}
	x := ansi.StringWidth(logo) + 1
	for i, c := range chips {
		proj := ""
		if i > 0 {
			proj = m.projects()[i-1]
		}
		cw := ansi.StringWidth(c)
		m.zones = append(m.zones, zone{x0: x, y0: 0, x1: x + cw, y1: 1, click: func(int, int) tea.Cmd { m.project = proj; m.board.clamp(m); return nil }})
		x += cw + 1
	}
	left := logo + bar.Render(" ") + strings.Join(chips, bar.Render(" "))
	if ansi.StringWidth(left)+ansi.StringWidth(clock) > w {
		left = ansi.Truncate(left, w-ansi.StringWidth(clock)-1, "…")
	}
	gap := max(w-ansi.StringWidth(left)-ansi.StringWidth(clock), 0)
	return fit(left+bar.Render(strings.Repeat(" ", gap))+clock, w)
}

// projectChipLabel is a header chip: [ ● name 33% ] — the dot is green when the project has live runs, the
// percentage its done share.
func (m *Model) projectChipLabel(p, label string, mode int) string {
	done, total, live := 0, 0, false
	for _, st := range m.ov.Status {
		if p != "" && st.Project.ID != p {
			continue
		}
		for s, n := range st.Tasks {
			total += n
			if s == "DONE" {
				done += n
			}
		}
		if len(st.Runs) > 0 {
			live = true
		}
	}
	dot := "○"
	if live {
		dot = "●"
	}
	pct := ""
	if total > 0 {
		pct = fmt.Sprintf(" %d%%", done*100/total)
	}
	open, shut := "[ ", " ]"
	switch mode {
	case 1:
		open, shut = " ", " "
	case 2:
		open, shut, pct = " ", " ", ""
	}
	text := open + dot + " " + label + pct + shut
	if p == m.project {
		return lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(text)
	}
	st := lipgloss.NewStyle().Background(theme.surface).Foreground(theme.text)
	if live {
		return st.Render(open) + st.Foreground(theme.green).Render(dot) + st.Render(" "+label+pct+shut)
	}
	return st.Foreground(theme.dim).Render(text)
}

// attentionCount is what waits on the owner: inbox items and active alerts.
func (m *Model) attentionCount() int {
	return len(m.ov.Inbox) + m.ov.Pending.Alerts + m.ov.Pending.Inspect
}

// tabLabel is a tab's label: "1 Dashboard", "1 Dash", or "1" (mode 2, but the current tab).
func (m *Model) tabLabel(i int, cur tab, mode int) string {
	switch {
	case mode == 0:
		return fmt.Sprintf("%d %s", i+1, tabNames[i])
	case mode == 2 && tab(i) != cur:
		return fmt.Sprint(i + 1)
	}
	return fmt.Sprintf("%d %s", i+1, tabShort[i])
}

// tabBadge is what a tab counts: inbox items, unread chat messages, nodes that need care.
func (m *Model) tabBadge(t tab) string {
	switch t {
	case tabNodes:
		if n := m.nodesNeedingCare(); n > 0 {
			return fmt.Sprintf(" ⚠%d", n)
		}
	case tabInbox:
		if len(m.ov.Inbox) > 0 {
			return fmt.Sprintf(" %d", len(m.ov.Inbox))
		}
	case tabChat:
		if n := m.unreadTotal(); n > 0 {
			return fmt.Sprintf(" •%d", n)
		}
	}
	return ""
}

// statusRight is the right of the tabs line: connection (with Core's URL), company, run state, attention.
func (m *Model) statusRight(withURL bool) string {
	right := []string{}
	switch {
	case m.ovErr != nil && !m.loaded:
		right = append(right, stRedBold.Render("○ unreachable"))
	case m.ovErr != nil:
		right = append(right, stYellow.Render("◐ retrying · "+ago(m.now(), m.lastOK)+" old"))
	case !m.loaded:
		right = append(right, stFaint.Render("◌ connecting"))
	default:
		feed := stGreen.Render("●")
		if m.feedSt.seen && !m.feedSt.connected {
			feed = stYellow.Render("◐")
		}
		if withURL {
			feed += stFaint.Render(" " + m.c.Describe())
		}
		right = append(right, feed)
	}
	if c := m.ov.Health.Company; c != "" {
		right = append(right, stBold.Render(c))
	}
	if p, stopped := m.companyStopped(); stopped {
		right = append(right, lipgloss.NewStyle().Background(theme.red).Foreground(theme.onAccent).Bold(true).Render(" ■ STOPPED "+p.Reason+" "))
	} else if m.loaded {
		right = append(right, stGreen.Render("▶ running"))
	}
	if n := m.attentionCount(); n > 0 {
		right = append(right, stRedBold.Render(fmt.Sprintf("⚠ %d", n)))
	}
	return strings.Join(right, stDim.Render(" │ ")) + " "
}

func (m *Model) tabsLine(w int) string {
	parts := []string{}
	cur := m.baseTab()
	x := 1
	// Full names, short names, then only the current tab named: the first that leaves the status room.
	mode := 0
	for ; mode < 2; mode++ {
		n := 1
		for i := range tabNames {
			n += ansi.StringWidth(m.tabLabel(i, cur, mode)+m.tabBadge(tab(i))) + 2
		}
		// Full names leave the whole status; short names may cut its start (the run state and ⚠ stay).
		reserve := ansi.StringWidth(m.statusRight(false))
		if mode == 1 && m.ovErr == nil { // a connection problem is never cut
			reserve = min(reserve, 22)
		}
		if n+reserve <= w {
			break
		}
	}
	for i := range tabNames {
		label, badge := m.tabLabel(i, cur, mode), m.tabBadge(tab(i))
		var s string
		if tab(i) == cur {
			s = lipgloss.NewStyle().Background(theme.accent).Foreground(theme.onAccent).Bold(true).Render(" " + label + badge + " ")
		} else {
			s = " " + stFaint.Render(label) + stRedBold.Render(badge) + " "
		}
		t := tab(i)
		m.zones = append(m.zones, zone{x0: x, y0: 1, x1: x + ansi.StringWidth(s), y1: 2, click: func(int, int) tea.Cmd { return m.switchTab(t) }})
		x += ansi.StringWidth(s)
		parts = append(parts, s)
	}
	left := " " + strings.Join(parts, "")
	if m.tab == tabRun {
		left += stAccent.Render("  ▸ run " + m.run.id)
	}
	if m.tab == tabTranscript {
		left += stAccent.Render("  ▸ transcript " + m.tx.id)
	}
	r := m.statusRight(w >= 110)
	room := w - ansi.StringWidth(left) - 1
	if ansi.StringWidth(r) > room {
		r = ansi.TruncateLeft(r, ansi.StringWidth(r)-max(room, 0)+1, "…") // the "…" takes a cell
	}
	return rightAlign(left, r, w)
}

// footerLine shows the keys of the current view, or the last action's result.
func (m *Model) footerLine(w int) string {
	if m.toast != "" && m.now().Sub(m.toastAt) < 8*time.Second {
		st := lipgloss.NewStyle().Foreground(theme.green)
		if m.toastEr {
			st = stRedBold
		}
		return fit(" "+st.Render(m.toast), w)
	}
	parts := []string{}
	for _, h := range m.hints() {
		parts = append(parts, stAccent.Bold(true).Render(h[0])+" "+stFaint.Render(h[1]))
	}
	return fit(" "+strings.Join(parts, stDim.Render("  ")), w)
}

func (m *Model) hints() [][2]string {
	switch {
	case m.form != nil:
		return formHint()
	case m.projForm != nil:
		return [][2]string{{"tab", "next field"}, {"ctrl+s", "create"}, {"esc", "cancel"}}
	case m.preview != nil:
		return [][2]string{{"j/k", "scroll"}, {"o", "open"}, {"y/n", "approve/reject"}, {"esc", "close"}}
	case m.input != nil:
		return [][2]string{{"enter", "ok"}, {"esc", "cancel"}}
	case m.confirm != nil:
		return [][2]string{{"y", "confirm"}, {"n", "cancel"}}
	case m.palette != nil:
		return [][2]string{{"type", "search"}, {"↑↓", "choose"}, {"enter", "go"}, {"esc", "close"}}
	case m.chooser != nil:
		return [][2]string{{"↑↓", "choose"}, {"enter", "pick"}, {"esc", "close"}}
	case m.help:
		return [][2]string{{"any key", "close"}}
	}
	common := [][2]string{{"^k", "palette"}, {"[ ]", "project"}, {"?", "help"}, {"q", "quit"}}
	switch m.tab {
	case tabDashboard:
		return append([][2]string{{"h/l", "panel"}, {"j/k", "move"}, {"enter", "open"}, {"z", "zoom"}}, common...)
	case tabBoard:
		if m.board.open {
			return [][2]string{{"j/k", "scroll"}, {"p/u", "pause/resume"}, {"r", "retry"}, {"x", "cancel"}, {"d", "direct"}, {"L", "live"}, {"esc", "close"}}
		}
		return append([][2]string{{"←→↑↓", "move"}, {"enter", "open"}, {"n", "new"}, {"/", "filter"}, {"!", "needs you"}, {"L", "live"}}, common...)
	case tabRun:
		return append([][2]string{{"t", "transcript"}, {"e", "errors"}, {"f", "follow"}, {"j/k", "scroll"}, {"T", "task"}, {"esc", "back"}}, common...)
	case tabTranscript:
		return [][2]string{{"j/k", "entry"}, {"enter", "expand"}, {"E", "all"}, {"e", "errors only"}, {"l", "local/core"}, {"f", "follow"}, {"T", "task"}, {"esc", "back"}}
	case tabChat:
		if m.chat.typing {
			return [][2]string{{"enter", "send"}, {"@name", "address"}, {"esc", "stop typing"}}
		}
		return append([][2]string{{"j/k", "report"}, {"enter", "write"}, {"pgup/dn", "scroll"}, {"t", "reply run"}}, common...)
	case tabFlow:
		return append([][2]string{{"g", "lanes/pairs"}, {"a", "agent"}, {"t", "task"}, {"x", "clear"}, {"j/k", "scroll"}}, common...)
	case tabFeed:
		return append([][2]string{{"!", "attention"}, {"/", "filter"}, {"j/k", "scroll"}, {"G", "newest"}}, common...)
	case tabInbox:
		if msg, ok := m.inboxSelected(); ok && !answerable(msg) && msg.ApprovalID == "" {
			return append([][2]string{{"↑↓", "item"}, {"r", "reply"}, {"m", "mark read"}, {"t", "task"}}, common...)
		}
		return append([][2]string{{"↑↓", "item"}, {"←→", "option"}, {"enter", "answer"}, {"a", "text"}, {"v", "preview"}, {"y/n", "approve"}}, common...)
	case tabNodes:
		return append([][2]string{{"h/l", "nodes/pauses"}, {"j/k", "move"}, {"p", "pause…"}, {"u", "resume"}, {"K", "kill switch"}}, common...)
	case tabCosts:
		return append([][2]string{{"r", "range"}}, common...)
	}
	return common
}

// modalLines is the open modal: help, palette, chooser, an input, a confirmation or the task detail.
func (m *Model) modalLines(w, h int) []string {
	mw := min(w-4, 76)
	switch {
	case m.form != nil:
		return m.formLines(w, h)
	case m.projForm != nil:
		return m.projFormLines(w, h)
	case m.preview != nil:
		return m.previewLines(w, h)
	case m.confirm != nil:
		lines := []string{}
		for _, l := range wrap(capitalize(m.confirm.Describe())+"?", mw-4) {
			lines = append(lines, stBold.Render(l))
		}
		yes := lipgloss.NewStyle().Background(theme.green).Foreground(theme.onAccent).Bold(true).Render(" y  confirm ")
		no := lipgloss.NewStyle().Background(theme.surfaceHi).Foreground(theme.text).Render(" n  cancel ")
		lines = append(lines, "", yes+"  "+no)
		border := theme.yellow
		if m.confirm.Kind == "stop" || m.confirm.Kind == "cancel" || m.confirm.Kind == "reject" || m.confirm.Urgent {
			border = theme.red
		}
		return frame(stYellow.Bold(true).Render("Confirm"), lines, mw, len(lines)+2, border, false, false)
	case m.input != nil:
		lines := []string{}
		for _, l := range wrap(m.input.label, mw-4) {
			lines = append(lines, stBold.Render(l))
		}
		lines = append(lines, "", m.input.ti.View())
		return frame(stTitle.Render("Input"), lines, mw, len(lines)+2, theme.accent, true, false)
	case m.palette != nil:
		return m.paletteLines(mw, h)
	case m.chooser != nil:
		return m.chooserLines(mw)
	case m.help:
		lines := m.helpLines(mw - 4)
		return frame(stTitle.Render("Keys"), lines, mw, min(len(lines)+2, h), theme.accent, true, false)
	case m.tab == tabBoard && m.board.open:
		return m.detailModal(w, h)
	}
	return nil
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
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

func (m *Model) prevProject(cur string) string {
	ps := m.projects()
	if cur == "" {
		if len(ps) > 0 {
			return ps[len(ps)-1]
		}
		return ""
	}
	for i, p := range ps {
		if p == cur && i > 0 {
			return ps[i-1]
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

// notLoaded is the body while there is no data yet.
func (m *Model) notLoaded() []string {
	if m.ovErr != nil {
		return []string{"", "  " + stRedBold.Render("○ Core is unreachable: ") + m.ovErr.Error(), "",
			stFaint.Render("  Retrying every few seconds. Check FACTORY_URL and FACTORY_TOKEN, or try factory ui --demo.")}
	}
	return []string{"", "  " + m.spin.View() + " Loading…"}
}

func (m *Model) onClick(ms tea.Mouse) tea.Cmd {
	if ms.Button != tea.MouseLeft {
		return nil
	}
	if m.input != nil || m.confirm != nil || m.palette != nil || m.chooser != nil || m.form != nil || m.projForm != nil || m.preview != nil {
		return nil
	}
	m.help = false
	for i := len(m.zones) - 1; i >= 0; i-- {
		z := m.zones[i]
		if ms.X >= z.x0 && ms.X < z.x1 && ms.Y >= z.y0 && ms.Y < z.y1 {
			return z.click(ms.X-z.x0, ms.Y-z.y0)
		}
	}
	return nil
}
