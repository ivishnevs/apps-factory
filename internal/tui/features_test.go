package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"appfactory/internal/redact"
	"appfactory/internal/storage"
)

// runDeep runs a command, delivers its messages and runs what they return, depth levels down.
func runDeep(m *Model, cmd tea.Cmd, depth int) {
	if cmd == nil || depth < 0 {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			runDeep(m, c, depth)
		}
	default:
		_, next := m.Update(msg)
		runDeep(m, next, depth-1)
	}
}

func openFixture(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "transcripts", name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

// TestParseClaudeSession: a Claude Code session file gives the reasoning, the text, every tool call with its
// full input and result (paired by id), errors flagged; subagent lines, user prompts and bad lines are left out.
func TestParseClaudeSession(t *testing.T) {
	entries, err := parseClaudeSession(openFixture(t, "claude.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, e := range entries {
		kinds = append(kinds, e.Kind+":"+e.Tool)
	}
	if got := strings.Join(kinds, " "); got != "think: say: tool:Bash tool:factory_task_get tool:Read tool:Grep" {
		t.Fatal(got)
	}
	if entries[0].Text != "Store first, then the cache." || entries[1].Text != "Starting with the store layer." {
		t.Fatalf("%+v", entries[:2])
	}
	bash := entries[2]
	if bash.Brief != "$ go test ./..." || !strings.Contains(bash.Input, "# Run the tests") || !bash.Done || !bash.IsError || !strings.Contains(bash.Result, "want 3 items, got 2") {
		t.Fatalf("%+v", bash)
	}
	if mcp := entries[3]; mcp.IsError || mcp.Result != `{"id":"P1-2","status":"DONE"}` || !strings.Contains(mcp.Input, `"task": "P1-2"`) {
		t.Fatalf("%+v", mcp)
	}
	if grep := entries[5]; grep.Done || grep.Brief != `"loadItems" in src` {
		t.Fatalf("a call without a result is still running: %+v", grep)
	}
	if !entries[2].At.Equal(time.Date(2026, 10, 20, 14, 18, 10, 0, time.UTC)) {
		t.Fatal(entries[2].At)
	}
	if tally := tallyErrors(entries); tally.N != 1 || !strings.HasPrefix(tally.Last, "$ go test ./...: --- FAIL: TestStore") {
		t.Fatalf("%+v", tally)
	}
}

// TestParseCodexSession: a Codex rollout file gives the reasoning summary, the assistant's text, shell calls
// (the script of bash -lc, the exit code), MCP and custom tool calls with their outputs, and runtime errors.
func TestParseCodexSession(t *testing.T) {
	entries, err := parseCodexSession(openFixture(t, "codex.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, e := range entries {
		kinds = append(kinds, e.Kind+":"+e.Tool)
	}
	if got := strings.Join(kinds, " "); got != "think: say: tool:Bash tool:factory_message_send tool:apply_patch error:" {
		t.Fatal(got)
	}
	if entries[0].Text != "**Planning** the parser before the tests." {
		t.Fatal(entries[0].Text)
	}
	sh := entries[2]
	if sh.Brief != "$ npm test -- module_2" || !sh.IsError || !strings.Contains(sh.Result, "Tests: 1 failed") || !strings.Contains(sh.Result, "(exit 1)") {
		t.Fatalf("%+v", sh)
	}
	if mcp := entries[3]; mcp.IsError || mcp.Result != `{"id":"m-9"}` || !strings.Contains(mcp.Input, `"recipient": "lead"`) {
		t.Fatalf("%+v", mcp)
	}
	if patch := entries[4]; patch.IsError || !strings.Contains(patch.Input, "*** Update File: src/module_2.ts") || !strings.Contains(patch.Result, "M src/module_2.ts") {
		t.Fatalf("%+v", patch)
	}
	if entries[5].Text != "runtime error: stream disconnected before completion" {
		t.Fatal(entries[5].Text)
	}
}

// TestTranscriptFromCoreEvents: the demo runs' Core events give full transcripts; the tool error tally counts
// a command guard refusal and its failed result once, a Core refusal of a Factory call and the runtime's
// failed result of it once, and the exit guard's refusal.
func TestTranscriptFromCoreEvents(t *testing.T) {
	f := DemoFixture()
	entries := transcriptFromEvents(f.RunEvents["r-d1-7"], f.ToolCalls["r-d1-7"])
	var think, results int
	for _, e := range entries {
		if e.Kind == txThink {
			think++
		}
		if e.Kind == txTool && e.Done {
			results++
		}
	}
	if think != 1 || results < 7 {
		t.Fatal(think, results)
	}
	if tally := tallyErrors(entries); tally.N != 3 || !strings.Contains(tally.Last, "exit guard") {
		t.Fatalf("%+v", tally)
	}
	codex := transcriptFromEvents(f.RunEvents["r-d2-4"], nil)
	if tally := tallyErrors(codex); tally.N != 1 || !strings.Contains(tally.Last, "npm test") {
		t.Fatalf("%+v", tally)
	}
	// A started Codex item is a running call; its completion replaces it in place.
	live := transcriptFromEvents(f.RunEvents["r-lead-c1"], nil)
	if n := len(live); n != 2 || live[1].Done || live[1].Brief != "$ factory why P2-2" {
		t.Fatalf("%+v", live)
	}
	// Without Core's records a Factory call's failed result counts itself.
	if tally := tallyErrors(transcriptFromEvents(f.RunEvents["r-d1-7"], nil)); tally.N != 3 {
		t.Fatalf("%+v", tally)
	}
}

// TestLocalTranscripts: the session file is found under the Claude config dir (projects/*/<id>.jsonl) or
// CODEX_HOME (sessions/**/…<thread>….jsonl); unsafe ids and absent files are "not available here"; what is
// read has credentials redacted (credential-named environment values, token shapes).
func TestLocalTranscripts(t *testing.T) {
	root := t.TempDir()
	claudeDir, codexHome := filepath.Join(root, "claude"), filepath.Join(root, "codex")
	b, _ := os.ReadFile(filepath.Join("testdata", "transcripts", "claude.jsonl"))
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(claudeDir, "projects", "-work-project-1"), 0o700))
	must(os.WriteFile(filepath.Join(claudeDir, "projects", "-work-project-1", "sess-1.jsonl"), b, 0o600))
	cb, _ := os.ReadFile(filepath.Join("testdata", "transcripts", "codex.jsonl"))
	must(os.MkdirAll(filepath.Join(codexHome, "sessions", "2026", "10", "20"), 0o700))
	must(os.WriteFile(filepath.Join(codexHome, "sessions", "2026", "10", "20", "rollout-2026-10-20T14-23-00-thread-d2-4.jsonl"), cb, 0o600))
	lt := LocalTranscripts{ClaudeDir: claudeDir, CodexHome: codexHome}

	path, provider, err := lt.Find("claude", "sess-1")
	if err != nil || provider != "claude" || !strings.HasSuffix(path, "sess-1.jsonl") {
		t.Fatal(path, provider, err)
	}
	t.Setenv("FACTORY_TEST_API_TOKEN", "local-secret-value-42")
	entries, err := lt.Read(path, provider)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Result, "ghp_") || strings.Contains(e.Result, "local-secret-value-42") {
			t.Fatalf("a secret in the transcript: %q", e.Result)
		}
	}
	if !strings.Contains(entries[4].Result, redact.Marker) {
		t.Fatal(entries[4].Result)
	}
	if path, provider, err = lt.Find("", "thread-d2-4"); err != nil || provider != "codex" {
		t.Fatal(path, provider, err)
	}
	if entries, err = lt.Read(path, provider); err != nil || len(entries) != 6 {
		t.Fatal(err, len(entries))
	}
	for _, id := range []string{"", "../etc/passwd", "a/b", "nope"} {
		if _, _, err = lt.Find("", id); !errors.Is(err, errNoLocal) {
			t.Fatal(id, err)
		}
	}
	if _, _, err = (LocalTranscripts{ClaudeDir: claudeDir, Off: true}).Find("claude", "sess-1"); !errors.Is(err, errNoLocal) {
		t.Fatal("off still reads", err)
	}
}

