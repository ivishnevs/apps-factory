package core

import (
	"context"
	"fmt"
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
)

func (c *Core) sweepStalls(ctx context.Context) error {
	now := c.Store.Now()
	if !c.lastStallSweep.IsZero() && now.Sub(c.lastStallSweep) < stallSweepEvery {
		return nil
	}
	c.lastStallSweep = now
	company := c.company()
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
		if t.Status == "BACKLOG" {
			continue // not released: nobody waits on it yet
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
	if len(stalled) > 0 {
		target, err := c.escalationTarget(ctx, company)
		if err != nil {
			return err
		}
		for i := range stalled {
			stalled[i].Target = target
		}
	}
	// Stalled waits are escalations now (answers unblock, cancel); alerts of the earlier kind clear.
	if err = c.Store.SyncEscalations(ctx, company, "stalled", stalled); err != nil {
		return err
	}
	if err = c.Store.SyncAlerts(ctx, company, "stalled", nil); err != nil {
		return err
	}
	return c.Store.SyncAlerts(ctx, company, "idle", idle)
}

// escalationTarget routes an escalation before the hierarchy exists (reliable-recovery D5): the
// configured agent with role manager that has the fewest open escalations (then by name), else the owner.
func (c *Core) escalationTarget(ctx context.Context, company string) (string, error) {
	load, err := c.Store.OpenEscalationsByTarget(ctx, company)
	if err != nil {
		return "", err
	}
	target := "owner"
	for _, name := range config.Names(c.Agents) {
		if c.Agents[name].Spec.Role != "manager" {
			continue
		}
		if target == "owner" || load[name] < load[target] {
			target = name
		}
	}
	return target, nil
}
