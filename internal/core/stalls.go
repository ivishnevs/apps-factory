package core

import (
	"context"
	"fmt"
	"time"

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
	stalled, idle := []storage.Alert{}, []storage.Alert{}
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
				stalled = append(stalled, storage.Alert{TaskID: t.ID, RefID: d.ID, Summary: fmt.Sprintf("waits on %s, which %s", d.ID, why)})
			}
		}
		if v := c.evaluate(s); v.Reason == tasks.ReasonIdle && t.Assignee != "" && now.Sub(time.Unix(t.UpdatedAt, 0)) >= idleAlertAfter {
			idle = append(idle, storage.Alert{TaskID: t.ID, Summary: fmt.Sprintf("idle for %s: %s; %s: %s", now.Sub(time.Unix(t.UpdatedAt, 0)).Round(time.Hour), v.Detail, v.Clearer.Who, v.Clearer.Action)})
		}
	}
	if err = c.Store.SyncAlerts(ctx, company, "stalled", stalled); err != nil {
		return err
	}
	return c.Store.SyncAlerts(ctx, company, "idle", idle)
}