// TestTranscriptView: t on a run opens its transcript; entries expand and collapse, e keeps the errors, a
// live event is appended, l without a local file says so, esc goes back to the run.
func TestTranscriptView(t *testing.T) {
	m, _ := demoModel(t, 120, 40)
	openDemoRun(m, "r-d1-7")
	run(m, key(m, "t"))
	if m.tab != tabTranscript || m.tx.id != "r-d1-7" || m.tx.run == nil {
		t.Fatal(m.tab, m.tx.id)
	}
	run(m, m.loadLocal("r-d1-7", *m.tx.run))
	s := ansi.Strip(m.Render())
	if !strings.Contains(s, "core events") || !strings.Contains(s, "✗3") || !strings.Contains(s, "local: not available here") || !strings.Contains(s, "∴ reasoning") {
		t.Fatal(s)
	}
	n := len(m.tx.visible())
	key(m, "e")
	if v := m.tx.visible(); len(v) != 5 {
		t.Fatalf("errors only: %d of %d", len(v), n)
	}
	key(m, "g")
	key(m, "enter")
	s = ansi.Strip(m.Render())
	if !strings.Contains(s, "▾ ❯ Bash") || !strings.Contains(s, "git push --force origin af/P1-3") || !strings.Contains(s, "error · 2 lines") {
		t.Fatal(s)
	}
	key(m, "enter")
	if strings.Contains(ansi.Strip(m.Render()), "error · 2 lines") {
		t.Fatal("not collapsed")
	}
	key(m, "e")
	// A live event arrives on the transcript's stream.
	ev := DemoFixture().RunEvents["r-lead-c1"][0]
	ev.Seq = 999
	m.Update(streamMsg{Stream: "tx:r-d1-7", Run: &ev})
	m.Render()
	if len(m.tx.visible()) != n+1 {
		t.Fatal(len(m.tx.visible()), n)
	}
	key(m, "l")
	if !strings.Contains(m.toast, "no local transcript") {
		t.Fatal(m.toast)
	}
	key(m, "esc")
	if m.tab != tabRun || m.run.id != "r-d1-7" {
		t.Fatal(m.tab)
	}
	// From the task detail: t opens the transcript of the live run.
	run(m, m.openDetail("P1-3"))
	run(m, key(m, "t"))
	if m.tab != tabTranscript || m.tx.id != "r-d1-7" {
		t.Fatal(m.tab, m.tx.id)
	}
	key(m, "esc")
	if m.tab != tabBoard {
		t.Fatal(m.tab)
	}
}

