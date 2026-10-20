package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Timer-only waits (waits-events-cost-cli D1). factory_exit_wait parks the task until wake_at; the tick
// clears the park and adds a wait_ended trigger. Consecutive waits without progress form an episode
// (wait_episodes): a new wait cannot pass the episode's first deadline, and more than max_waits waits, or
// the deadline passing while the task waits, escalate to the assignee's manager.

// MaxWaitAhead is how far ahead a wait's deadline may be.
const MaxWaitAhead = 14 * 24 * time.Hour

// Wait escalations and their answers: extend (a new episode: the agent runs and may wait again with a new
// deadline), retry (counters reset, run now), reassign (a reassignment to the answering manager, as the reassign
// action; the owner's answer leaves the task unassigned for factory tasks reassign), cancel.
const (
	WaitDeadlineReason  = "wait_deadline"
	WaitExhaustedReason = "wait_exhausted"
)

func init() {
	for _, r := range []string{WaitDeadlineReason, WaitExhaustedReason} {
		escalationAnswers[r] = []string{"extend", "retry", "reassign", "cancel"}
	}
}

type waitEpisode struct {
	Deadline int64
	Count    int
}

func episodeTx(ctx context.Context, tx *Tx, task string) (*waitEpisode, error) {
	var e waitEpisode
	err := tx.QueryRowContext(ctx, "SELECT deadline,count FROM wait_episodes WHERE company_id=? AND task_id=?", tx.company, task).Scan(&e.Deadline, &e.Count)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &e, err
}

// endEpisode ends the task's wait episode (progress, authority input, an answered wait escalation).
func endEpisode(ctx context.Context, tx *Tx, task string) error {
	_, err := tx.ExecContext(ctx, "DELETE FROM wait_episodes WHERE company_id=? AND task_id=?", tx.company, task)
	return err
}

// runAgent is the agent configuration captured in the run's spec (zero when the run has none).
func runAgent(ctx context.Context, tx *Tx, runID string) config.Agent {
	var a config.Agent
	if spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", tx.company, runID); err == nil && len(spec.ConfigSnapshot) > 0 {
		_ = json.Unmarshal(spec.ConfigSnapshot, &a)
	}
	return a
}

// waitTimes reads wake_at (RFC 3339) or after (a Go duration) and the deadline of a wait exit.
func waitTimes(a map[string]any, now time.Time) (wake, deadline time.Time, r *ExitRefusal) {
	var err error
	switch {
	case str(a, "wake_at") != "":
		if wake, err = time.Parse(time.RFC3339, str(a, "wake_at")); err != nil {
			return wake, deadline, refuse("invalid_exit", "wake_at must be RFC 3339, e.g. "+now.Add(30*time.Minute).UTC().Format(time.RFC3339))
		}
	case str(a, "after") != "":
		d, err := time.ParseDuration(str(a, "after"))
		if err != nil {
			return wake, deadline, refuse("invalid_exit", "after must be a duration such as 30m or 2h")
		}
		wake = now.Add(d)
	default:
		return wake, deadline, refuse("invalid_exit", "wake_at (RFC 3339) or after (duration) required")
	}
	if str(a, "deadline") == "" {
		return wake, deadline, refuse("deadline_required", "a wait needs a deadline (RFC 3339): when waiting longer no longer makes sense and your manager must decide")
	}
	if deadline, err = time.Parse(time.RFC3339, str(a, "deadline")); err != nil {
		return wake, deadline, refuse("deadline_required", "deadline must be RFC 3339")
	}
	return wake, deadline, nil
}

