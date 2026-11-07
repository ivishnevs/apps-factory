package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	for _, company := range c.companies() {
		if err := c.isolate(ctx, company, "stall sweep", c.sweepStallsOf(ctx, company, now)); err != nil {
			return err
		}
	}
	return nil
}

// isolate keeps one company's failing background step from stopping Core (dependency-wait-escalations 0.2): the
// error is logged with the company and step and the tick goes on. Only a lost storage stays fatal.
func (c *Core) isolate(ctx context.Context, company, step string, err error) error {
	if err == nil || ctx.Err() != nil {
		return err
	}
	if perr := c.Store.DB.PingContext(ctx); perr != nil {
		return err
	}
	slog.Error("background step failed; Core goes on", "company", company, "step", step, "error", err)
	return nil
}

// syncSweepFailures raises one sweep_failed alert per failing task and episode and clears the ones that recovered.
func (c *Core) syncSweepFailures(ctx context.Context, company string, failed storage.SweepErrors) error {
	alerts := []storage.Alert{}
	seen := map[string]bool{}
	for _, f := range failed {
		slog.Error("stall sweep failed for a task; skipped", "company", company, "task", f.TaskID, "error", f.Err)
		if f.TaskID != "" && !seen[f.TaskID] {
			seen[f.TaskID] = true
			alerts = append(alerts, storage.Alert{TaskID: f.TaskID, Summary: "background sweep failed: " + f.Err.Error()})
		}
	}
	return c.Store.SyncAlerts(ctx, company, "sweep_failed", alerts)
}

// sweepStallsOf is the stall sweep of one company.
func (c *Core) sweepStallsOf(ctx context.Context, company string, now time.Time) error {
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
	backlogAfter, err := c.Store.StallBacklogAfter(ctx, company)
	if err != nil {
		return err
	}
	kept, err := c.Store.KeptWaiting(ctx, company)
	if err != nil {
		return err
	}
	// moved is when a task waited on last changed: itself or one of its open dependencies.
	moved := func(id string) time.Time {
		d := open[id]
		at := d.UpdatedAt
		for _, dd := range d.Dependencies {
			at = max(at, open[dd].UpdatedAt)
		}
		return time.Unix(at, 0)
	}
	// A planned BACKLOG wait is a stall only after it has not moved for backlogAfter (dependency-wait-escalations 1.1).
	stale := func(id string) bool { return now.Sub(moved(id)) >= backlogAfter }
	// keep_waiting snoozes (task, ref) until ref moves or another backlogAfter passes (2.1).
	snoozed := func(task, ref string) bool {
		at, ok := kept[task+"\x00"+ref]
		return ok && !moved(ref).After(at) && now.Sub(at) < backlogAfter
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
			case d.Status != "DONE" && open[d.ID].Assignee == "":
				why = "has no assignee"
			case d.Status == "BACKLOG" && stale(d.ID):
				why = "has been in BACKLOG without change for " + hoursOf(now.Sub(moved(d.ID)))
			}
			if why != "" && !snoozed(t.ID, d.ID) {
				stalled = append(stalled, storage.EscalationWant{TaskID: t.ID, RefID: d.ID, Summary: fmt.Sprintf("waits on %s%s, which %s", d.ID, titled(open[d.ID].Title), why)})
			}
		}
		// A task parked on its children (Snapshot.Children, loaded with the park) stalls on an open child that
		// cannot move on its own; a closed child releases the parent, so only open ones count.
		for _, ch := range s.Children {
			why := ""
			switch {
			case ch.Assignee == "":
				why = "has no assignee"
			case ch.Status == "BACKLOG" && stale(ch.ID):
				why = "has been in BACKLOG without change for " + hoursOf(now.Sub(moved(ch.ID)))
			}
			if why != "" && !snoozed(t.ID, ch.ID) {
				stalled = append(stalled, storage.EscalationWant{TaskID: t.ID, RefID: ch.ID, Summary: fmt.Sprintf("waits on its child %s%s, which %s", ch.ID, titled(open[ch.ID].Title), why)})
			}
		}
		if v := c.evaluateIn(company, s); v.Reason == tasks.ReasonIdle && t.Assignee != "" && now.Sub(time.Unix(t.UpdatedAt, 0)) >= idleAlertAfter {
			idle = append(idle, storage.Alert{TaskID: t.ID, Summary: fmt.Sprintf("idle for %s: %s; %s: %s", now.Sub(time.Unix(t.UpdatedAt, 0)).Round(time.Hour), v.Detail, v.Clearer.Who, v.Clearer.Action)})
		}
	}
	for i := range stalled {
		if t, err := c.Store.Task(ctx, company, stalled[i].TaskID); err == nil {
			stalled[i].Target = c.escalationTargetIn(company, t.Assignee)
		}
	}
	// Stalled waits are escalations (answers keep_waiting, drop_dependency, cancel); alerts of the earlier kind clear.
	// A task that fails to sync is reported and the rest of the sweep goes on (dependency-wait-escalations 0.2).
	var failed storage.SweepErrors
	if err = c.Store.SyncEscalations(ctx, company, "stalled", stalled); err != nil && !errors.As(err, &failed) {
		return err
	}
	if err = c.Store.SyncAlerts(ctx, company, "stalled", nil); err != nil {
		return err
	}
	pinned, err := c.Store.PinnedUnavailable(ctx, company, func(s *tasks.Snapshot) { c.needs(company, s) })
	if err != nil {
		return err
	}
	for i := range pinned {
		if t, err := c.Store.Task(ctx, company, pinned[i].TaskID); err == nil {
			pinned[i].Target = c.escalationTargetIn(company, t.Assignee)
		}
	}
	var pinFailed storage.SweepErrors
	if err = c.Store.SyncEscalations(ctx, company, "pinned_node_unavailable", pinned); err != nil && !errors.As(err, &pinFailed) {
		return err
	}
	failed = append(failed, pinFailed...)
	if err = c.syncSweepFailures(ctx, company, failed); err != nil {
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
	for _, company := range c.companies() {
		if _, err := c.Store.ReportUnmergedBranches(ctx, company); c.isolate(ctx, company, "unmerged branches", err) != nil {
			return err
		}
		// OpenSpec changes nobody works on (openspec-in-git D3), for the owner's default report.
		if _, err := c.Store.ReportOrphanChanges(ctx, company, c.defaultReport(company)); c.isolate(ctx, company, "orphan changes", err) != nil {
			return err
		}
	}
	return nil
}

// escalationTarget routes an escalation of a task to its assignee's manager (hierarchy: reports_to;
// reliable-recovery D5), the owner for agents reporting to the owner.
func (c *Core) escalationTarget(assignee string) string {
	return c.escalationTargetIn(c.company(), assignee)
}

func (c *Core) escalationTargetIn(company, assignee string) string {
	agents := c.cat(company).Agents
	if m := config.Manager(agents, assignee); m != config.Owner {
		if _, ok := agents[m]; ok {
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
	if slices.Contains(config.Chain(c.cat(company).Agents, t.Assignee), actor) {
		return true
	}
	if t.OriginTask != "" {
		if o, err := c.Store.Task(ctx, company, t.OriginTask); err == nil && o.Assignee == actor {
			return true
		}
	}
	return false
}

// titled is ` ("TITLE")` for a known task title, so an escalation says which task it waits on.
func titled(title string) string {
	if title == "" {
		return ""
	}
	return fmt.Sprintf(" (%q)", title)
}

// hoursOf is a duration in whole hours ("26h").
func hoursOf(d time.Duration) string {
	return fmt.Sprintf("%dh", int(d.Hours()))
}