// TestToolErrorBadges: the counts reach the agent tree, the cards and the run header; a new error in a live
// run's tail recounts it.
func TestToolErrorBadges(t *testing.T) {
	m, _ := demoModel(t, 140, 40)
	if m.errs["r-d1-7"].N != 3 || m.errs["r-d2-4"].N != 1 || m.errs["r-rev-2"].N != 0 {
		t.Fatalf("%+v", m.errs)
	}
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "developer-1: Task 3") || !strings.Contains(s, "✗3") {
		t.Fatal(s)
	}
	onBoard(t, m, "P1-3")
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "✗ exit guard refused") {
		t.Fatal("the selected card shows its last error:", s)
	}
	// A refusal in the tail: a recount at once.
	m.errAt["r-d2-4"] = DemoAt.Add(-time.Minute)
	ev := DemoFixture().RunEvents["r-d1-7"][12]
	ev.Seq = 500
	cmd := m.onStream(StreamEvent{Stream: "tail:r-d2-4", Run: &ev}) // Update adds the bus listener
	if cmd == nil {
		t.Fatal("no recount")
	}
	if msgs := run(m, cmd); len(msgs) == 0 {
		t.Fatal("recount sent nothing")
	}
	if _, ok := m.errs["r-d2-4"]; !ok {
		t.Fatal(m.errs)
	}
}

