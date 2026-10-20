package tui

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"appfactory/internal/storage"
)

// Notifications (--notify): a new attention item for the owner (a question, an approval, an escalation)
// shows the toast and, with desktop, a macOS notification (osascript); elsewhere, or when osascript fails,
// the terminal bell. Each item notifies once; at most one notification per notifyGap (the rest are counted
// into the next one).

// Notify modes.
const (
	NotifyDesktop = "desktop"
	NotifyBell    = "bell"
	NotifyOff     = "off"
)

const notifyGap = 10 * time.Second

// CommandRunner runs a command (tests replace it).
type CommandRunner func(ctx context.Context, name string, args ...string) error

func execRunner(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

type notifier struct {
	mode    string
	run     CommandRunner
	goos    string
	seen    map[string]bool
	last    time.Time
	pending int // items held back by the rate limit
}

func newNotifier(mode string, run CommandRunner) *notifier {
	if mode == "" {
		mode = NotifyDesktop
	}
	if run == nil {
		run = execRunner
	}
	return &notifier{mode: mode, run: run, goos: runtime.GOOS, seen: map[string]bool{}}
}

// notifiable: an attention item that waits on the owner.
func notifiable(item storage.FeedItem) bool {
	if !item.Attention {
		return false
	}
	t := item.Type
	return strings.HasPrefix(t, "message.") || strings.HasPrefix(t, "approval.") || strings.HasPrefix(t, "task.escalation.")
}

// notifyTitle names the kind of item.
func notifyTitle(item storage.FeedItem) string {
	switch {
	case strings.HasPrefix(item.Type, "approval."):
		return "Approval requested"
	case strings.HasPrefix(item.Type, "task.escalation."):
		return "Escalation"
	}
	return "Question for you"
}

// osaQuote is s as an AppleScript string literal.
func osaQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// item decides what a fresh attention item notifies: nil when it is off, seen, rate-limited or not for the
// owner; otherwise the command that notifies.
func (n *notifier) item(item storage.FeedItem, now time.Time) tea.Cmd {
	if n == nil || n.mode == NotifyOff || !notifiable(item) {
		return nil
	}
	key := item.EventID
	if key == "" {
		key = fmt.Sprintf("%s:%d", item.Type, item.Sequence)
	}
	if n.seen[key] {
		return nil
	}
	n.seen[key] = true
	if !n.last.IsZero() && now.Sub(n.last) < notifyGap {
		n.pending++
		return nil
	}
	n.last = now
	body := clip(oneLine(item.Summary), 200)
	if n.pending > 0 {
		body += fmt.Sprintf(" (+%d more)", n.pending)
		n.pending = 0
	}
	if n.mode == NotifyBell || n.goos != "darwin" {
		return tea.Raw("\a")
	}
	title, sub := notifyTitle(item), item.ProjectID
	run := n.run
	script := "display notification " + osaQuote(body) + " with title " + osaQuote("App Factory · "+title)
	if sub != "" {
		script += " subtitle " + osaQuote(sub)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := run(ctx, "osascript", "-e", script); err != nil {
			return bellMsg{}
		}
		return nil
	}
}

// bellMsg: the desktop notification failed; ring the bell instead.
type bellMsg struct{}
