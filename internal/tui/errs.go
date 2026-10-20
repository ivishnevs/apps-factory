package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Tool errors per run (badges ✗N on agent tree rows, task cards and run headers): counted from the run's
// full transcript (tallyErrors). Live runs are recounted every errRefresh and at once when their tail shows
// an error or a refusal; ended runs are counted once when a view needs them (task detail, a run view).

const errRefresh = 15 * time.Second

type errsMsg struct {
	id    string
	tally errTally
	err   error
}

// noteErrors records a run's tally.
func (m *Model) noteErrors(id string, t errTally) {
	m.errs[id] = t
	m.errAt[id] = m.now()
}

// loadErrs counts a run's tool errors from Core's events and tool call records.
func (m *Model) loadErrs(id string) tea.Cmd {
	m.errAt[id] = m.now() // in flight: no second read
	c, ctx := m.c, m.ctx
	return func() tea.Msg {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		events, err := c.RunEvents(rctx, id)
		if err != nil {
			return errsMsg{id: id, err: err}
		}
		calls, _ := c.ToolCalls(rctx, id)
		return errsMsg{id: id, tally: tallyErrors(transcriptFromEvents(events, calls))}
	}
}

func (m *Model) onErrs(msg errsMsg) {
	if msg.err == nil {
		m.errs[msg.id] = msg.tally
	}
}

// syncErrs recounts the live runs whose count is older than errRefresh (or marked stale).
func (m *Model) syncErrs() tea.Cmd {
	cmds := []tea.Cmd{}
	for _, r := range m.liveRuns() {
		if at, ok := m.errAt[r.ID]; !ok || m.now().Sub(at) >= errRefresh {
			cmds = append(cmds, m.loadErrs(r.ID))
		}
	}
	return tea.Batch(cmds...)
}

// ensureErrs counts runs that have no count yet (ended runs: once).
func (m *Model) ensureErrs(ids ...string) tea.Cmd {
	cmds := []tea.Cmd{}
	for _, id := range ids {
		if _, ok := m.errAt[id]; id != "" && !ok {
			cmds = append(cmds, m.loadErrs(id))
		}
	}
	return tea.Batch(cmds...)
}

// runErrs is a run's count, if known.
func (m *Model) runErrs(id string) (errTally, bool) {
	t, ok := m.errs[id]
	return t, ok && id != ""
}

// taskErrs is the count of a task's live run, else of its current or last run.
func (m *Model) taskErrs(t Task) (errTally, bool) {
	if id, ok := m.liveRunOf(t.ID); ok {
		return m.runErrs(id)
	}
	if t.CurrentRunID != "" {
		return m.runErrs(t.CurrentRunID)
	}
	return errTally{}, false
}