// TestChat: the direct reports with the default marked; enter writes, enter sends to the selected report
// (with @name when it is not the default), a leading @name addresses another one, an unknown one is refused;
// unread markers clear when the conversation is shown.
func TestChat(t *testing.T) {
	m, c := demoModel(t, 120, 40)
	if n := m.unreadTotal(); n != 1 {
		t.Fatal("unread before viewing:", n)
	}
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "6 Chat •1") {
		t.Fatal(s)
	}
	press(m, "6")
	if r := m.chatReports(); len(r) != 2 || r[0] != "pm" {
		t.Fatal(r)
	}
	s := ansi.Strip(m.Render())
	if !strings.Contains(s, "● pm ★") || !strings.Contains(s, "developer-1 is on P1-3 now") || !strings.Contains(s, "waits on you") {
		t.Fatal(s)
	}
	if m.unreadTotal() != 0 {
		t.Fatal("shown but unread")
	}
	press(m, "enter")
	if !m.chat.typing {
		t.Fatal("not typing")
	}
	// Global keys are text while typing.
	typeText(m, "q1 status?")
	if m.tab != tabChat {
		t.Fatal(m.tab)
	}
	runDeep(m, key(m, "enter"), 2)
	if a := lastSent(t, c); a.Kind != "say" || a.Project != "project-1" || a.Text != "q1 status?" {
		t.Fatalf("%+v", a)
	}
	if list := m.chat.msgs["owner-pm-project-1"]; list[len(list)-1].Body != "q1 status?" {
		t.Fatal("the sent message is not shown", list)
	}
	// To lead: the address is added.
	press(m, "esc", "j", "enter")
	typeText(m, "ship it")
	run(m, key(m, "enter"))
	if a := lastSent(t, c); a.Text != "@lead ship it" {
		t.Fatalf("%+v", a)
	}
	// A leading @name switches to that report; an unknown one is refused.
	typeText(m, "@pm thanks")
	run(m, key(m, "enter"))
	if a := lastSent(t, c); a.Text != "@pm thanks" || m.chatAgent() != "pm" {
		t.Fatalf("%+v %s", a, m.chatAgent())
	}
	sent := len(c.Sent)
	typeText(m, "@qa hi")
	run(m, key(m, "enter"))
	if len(c.Sent) != sent || !strings.Contains(m.toast, "direct reports") || m.chat.ti.Value() != "@qa hi" {
		t.Fatal(m.toast, len(c.Sent))
	}
	// lead is replying in project-2.
	press(m, "esc", "]", "]", "j")
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "lead is replying") || !strings.Contains(s, "replying…") {
		t.Fatal(s)
	}
	run(m, key(m, "t"))
	if m.tab != tabTranscript || m.tx.id != "r-lead-c1" {
		t.Fatal(m.tab, m.tx.id)
	}
}

// failingClient refuses every action.
type failingClient struct{ *FixtureClient }

func (failingClient) Do(context.Context, Action) (string, error) {
	return "", errors.New("400 Bad Request: dependency P1-99 not found")
}

// TestCreateTaskForm: n opens the form on the board; empty fields are refused at their field; a filled form
// sends the create action with dependencies and ready; Core's refusal keeps the form open with the reason.
func TestCreateTaskForm(t *testing.T) {
	m, c := demoModel(t, 120, 40)
	press(m, "2", "n")
	if m.form == nil || m.form.projectID() != "project-1" {
		t.Fatal("no form")
	}
	run(m, key(m, "ctrl+s"))
	if len(c.Sent) != 0 || m.form.errs[fTitle] == "" || m.form.errs[fAssignee] == "" {
		t.Fatalf("%+v", m.form.errs)
	}
	fillDemoForm(m)
	run(m, key(m, "ctrl+s"))
	a := lastSent(t, c)
	if a.Kind != "create" || a.Project != "project-1" || a.Title != "Export items as CSV" || a.Text != "One row per item." || a.Assignee != "developer-1" || a.Priority != 7 || !a.Ready || len(a.Deps) != 1 || a.Deps[0] != "P1-3" {
		t.Fatalf("%+v", a)
	}
	if m.form != nil || !strings.Contains(m.toast, "create task") {
		t.Fatal("form still open", m.toast)
	}
	// The ready toggle and the project picker.
	press(m, "n", "shift+tab", "shift+tab", "shift+tab")
	if m.form.focus != fReady {
		t.Fatal(m.form.focus)
	}
	press(m, "space")
	if m.form.ready {
		t.Fatal("ready not toggled")
	}
	key(m, "esc")
	if m.form != nil {
		t.Fatal("esc keeps the form")
	}
	// Core refuses: the reason under the form, the form stays.
	m.c = failingClient{c}
	press(m, "n")
	fillDemoForm(m)
	run(m, key(m, "ctrl+s"))
	if m.form == nil || !strings.Contains(m.form.server, "P1-99") {
		t.Fatal("refusal not shown")
	}
	if s := ansi.Strip(m.Render()); !strings.Contains(s, "Core refused: 400 Bad Request") {
		t.Fatal(s)
	}
}

