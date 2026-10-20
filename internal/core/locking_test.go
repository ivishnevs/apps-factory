package core

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"appfactory/internal/protocol"
	"appfactory/internal/storage"
	"appfactory/internal/tasks"
)

// TestParallelScheduleClaimExpire (postgres-tenancy 3.1): dispatch, claim, run ends and lease/queue expiry run
// concurrently on many connections for one company; their transactions serialize on the company lock, so none
// fails on a race (no unique violation, no deadlock) and no task ever has two live runs.
func TestParallelScheduleClaimExpire(t *testing.T) {
	c, s, fake := schedulerCore(t)
	ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	co := storage.DefaultCompany
	agents := []string{"demo", "calorie-ios", "calorie-qa", "calorie-designer"}
	for i := range 12 {
		project := "p"
		if i%2 == 1 {
			project = "q"
		}
		if i == 1 {
			if _, err := s.CreateProject(context.Background(), co, tasks.ProjectCreate{ID: "q", Name: "Q", Workspace: "/tmp/core-dispatch-q"}); err != nil {
				t.Fatal(err)
			}
		}
		id := string(rune('a' + i))
		if _, err := s.CreateTask(context.Background(), co, tasks.Create{ID: id, ProjectID: project, Title: id, Assignee: agents[i%len(agents)], Ready: true}); err != nil {
			t.Fatal(err)
		}
	}
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	loop := func(name string, step func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				if err := step(); err != nil && ctx.Err() == nil {
					errs <- errors.New(name + ": " + err.Error())
					return
				}
			}
		}()
	}
	runs := func(status string) []string {
		rows, err := s.DB.QueryContext(ctx, "SELECT id FROM runs WHERE status=? AND task_id IS NOT NULL ORDER BY seq", status)
		if err != nil {
			return nil
		}
		defer rows.Close()
		var ids []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		return ids
	}
	leases := sync.Map{}
	// Two Core replicas dispatch the company's work.
	replica := &Core{Store: s, Agents: c.Agents}
	replica.Handler()
	loop("schedule", func() error { return c.Schedule(ctx) })
	loop("schedule replica", func() error { return replica.Schedule(ctx) })
	for range 2 {
		loop("claim", func() error {
			for _, id := range runs("QUEUED") {
				lease, err := s.Claim(ctx, co, id)
				if err == nil {
					leases.Store(id, lease)
				} else if !errors.Is(err, storage.ErrConflict) && !errors.Is(err, storage.ErrNotFound) {
					return err
				}
			}
			return nil
		})
	}
	loop("end", func() error {
		for _, id := range runs("RUNNING") {
			lease, ok := leases.Load(id)
			if !ok {
				continue
			}
			e := protocol.Wrap("agent.completed", id, "", protocol.Event{RunID: id, Lease: lease.(string), Status: "ENDED", Outcome: "failed", Result: "stress"})
			if err := s.Apply(ctx, co, e); err != nil && !errors.Is(err, storage.ErrConflict) {
				return err
			}
		}
		return nil
	})
	loop("expire", func() error {
		fake.Advance(10 * time.Second)
		if err := s.Expire(ctx, co); err != nil {
			return err
		}
		if _, err := s.ExpireQueued(ctx, co); err != nil {
			return err
		}
		if _, err := s.StopIdle(ctx, co); err != nil {
			return err
		}
		return s.ExpireApprovals(ctx, co)
	})
	loop("messages", func() error { return c.ScheduleMessages(ctx) })
	checks := 0
	loop("check", func() error {
		var doubled int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT task_id FROM runs WHERE task_id IS NOT NULL
 AND (status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING') OR unreleased=1) GROUP BY task_id HAVING count(*)>1) d`).Scan(&doubled); err != nil {
			return err
		}
		if doubled > 0 {
			return errors.New("a task has two live runs")
		}
		// Every agent's cap is one live run (configs/agents): dispatch decides on the agent's live runs.
		var busy int
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT agent FROM runs WHERE task_id IS NOT NULL
 AND status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING') GROUP BY agent HAVING count(*)>1) d`).Scan(&busy); err != nil {
			return err
		}
		if busy > 0 {
			return errors.New("an agent has more live runs than its cap")
		}
		checks++
		return nil
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	var dispatched int
	_ = s.DB.QueryRow("SELECT count(*) FROM runs WHERE task_id IS NOT NULL").Scan(&dispatched)
	if dispatched < 12 || checks == 0 {
		t.Fatalf("too little happened: %d runs, %d checks", dispatched, checks)
	}
}