// checkWait applies the wait limits to a wait exit; nothing changes.
func checkWait(ctx context.Context, tx *Tx, runID string, t tasks.Task, a map[string]any) error {
	now := tx.now()
	wake, deadline, r := waitTimes(a, now)
	if r != nil {
		return r
	}
	interval, _ := runAgent(ctx, tx, runID).WaitPolicy()
	switch {
	case wake.Sub(now) < interval:
		return refuse("wait_too_short", fmt.Sprintf("the minimum wait is %s (min_wait_interval): wake at %s or later", interval, now.Add(interval).UTC().Format(time.RFC3339)))
	case deadline.Before(wake):
		return refuse("invalid_exit", "deadline must not be before the wake time")
	case deadline.Sub(now) > MaxWaitAhead:
		return refuse("wait_too_long", "the deadline may be at most 14 days ahead ("+now.Add(MaxWaitAhead).UTC().Format(time.RFC3339)+")")
	}
	ep, err := episodeTx(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	if ep != nil && deadline.Unix() > ep.Deadline {
		first := time.Unix(ep.Deadline, 0).UTC().Format(time.RFC3339)
		return refuse("past_episode_deadline", "consecutive waits without progress cannot pass the first deadline "+first+": wait with a deadline up to "+first+", or ask, block or hand off")
	}
	return nil
}

// applyWait parks the task until the wake time, or opens wait_exhausted when the episode used up max_waits.
func applyWait(ctx context.Context, tx *Tx, runID string, t *tasks.Task, a map[string]any) error {
	now := tx.now()
	wake, deadline, _ := waitTimes(a, now)
	_, max := runAgent(ctx, tx, runID).WaitPolicy()
	ep, err := episodeTx(ctx, tx, t.ID)
	if err != nil {
		return err
	}
	count, first := 1, deadline.Unix()
	if ep != nil {
		count, first = ep.Count+1, ep.Deadline
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO wait_episodes(company_id,task_id,deadline,count,started_at) VALUES(?,?,?,?,?)
 ON CONFLICT(company_id,task_id) DO UPDATE SET count=excluded.count`, tx.company, t.ID, first, count, now.Unix()); err != nil {
		return err
	}
	t.Status = "READY"
	if count > max {
		// Saved by the caller; the escalation needs the stored task.
		return nil
	}
	reason := fmt.Sprintf("waiting until %s (wait %d of %d, deadline %s): %s", wake.UTC().Format(time.RFC3339), count, max, time.Unix(first, 0).UTC().Format(time.RFC3339), str(a, "reason"))
	return addPark(ctx, tx, *t, tasks.ParkWait, strconv.FormatInt(wake.Unix(), 10), reason, false)
}

// waitExhausted opens the wait_exhausted escalation after a wait exit that exceeded max_waits (the task is
// saved); nothing when the wait was parked as usual.
func waitExhausted(ctx context.Context, tx *Tx, runID string, t tasks.Task, a map[string]any) error {
	_, max := runAgent(ctx, tx, runID).WaitPolicy()
	ep, err := episodeTx(ctx, tx, t.ID)
	if err != nil || ep == nil || ep.Count <= max {
		return err
	}
	return openEscalation(ctx, tx, WaitExhaustedReason, escalationAnswers[WaitExhaustedReason], EscalationWant{TaskID: t.ID,
		Summary: fmt.Sprintf("%d waits in a row without progress (max_waits %d), first deadline %s; last reason: %s", ep.Count, max, time.Unix(ep.Deadline, 0).UTC().Format(time.RFC3339), excerpt512(str(a, "reason"))),
		Target:  retryTarget(tx, t.Assignee)})
}

// interruptWait ends an active wait early: authority input overrides the park (explicit-exits-authority D7),
// and the episode ends with it.
func interruptWait(ctx context.Context, tx *Tx, t tasks.Task, actor, kind string) error {
	var active int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM task_parks WHERE company_id=? AND task_id=? AND kind=? AND status='active'", tx.company, t.ID, tasks.ParkWait).Scan(&active); err != nil || active == 0 {
		return err
	}
	if err := clearParks(ctx, tx, t, tasks.ParkWait, anyRef, "overridden"); err != nil {
		return err
	}
	if err := endEpisode(ctx, tx, t.ID); err != nil {
		return err
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "task.wait_interrupted", EntityID: t.ID, ProjectID: t.ProjectID, CreatedAt: tx.now(), TaskID: t.ID, Agent: t.Assignee, Actor: actor, Status: t.Status, Summary: "wait interrupted by " + kind + " from " + actor})
}

// ExpireWaits wakes tasks whose wait reached its time: the wait park clears with a wait_ended trigger, or,
// when the episode's deadline has passed, a wait_deadline escalation to the manager. A timer that came due
// while Core was not running fires once, on the first tick after.
func (s *Store) ExpireWaits(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := tx.now()
	rows, err := tx.QueryContext(ctx, "SELECT task_id,ref_id FROM task_parks WHERE company_id=? AND kind=? AND status='active'", company, tasks.ParkWait)
	if err != nil {
		return err
	}
	type due struct{ task, ref string }
	var wake []due
	for rows.Next() {
		var d due
		if err = rows.Scan(&d.task, &d.ref); err != nil {
			rows.Close()
			return err
		}
		if at, e := strconv.ParseInt(d.ref, 10, 64); e == nil && at <= now.Unix() {
			wake = append(wake, d)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, d := range wake {
		t, err := taskTx(ctx, tx, d.task)
		if err != nil {
			return err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkWait, d.ref, "cleared"); err != nil {
			return err
		}
		ep, err := episodeTx(ctx, tx, t.ID)
		if err != nil {
			return err
		}
		if ep != nil && now.Unix() >= ep.Deadline {
			summary := fmt.Sprintf("still waiting at the deadline %s after %d waits", time.Unix(ep.Deadline, 0).UTC().Format(time.RFC3339), ep.Count)
			if err = openEscalation(ctx, tx, WaitDeadlineReason, escalationAnswers[WaitDeadlineReason], EscalationWant{TaskID: t.ID, Summary: summary, Target: retryTarget(tx, t.Assignee)}); err != nil {
				return err
			}
			continue
		}
		if err = addTrigger(ctx, tx, t, "wait_ended", false, "system", d.ref, "wait time reached: check the outside world, then continue or wait again"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// waitAnswer applies extend and reassign to a wait escalation (handled) and ends the episode for every
// answer; retry and cancel continue as for other escalations.
func waitAnswer(ctx context.Context, tx *Tx, t tasks.Task, e Escalation, answer, actor string) (bool, error) {
	if e.Reason != WaitDeadlineReason && e.Reason != WaitExhaustedReason {
		return false, nil
	}
	if err := endEpisode(ctx, tx, t.ID); err != nil {
		return true, err
	}
	switch answer {
	case "extend":
		return true, comment(ctx, tx, t.ID, actor, "escalation "+e.Reason+": extend — continue; a new wait may set a new deadline")
	case "reassign":
		// An agent's answer is a reassignment to that manager, as with the reassign action (hierarchy D2); the
		// owner's leaves the task unassigned until factory tasks reassign names the new assignee.
		if isOwner(actor) {
			t.Assignee, t.AttemptsNoProgress, t.FailStreak, t.NotBefore = "", 0, 0, 0
			if err := comment(ctx, tx, t.ID, actor, "escalation "+e.Reason+": reassign; choose the new assignee with factory tasks reassign"); err != nil {
				return true, err
			}
			return true, saveTask(ctx, tx, &t, "task.updated")
		}
		if actor == t.Assignee {
			return true, comment(ctx, tx, t.ID, actor, "escalation "+e.Reason+": reassign — kept by "+actor)
		}
		_, err := reassignTx(ctx, tx, t, actor, actor, "escalation "+e.Reason+": reassign")
		return true, err
	}
	return false, nil
}