// recorder is a fake command runner.
type recorder struct {
	mu   sync.Mutex
	runs [][]string
	err  error
}

func (r *recorder) run(_ context.Context, name string, args ...string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runs = append(r.runs, append([]string{name}, args...))
	return r.err
}

func (r *recorder) all() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.runs...)
}

// TestNotifier: an owner question, approval or escalation notifies once with osascript (quoted), at most once
// per notifyGap (the rest are counted into the next); bell mode and other systems ring the bell; off and
// routine rows do nothing; a failing osascript falls back to the bell.
func TestNotifier(t *testing.T) {
	rec := &recorder{}
	n := newNotifier(NotifyDesktop, rec.run)
	n.goos = "darwin"
	at := DemoAt
	q := storage.FeedItem{Sequence: 1, EventID: "e1", Type: "message.created", ProjectID: "project-1", Attention: true, Summary: `pm → owner (question): "A" or B?`}
	cmd := n.item(q, at)
	if cmd == nil {
		t.Fatal("no notification")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("%T", msg)
	}
	got := rec.all()
	if len(got) != 1 || got[0][0] != "osascript" || got[0][1] != "-e" ||
		got[0][2] != `display notification "pm → owner (question): \"A\" or B?" with title "App Factory · Question for you" subtitle "project-1"` {
		t.Fatalf("%q", got)
	}
	if n.item(q, at.Add(time.Minute)) != nil {
		t.Fatal("the same item twice")
	}
	routine := storage.FeedItem{Sequence: 2, EventID: "e2", Type: "task.created", Attention: false}
	if n.item(routine, at.Add(time.Minute)) != nil {
		t.Fatal("a routine row notified")
	}
	// Rate limit: within notifyGap only counted; the next one says so.
	ap := storage.FeedItem{Sequence: 3, EventID: "e3", Type: "approval.requested", Attention: true, Summary: "release 1.2"}
	if n.item(ap, at.Add(2*time.Second)) != nil {
		t.Fatal("not rate-limited")
	}
	esc := storage.FeedItem{Sequence: 4, EventID: "e4", Type: "task.escalation.stalled", Attention: true, Summary: "P2-3 waits"}
	cmd = n.item(esc, at.Add(notifyGap+time.Second))
	cmd()
	if got = rec.all(); len(got) != 2 || !strings.Contains(got[1][2], "P2-3 waits (+1 more)") || !strings.Contains(got[1][2], "Escalation") {
		t.Fatalf("%q", got)
	}
	// osascript fails: the bell.
	rec.err = errors.New("exit status 1")
	cmd = n.item(storage.FeedItem{Sequence: 5, EventID: "e5", Type: "message.created", Attention: true}, at.Add(time.Hour))
	if _, ok := cmd().(bellMsg); !ok {
		t.Fatal("no bell fallback")
	}
	// Bell mode, another system: the bell, no command.
	for _, nn := range []*notifier{newNotifier(NotifyBell, rec.run), func() *notifier { x := newNotifier(NotifyDesktop, rec.run); x.goos = "linux"; return x }()} {
		before := len(rec.all())
		cmd = nn.item(q, at)
		if cmd == nil || len(rec.all()) != before {
			t.Fatal("bell")
		}
	}
	if newNotifier(NotifyOff, rec.run).item(q, at) != nil {
		t.Fatal("off notified")
	}
}

// TestNotifyFromFeed: a fresh attention row in the feed stream shows the toast and notifies.
func TestNotifyFromFeed(t *testing.T) {
	rec := &recorder{}
	now := DemoAt
	c := &FixtureClient{F: DemoFixture()}
	m := New(Options{Client: c, Now: func() time.Time { return now }, Location: time.UTC, NoStreams: true, Notify: NotifyDesktop, Run: rec.run, Local: LocalTranscripts{Off: true}})
	t.Cleanup(m.Close)
	m.notif.goos = "darwin"
	m.Update(overviewMsg{ov: c.F.Overview})
	now = now.Add(10 * time.Second)
	item := storage.FeedItem{Sequence: 900, EventID: "e900", Type: "approval.requested", ProjectID: "project-2", CreatedAt: now, Attention: true, Summary: "brand new approval"}
	cmd := m.onStream(StreamEvent{Stream: "feed", Feed: &item})
	run(m, cmd)
	if len(m.toasts) != 1 || len(rec.all()) != 1 || !strings.Contains(rec.all()[0][2], "brand new approval") {
		t.Fatal(m.toasts, rec.all())
	}
}

