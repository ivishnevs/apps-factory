package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata" // company time zones on hosts without a zoneinfo database

	"appfactory/internal/config"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Automatic retry (reliable-recovery D2–D3; owner decision 2026-10-14: autonomy first). A task whose run
// ended with an attempt or lost outcome goes back to READY after a backoff and runs again in the same
// workspace, resuming the session where the node allows it. Three attempts without progress or the daily
// budget escalate instead: the task stays READY behind the escalation's park (FAILED is retired). Tasks
// with external side effects (the task's side_effects, or a recovery: inspect policy rule of the run's
// agent) are inspected instead of retried.

const (
	// RetryAttempts is the number of attempt outcomes in a row without progress that escalates.
	RetryAttempts = 3
	// DailyBudget is the number of budget-counted runs per task and day that escalates.
	DailyBudget = 30
)

// backoffSteps are the waits after 1, 2, 3+ backoff outcomes in a row.
var backoffSteps = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute}

func backoff(streak int) time.Duration {
	if streak < 1 {
		return 0
	}
	return backoffSteps[min(streak, len(backoffSteps))-1]
}

func init() {
	escalationAnswers["retries_exhausted"] = []string{"retry", "cancel"}
	escalationAnswers["budget_exhausted"] = []string{"retry", "cancel"}
	escalationAnswers["no_exit"] = []string{"retry", "reassign", "accept", "cancel"}
	escalationAnswers["inspect"] = []string{"retry", "continue", "cancel"}
}

// budgetDay is the company-local day of now (companies.time_zone, UTC when unset or unknown) that the
// daily budget counts in.
func budgetDay(ctx context.Context, tx *Tx) (string, error) {
	var zone string
	if err := tx.QueryRowContext(ctx, "SELECT time_zone FROM companies WHERE id=?", tx.company).Scan(&zone); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	return tx.now().In(loc).Format("2006-01-02"), nil
}

// CompanyTimeZone returns the company's time zone (an IANA name; UTC by default).
func (s *Store) CompanyTimeZone(ctx context.Context, company string) (string, error) {
	var zone string
	err := s.DB.QueryRowContext(ctx, "SELECT time_zone FROM companies WHERE id=?", company).Scan(&zone)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return zone, err
}

// SetCompanyTimeZone sets the company's time zone, the day of the daily budget.
func (s *Store) SetCompanyTimeZone(ctx context.Context, company, zone string) error {
	if _, err := time.LoadLocation(zone); err != nil || zone == "" || zone == "Local" {
		return inputError("time_zone must be an IANA time zone name such as Europe/Berlin or UTC")
	}
	res, err := s.DB.ExecContext(ctx, "UPDATE companies SET time_zone=? WHERE id=?", zone, company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}
	return nil
}

