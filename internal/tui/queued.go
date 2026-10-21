package tui

import (
	"strings"
	"time"

	"appfactory/internal/core"
	"appfactory/internal/storage"
)

// Why a run is QUEUED (owner-ui 6.1): Core's queued reason on the run (GET /status runs, the task's why),
// shown on the run, its task card and its agent's row instead of a working spinner.

// queuedRun is the QUEUED run of a task or (task "") of an agent, if any.
func (m *Model) queuedRun(task, agent string) (core.RunStatus, bool) {
	for _, p := range m.ov.Status {
		for _, r := range p.Runs {
			if r.Status == "QUEUED" && (task != "" && r.TaskID == task || task == "" && r.Agent == agent) {
				return r, true
			}
		}
	}
	return core.RunStatus{}, false
}

func (m *Model) isQueued(task string) bool {
	_, ok := m.queuedRun(task, "")
	return ok
}

// runStatusOf is a live run's status entry.
func (m *Model) runStatusOf(id string) (core.RunStatus, bool) {
	for _, p := range m.ov.Status {
		for _, r := range p.Runs {
			if r.ID == id {
				return r, true
			}
		}
	}
	return core.RunStatus{}, false
}

// queuedLabels name the reasons for the owner.
var queuedLabels = map[string]string{
	storage.QueuedPaused:         "paused",
	storage.QueuedAgentBusy:      "agent busy",
	storage.QueuedPinned:         "pinned node",
	storage.QueuedNoSlot:         "no free slot",
	storage.QueuedResourceBusy:   "resource busy",
	storage.QueuedNodePaused:     "node paused",
	storage.QueuedDriverDisabled: "driver disabled",
	storage.QueuedNodeOffline:    "node offline",
	storage.QueuedLabels:         "no node with the labels",
	storage.QueuedNoNode:         "no node",
	storage.QueuedUnclaimed:      "claim on its way",
}

// queuedText is the reason in one line: "driver disabled · mac-studio: driver claude disabled: auth check
// failed · until 14:40". Without Core's reason (an older Core) it says only that the run waits for a node.
func (m *Model) queuedText(q *storage.QueuedReason) string {
	if q == nil {
		return "waits for a node to claim it"
	}
	d := q.Detail
	// Core's RFC 3339 "(until …)" becomes the local clock.
	if i := strings.Index(d, " (until "); i >= 0 {
		if j := strings.Index(d[i:], ")"); j >= 0 {
			d = d[:i] + d[i+j+1:]
		}
	}
	label := queuedLabels[q.Reason]
	if label == "" {
		label = q.Reason
	}
	out := label + " · " + d
	if q.Until > m.now().Unix() {
		out += " · until " + m.clock(time.Unix(q.Until, 0))
	}
	return out
}

// queuedOf is the QUEUED run's reason line of a task, if its run is QUEUED.
func (m *Model) queuedOf(task string) (string, bool) {
	r, ok := m.queuedRun(task, "")
	if !ok {
		return "", false
	}
	return m.queuedText(r.Queued), true
}