// TestExternalLinks: o in a task detail opens its pull request; v on an artifact approval previews the
// markdown inside the UI, o there opens the file, y decides; a binary artifact opens with the system viewer.
func TestExternalLinks(t *testing.T) {
	rec := &recorder{}
	f := DemoFixture()
	c := &FixtureClient{F: f}
	m := New(Options{Client: c, Now: func() time.Time { return DemoAt }, Location: time.UTC, NoStreams: true, Run: rec.run, Notify: NotifyOff, Local: LocalTranscripts{Off: true}})
	t.Cleanup(m.Close)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m.Update(overviewMsg{ov: f.Overview})
	run(m, m.openDetail("P2-1"))
	run(m, key(m, "o"))
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	if got := rec.all(); len(got) != 1 || got[0][0] != opener || got[0][1] != "https://github.com/company-1/project-2/pull/12" {
		t.Fatalf("%q", got)
	}
	// Without a forge URL the PR link comes from the repository.
	if u := m.prURL("project-1", &storage.PRFacts{Number: 4}); u != "https://github.com/company-1/project-1/pull/4" {
		t.Fatal(u)
	}
	run(m, m.openDetail("P1-3"))
	run(m, key(m, "o"))
	if !strings.Contains(m.toast, "has no pull request") {
		t.Fatal(m.toast)
	}
	key(m, "esc")
	press(m, "5", "j", "j")
	run(m, key(m, "v"))
	if m.preview == nil || m.preview.err != nil {
		t.Fatalf("%+v", m.preview)
	}
	s := ansi.Strip(m.Render())
	if !strings.Contains(s, "Preview · release-notes v2") || !strings.Contains(s, "• Faster sync") || strings.Contains(s, "**") {
		t.Fatal(s)
	}
	run(m, key(m, "o"))
	got := rec.all()
	if len(got) != 2 || !strings.HasSuffix(got[1][1], "release-notes.md") {
		t.Fatalf("%q", got)
	}
	b, err := os.ReadFile(got[1][1])
	if err != nil || string(b) != demoReleaseNotes {
		t.Fatal(err)
	}
	st, _ := os.Stat(got[1][1])
	if st.Mode().Perm() != 0o600 {
		t.Fatal(st.Mode())
	}
	os.RemoveAll(filepath.Dir(got[1][1]))
	key(m, "y")
	if m.preview != nil || m.input == nil || !strings.Contains(m.input.label, "Approve ap-2") {
		t.Fatal("y from the preview does not approve")
	}
	key(m, "esc")
	// A binary artifact is opened, not shown.
	c.F.Artifacts["project-2/release-notes/v2"] = "\x89PNG\r\n\x1a\n\x00\x00"
	m.Update(previewMsg{}) // a stale message changes nothing
	ap, _ := m.approvalOf(m.ov.Inbox[2])
	ap.Artifact.Name = "mockup.png"
	cmd := m.previewArtifact(ap)
	_, cmd = m.Update(cmd())
	run(m, cmd)
	if m.preview != nil {
		t.Fatal("binary shown in the UI")
	}
	if got = rec.all(); len(got) != 3 || !strings.HasSuffix(got[2][1], "mockup.png") {
		t.Fatalf("%q", got)
	}
	os.RemoveAll(filepath.Dir(got[2][1]))
}

func TestRenderMarkdown(t *testing.T) {
	lines := renderMarkdown("# Title\n\nSome **bold** and `code`, a [link](https://x.y).\n\n- [x] done\n- [ ] todo\n  - nested\n1. first\n\n```go\nfmt.Println(1)\n```\n---", 60)
	text := ansi.Strip(strings.Join(lines, "\n"))
	for _, want := range []string{" Title ", "Some bold and code, a link (https://x.y).", "☑ done", "☐ todo", "  • nested", "1.  first", "┌─ go", "│ fmt.Println(1)", "└─"} {
		if !strings.Contains(text, want) {
			t.Errorf("%q not in\n%s", want, text)
		}
	}
}
