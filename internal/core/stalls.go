package core

import (
	"context"
	"fmt"
	"slices"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// Stall detection (reliable-recovery D8, diagnostic part): Core raises one attention alert per
// episode for work that cannot move on its own.
const (
	stallSweepEvery = time.Minute
	idleAlertAfter  = 24 * time.Hour
	// noProgressAlertAfter is how long a task's runs may go without progress before its manager is alerted.
	noProgressAlertAfter = 8 * time.Hour
)

func (c *Core) sweepStalls(ctx context.Context) error {
	now := c.Store.Now()
	if !c.lastStallSweep.IsZero() && now.Sub(c.lastStallSweep) < stallSweepEvery {
		return nil
	}
	c.lastStallSweep = now
	company := c.company()
	if _, err := c.Store.AutoForward(ctx, company); err != nil {
		return err
	}
	snaps, err := c.Store.TaskSnapshots(ctx, company, "")
	if err != nil {
		return err
	}
	open := map[string]tasks.Task{}
	for _, s := range snaps {
		open[s.Task.ID] = s.Task
	}
	stalled, idle := []storage.EscalationWant{}, []storage.Alert{}
	for _, s := range snaps {
		t := s.Task
		if t.Status == "BACKLOG" || t.Kind == tasks.KindConversation { // a conversation waits for input by design
			continue
		}
		for _, d := range s.Dependencies {
			why := ""
			switch {
			case d.Status == "CANCELLED":
				why = "was cancelled; this task can never run until the dependency is replaced"
			case d.Status == "BACKLOG":
				why = "is in BACKLOG"
			case d.Status != "DONE" && open[d.ID].Assignee == "":
				why = "has no assignee"
			}
			if why != "" {
				stalled = append(stalled, storage.EscalationWant{TaskID: t.ID, RefID: d.ID, Summary: fmt.Sprintf("waits on %s, which %s", d.ID, why)})
			}
		}
		if v := c.evaluate(s); v.Reason == tasks.ReasonIdle && t.Assignee != "" && now.Sub(time.Unix(t.UpdatedAt, 0)) >= idleAlertAfter {
			idle = append(idle, storage.Alert{TaskID: t.ID, Summary: fmt.Sprintf("idle for %s: %s; %s: %s", now.Sub(time.Unix(t.UpdatedAt, 0)).Round(time.Hour), v.Detail, v.Clearer.Who, v.Clearer.Action)})
		}
	}
	for i := range stalled {
		if t, err := c.Store.Task(ctx, company, stalled[i].TaskID); err == nil {
			stalled[i].Target = c.escalationTarget(t.Assignee)
		}
	}
	// Stalled waits are escalations now (answers unblock, cancel); alerts of the earlier kind clear.
	if err = c.Store.SyncEscalations(ctx, company, "stalled", stalled); err != nil {
		return err
	}
	if err = c.Store.SyncAlerts(ctx, company, "stalled", nil); err != nil {
		return err
	}
	pinned, err := c.Store.PinnedUnavailable(ctx, company, c.needs)
	if err != nil {
		return err
	}
	for i := range pinned {
		if t, err := c.Store.Task(ctx, company, pinned[i].TaskID); err == nil {
			pinned[i].Target = c.escalationTarget(t.Assignee)
		}
	}
	if err = c.Store.SyncEscalations(ctx, company, "pinned_node_unavailable", pinned); err != nil {
		return err
	}
	if err = c.Store.SyncAlerts(ctx, company, "idle", idle); err != nil {
		return err
	}
	noProgress, err := c.Store.NoProgress(ctx, company, now.Add(-noProgressAlertAfter))
	if err != nil {
		return err
	}
	return c.Store.SyncAlerts(ctx, company, "no_progress", noProgress)
}

// unmergedCheckEvery is the period of the unmerged-branch check (task-worktrees D9).
const unmergedCheckEvery = 24 * time.Hour

// checkUnmerged runs the unmerged-branch check once a day (and at the first tick after a start; the check
// records at most one event per task, so a restart repeats nothing).
func (c *Core) checkUnmerged(ctx context.Context) error {
	now := c.Store.Now()
	if !c.lastUnmergedCheck.IsZero() && now.Sub(c.lastUnmergedCheck) < unmergedCheckEvery {
		return nil
	}
	c.lastUnmergedCheck = now
	if _, err := c.Store.ReportUnmergedBranches(ctx, c.company()); err != nil {
		return err
	}
	// OpenSpec changes nobody works on (core-owned-project-data D5), for the owner's default report.
	_, err := c.Store.ReportOrphanChanges(ctx, c.company(), c.defaultReport())
	return err
}

// escalationTarget routes an escalation of a task to its assignee's manager (hierarchy: reports_to;
// reliable-recovery D5), the owner for agents reporting to the owner.
func (c *Core) escalationTarget(assignee string) string {
	if m := config.Manager(c.Agents, assignee); m != config.Owner {
		if _, ok := c.Agents[m]; ok {
			return m
		}
	}
	return config.Owner
}

// hasAuthority tells whether actor has authority over the task (hierarchy D2): the owner, an agent in the
// assignee's chain, or the assignee of the task it was delegated from (origin_task).
func (c *Core) hasAuthority(ctx context.Context, company, actor string, t tasks.Task) bool {
	if actor == "" || actor == config.Owner {
		return true
	}
	if slices.Contains(config.Chain(c.Agents, t.Assignee), actor) {
		return true
	}
	if t.OriginTask != "" {
		if o, err := c.Store.Task(ctx, company, t.OriginTask); err == nil && o.Assignee == actor {
			return true
		}
	}
	return false
}