// NoProgress lists the open tasks whose runs have made no progress (D2) since before the given time: the
// first run after the task's last progress started then or earlier (reliable-recovery D8). The episode ends
// with progress, which re-arms the alert.
func (s *Store) NoProgress(ctx context.Context, company string, since time.Time) ([]Alert, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id,min(r.created_at),count(*) FROM tasks t JOIN runs r ON r.company_id=t.company_id AND r.task_id=t.id
 WHERE t.company_id=? AND t.status NOT IN ('DONE','CANCELLED','BACKLOG') AND r.created_at>coalesce((t.body->>'progress_at')::bigint,0)
 GROUP BY t.id HAVING min(r.created_at)<=? ORDER BY t.id`, company, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var id string
		var first int64
		var runs int
		if err = rows.Scan(&id, &first, &runs); err != nil {
			return nil, err
		}
		out = append(out, Alert{TaskID: id, Summary: fmt.Sprintf("no progress in %d runs since %s", runs, time.Unix(first, 0).UTC().Format(time.RFC3339))})
	}
	return out, rows.Err()
}

// recoverTask applies the outcome table to the task after its current run ended (called from
// syncTaskRun once the task has its status). It counts the attempt and the budget, then either schedules
// the retry (READY with not_before), escalates, or leaves the task as it is. A task still IN_PROGRESS on
// the run (or IN_REVIEW for a review run) has not had this end applied; afterwards it is READY, CANCELLED or
// parked, so a later call for the same run (late evidence, the release of a lost run) changes nothing.
func recoverTask(ctx context.Context, tx *Tx, runID string) error {
	var taskID sql.NullString
	var outcome, result string
	var unreleased bool
	var briefTo int64
	var specJSON []byte
	if err := tx.QueryRowContext(ctx, "SELECT task_id,outcome,result,unreleased,brief_to,spec FROM runs WHERE id=?", runID).Scan(&taskID, &outcome, &result, &unreleased, &briefTo, &specJSON); err != nil {
		return err
	}
	if !taskID.Valid {
		return nil
	}
	t, err := taskTx(ctx, tx, taskID.String)
	if err != nil || t.CurrentRunID != runID {
		return err
	}
	now := tx.now()
	hasExit, err := runHasExit(ctx, tx, runID)
	if err != nil {
		return err
	}
	day, err := budgetDay(ctx, tx)
	if err != nil {
		return err
	}
	if outcome == "succeeded" {
		if first, err := markRecovered(ctx, tx, runID); err != nil || !first {
			return err
		}
		if err = countBudget(ctx, tx, t.ID, day); err != nil {
			return err
		}
		// A success ends the failure streak; it resets the attempts only with progress (D2): an exit that
		// moved exit_sha, or without git facts submit, done or a handoff to another agent. Without explicit
		// exits every success counts as progress.
		progress := true
		if hasExit {
			// A failed merge already counted its attempt and backoff at the exit (task-worktrees D7).
			if failed, err := exitMergeFailed(ctx, tx, runID); err != nil || failed {
				return err
			}
			if progress, err = exitProgress(ctx, tx, runID, t.ID); err != nil {
				return err
			}
		}
		before := t
		t.FailStreak = 0
		if progress {
			t.AttemptsNoProgress, t.ProgressAt = 0, now.Unix()
		}
		if !hasExit { // an exit sets not_before itself (wait)
			t.NotBefore = 0
		}
		if t.FailStreak == before.FailStreak && t.AttemptsNoProgress == before.AttemptsNoProgress && t.NotBefore == before.NotBefore && t.ProgressAt == before.ProgressAt {
			return nil
		}
		return saveTask(ctx, tx, &t, "task.updated")
	}
	rule, ok := Outcomes[outcome]
	if !ok || t.CancelRequested || outcome == "no_exit" && t.Kind == tasks.KindConversation {
		return nil // a conversation turn without an exit handled its input (syncTaskStatus)
	}
	// A review run (the reviewer is the assignee of the task in review) keeps the task IN_REVIEW.
	review := t.Status == "IN_REVIEW" && t.Reviewer != "" && t.Assignee == t.Reviewer
	switch {
	case outcome == "no_exit", review, t.Status == "IN_PROGRESS":
	case t.Status == "BLOCKED" && strings.HasPrefix(t.BlockReason, "run:"): // held on an UNKNOWN run before 0029
	default:
		return nil // already applied, CANCELLED, or READY after a kill switch
	}
	if inspect, err := inspectsRun(ctx, tx, t, specJSON, outcome); err != nil || inspect {
		if err != nil {
			return err
		}
		// reliable-recovery D4: an unclear end of a task with external effects is inspected, never retried;
		// only this task waits (its escalation park), at once even while a lost run's node is silent.
		var seen int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM escalations WHERE company_id=? AND task_id=? AND reason='inspect' AND ref_id=?", tx.company, t.ID, runID).Scan(&seen); err != nil || seen > 0 {
			return err
		}
		if first, err := markRecovered(ctx, tx, runID); err != nil || !first {
			return err
		}
		if !review {
			t.Status, t.BlockReason = "READY", ""
		}
		t.Result = result
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return err
		}
		return openEscalation(ctx, tx, "inspect", escalationAnswers["inspect"], EscalationWant{TaskID: t.ID, RefID: runID,
			Summary: "run " + runID + " of a task with external effects ended " + outcome + ": " + excerpt512(result) + " — check whether the effect happened: retry (it did not, or it is idempotent), continue (it happened), cancel",
			Target:  retryTarget(tx, t.Assignee)})
	}
	if unreleased {
		return nil // the runtime may still run: the task waits for its node (releaseLost)
	}
	if first, err := markRecovered(ctx, tx, runID); err != nil || !first {
		return err
	}
	if err = clearParks(ctx, tx, t, tasks.ParkInspect, runID, "cleared"); err != nil {
		return err
	}
	// A loss to a new worker epoch is not the task's doing (D1): no budget.
	if rule.Budget && !strings.Contains(result, "restarted (epoch") {
		if err = countBudget(ctx, tx, t.ID, day); err != nil {
			return err
		}
	}
	if rule.Attempt {
		t.AttemptsNoProgress++
	}
	if rule.Backoff {
		t.FailStreak++
		t.NotBefore = now.Add(backoff(t.FailStreak)).Unix()
	}
	// Authority input the run was not briefed on counts as a retry answer (D2, D5): counters, backoff and
	// today's budget reset, and the task runs with that input instead of escalating.
	var fresh bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM task_triggers WHERE company_id=? AND task_id=? AND authority=1 AND seq>GREATEST(?::bigint,(SELECT handled_seq FROM tasks WHERE company_id=? AND id=?)))", tx.company, t.ID, briefTo, tx.company, t.ID).Scan(&fresh); err != nil {
		return err
	}
	if fresh {
		if err = resetRetry(ctx, tx, &t); err != nil {
			return err
		}
	}
	var used int
	if err = tx.QueryRowContext(ctx, "SELECT coalesce((SELECT used FROM task_budget WHERE company_id=? AND task_id=? AND day=?),0)", tx.company, t.ID, day).Scan(&used); err != nil {
		return err
	}
	reason, summary := "", ""
	switch {
	case outcome == "setup_failed" && strings.HasPrefix(result, "workspace_diverged"):
		// Retrying cannot help: the worktree changed outside any run (task-worktrees D4).
		reason, summary = "workspace_diverged", excerpt512(result)
	case fresh:
	case outcome == "no_exit" && t.AttemptsNoProgress < RetryAttempts:
		reason, summary = "no_exit", "the agent's run ended without a factory_exit_* call (for review: the agent did not finish): "+excerpt512(result)
	case t.AttemptsNoProgress >= RetryAttempts:
		reason, summary = "retries_exhausted", fmt.Sprintf("%d attempts in a row without progress; last run %s: %s", t.AttemptsNoProgress, outcome, excerpt512(result))
	case used >= DailyBudget:
		reason, summary = "budget_exhausted", fmt.Sprintf("%d runs today (budget %d); last run %s: %s", used, DailyBudget, outcome, excerpt512(result))
	}
	t.Result = result
	if !review && !(reason == "no_exit" && t.Status == "IN_PROGRESS") {
		t.Status, t.BlockReason = "READY", ""
	}
	if reason != "" {
		// The escalation's park holds the task (READY, or IN_REVIEW for a review run) until it is answered.
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return err
		}
		return openEscalation(ctx, tx, reason, escalationAnswers[reason], EscalationWant{TaskID: t.ID, Summary: summary, Target: retryTarget(tx, t.Assignee)})
	}
	// One feed event names the task, the outcome and the retry time (waits-events-cost-cli D2).
	note := fmt.Sprintf("%s: run %s ended %s; ", t.ID, runID, outcome)
	if t.NotBefore > now.Unix() {
		note += fmt.Sprintf("retry in %s", time.Duration(t.NotBefore-now.Unix())*time.Second)
	} else {
		note += "retry now"
	}
	if result != "" {
		note += ": " + excerpt512(result)
	}
	return saveTaskNote(ctx, tx, &t, "task.retry", note)
}

// markRecovered records that the run's end is applied to its task now; false when it already was (an end
// repeated by later events of the run).
func markRecovered(ctx context.Context, tx *Tx, runID string) (bool, error) {
	res, err := tx.ExecContext(ctx, "UPDATE runs SET recovered=1 WHERE id=? AND recovered=0", runID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// countBudget counts a run's end against the task's budget for day.
func countBudget(ctx context.Context, tx *Tx, task, day string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO task_budget(company_id,task_id,day,used) VALUES(?,?,?,1) ON CONFLICT(company_id,task_id,day) DO UPDATE SET used=task_budget.used+1", tx.company, task, day)
	return err
}

// inspectsRun tells whether the run's end parks its task for inspection (D4): the task has side effects,
// or the run's captured agent config has a recovery: inspect rule, and the outcome is unclear (not
// paused_out, stale, unclaimed or a revoke).
func inspectsRun(ctx context.Context, tx *Tx, t tasks.Task, specJSON []byte, outcome string) (bool, error) {
	switch outcome {
	case "revoked", "paused_out", "stale", "unclaimed":
		return false, nil
	}
	if t.SideEffects {
		return true, nil
	}
	var spec protocol.RunSpec
	var captured config.Agent
	if json.Unmarshal(specJSON, &spec) != nil || len(spec.ConfigSnapshot) == 0 || json.Unmarshal(spec.ConfigSnapshot, &captured) != nil {
		return false, nil
	}
	return captured.RecoveryMode() == "inspect", nil
}

func excerpt512(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 512 {
		return s[:512] + "…"
	}
	return s
}

func retryTarget(tx *Tx, assignee string) string {
	if tx.store.EscalationTarget == nil {
		return "owner"
	}
	return tx.store.EscalationTarget(assignee)
}

// resetRetry clears the counters, the backoff and today's budget: authority input or a retry answer.
// Either counts as progress for the no-progress alert.
func resetRetry(ctx context.Context, tx *Tx, t *tasks.Task) error {
	t.AttemptsNoProgress, t.FailStreak, t.NotBefore, t.ProgressAt = 0, 0, 0, tx.now().Unix()
	if err := endEpisode(ctx, tx, t.ID); err != nil {
		return err
	}
	day, err := budgetDay(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM task_budget WHERE company_id=? AND task_id=? AND day=?", tx.company, t.ID, day)
	return err
}

// clearRetryEscalations clears open retry escalations of the task (reassignment, authority ready).
func clearRetryEscalations(ctx context.Context, tx *Tx, t tasks.Task, why string) error {
	open, err := listEscalations(ctx, tx, "WHERE company_id=? AND task_id=? AND status='open' AND reason IN ('retries_exhausted','budget_exhausted')", tx.company, t.ID)
	if err != nil {
		return err
	}
	for _, e := range open {
		if err = closeEscalation(ctx, tx, e, "cleared", "", "system", why); err != nil {
			return err
		}
	}
	return nil
}

// retryAnswer is the retry answer of a retry escalation: counters reset, the task runs again now.
func retryAnswer(ctx context.Context, tx *Tx, t tasks.Task) error {
	if t.Status != "READY" && t.Status != "IN_PROGRESS" && t.Status != "IN_REVIEW" {
		return fmt.Errorf("%w: task is %s", ErrConflict, t.Status)
	}
	if err := resetRetry(ctx, tx, &t); err != nil {
		return err
	}
	t.BlockReason = ""
	if !(t.Status == "IN_REVIEW" && t.Reviewer != "" && t.Assignee == t.Reviewer) {
		t.Status = "READY"
	}
	return saveTask(ctx, tx, &t, "task.retry")
}

// acceptAnswer takes a no_exit run's work as it is: the task's triggers count as handled, so it stays
// idle until new input.
func acceptAnswer(ctx context.Context, tx *Tx, t tasks.Task) error {
	if _, err := tx.ExecContext(ctx, "UPDATE tasks SET handled_seq=wake_seq WHERE company_id=? AND id=?", tx.company, t.ID); err != nil {
		return err
	}
	if t.Status == "IN_PROGRESS" {
		t.Status = "READY"
	}
	return saveTask(ctx, tx, &t, "task.updated")
}

// releaseLost marks a lost run's runtime as gone (it no longer holds its workspace, session or task) and
// lets its task retry.
func releaseLost(ctx context.Context, tx *Tx, id string) error {
	res, err := tx.ExecContext(ctx, "UPDATE runs SET unreleased=0 WHERE id=? AND unreleased=1", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return recoverTask(ctx, tx, id)
}

// releaseNodeRuns releases the unreleased lost runs of node that its new state proves gone: runs of an
// older epoch (the node killed what its previous process left), or runs of this epoch it no longer lists.
func releaseNodeRuns(ctx context.Context, tx *Tx, node string, olderThan int64, listed map[string]bool, epoch int64) error {
	rows, err := tx.QueryContext(ctx, "SELECT id,epoch FROM runs WHERE company_id=? AND node_id=? AND unreleased=1 ORDER BY seq", tx.company, node)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		var e int64
		if err = rows.Scan(&id, &e); err != nil {
			rows.Close()
			return err
		}
		if e < olderThan || listed != nil && e == epoch && !listed[id] {
			ids = append(ids, id)
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, id := range ids {
		if err = releaseLost(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

// inspectAnswer answers the inspect escalation of a side-effect task: the inspected run is reconciled (a
// lost run is released, the inspector having checked its runtime, with the answer in its result) and the
// task runs again; continue tells the next brief that the effect happened.
func inspectAnswer(ctx context.Context, tx *Tx, t tasks.Task, e Escalation, answer, actor string) error {
	note := "inspected: the effect did not happen; retry"
	if answer == "continue" {
		note = "inspected: the effect happened; continue from there"
	}
	if _, err := tx.ExecContext(ctx, "UPDATE runs SET result=result||' ['||?||']',unreleased=0 WHERE id=? AND outcome='lost'", note, e.RefID); err != nil {
		return err
	}
	if err := clearParks(ctx, tx, t, tasks.ParkInspect, e.RefID, "cleared"); err != nil {
		return err
	}
	if err := resetRetry(ctx, tx, &t); err != nil {
		return err
	}
	if answer == "continue" {
		t.ReviewFeedback = "The previous run's external effect happened (confirmed by " + actor + "): do not repeat it; continue from there."
	}
	t.Status, t.BlockReason = "READY", ""
	if err := comment(ctx, tx, t.ID, actor, note); err != nil {
		return err
	}
	return saveTask(ctx, tx, &t, "task.retry")
}

// exitProgress tells whether the run's accepted exit is progress for its task (reliable-recovery D2).
func exitProgress(ctx context.Context, tx *Tx, runID, task string) (bool, error) {
	var kind, sha, args string
	var at int64
	if err := tx.QueryRowContext(ctx, "SELECT kind,exit_sha,arguments,created_at FROM run_exits WHERE run_id=?", runID).Scan(&kind, &sha, &args, &at); err != nil {
		return false, err
	}
	if sha != "" {
		var prev string
		err := tx.QueryRowContext(ctx, "SELECT exit_sha FROM run_exits WHERE company_id=? AND task_id=? AND run_id!=? AND exit_sha!='' AND created_at<=? ORDER BY created_at DESC, seq DESC LIMIT 1", tx.company, task, runID, at).Scan(&prev)
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil // the first commit handed over
		}
		return sha != prev, err
	}
	switch kind {
	case "submit", "done":
		return true, nil
	case "handoff":
		var a struct {
			To      string `json:"to"`
			Verdict string `json:"verdict"`
		}
		_ = json.Unmarshal([]byte(args), &a)
		var agent string
		if err := tx.QueryRowContext(ctx, "SELECT agent FROM runs WHERE id=?", runID).Scan(&agent); err != nil {
			return false, err
		}
		return a.Verdict != "" || a.To != agent, nil
	}
	return false, nil // ask, block, wait
}
