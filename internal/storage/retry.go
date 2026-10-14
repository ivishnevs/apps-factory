package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/tasks"
)

// Automatic retry (reliable-recovery D2–D3; owner decision 2026-10-14: autonomy first). A task whose run
// ended with an attempt or lost outcome goes back to READY after a backoff and runs again in the same
// workspace, resuming the session where the node allows it. Three attempts without progress or the daily
// budget escalate instead. Tasks marked side_effects keep the conservative path (FAILED, or inspect).

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
	escalationAnswers["no_exit"] = []string{"retry", "accept", "cancel"}
}

func budgetDay(t time.Time) string { return t.UTC().Format("2006-01-02") }

// recoverTask applies the outcome table to the task after its current run ended (called from
// syncTaskRun once the task has its status). It counts the attempt and the budget, then either schedules
// the retry (READY with not_before), escalates, or leaves the task as it is.
func recoverTask(ctx context.Context, tx *Tx, runID string) error {
	var taskID sql.NullString
	var status, outcome, result string
	var released bool
	if err := tx.QueryRowContext(ctx, "SELECT task_id,status,outcome,result,released FROM runs WHERE id=?", runID).Scan(&taskID, &status, &outcome, &result, &released); err != nil {
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
	if outcome == "succeeded" {
		if t.AttemptsNoProgress == 0 && t.FailStreak == 0 && (hasExit || t.NotBefore == 0) {
			return nil
		}
		t.AttemptsNoProgress, t.FailStreak = 0, 0
		if !hasExit { // an exit sets not_before itself (wait)
			t.NotBefore = 0
		}
		return saveTask(ctx, tx, &t, "task.updated")
	}
	rule, ok := Outcomes[outcome]
	if !ok || t.SideEffects || t.CancelRequested || t.WaitMessageID != "" {
		return nil
	}
	// A review run (the reviewer is the assignee of the task in review) keeps the task IN_REVIEW.
	review := t.Status == "IN_REVIEW" && t.Reviewer != "" && t.Assignee == t.Reviewer
	switch {
	case outcome == "no_exit":
	case t.Status == "FAILED", review:
	case t.Status == "BLOCKED" && status == "UNKNOWN" && released:
	default:
		return nil // CANCELLED, READY (kill switch), or a lost run that may still run
	}
	if rule.Budget && !strings.Contains(result, "restarted (epoch") {
		if _, err = tx.ExecContext(ctx, "INSERT INTO task_budget(company_id,task_id,day,used) VALUES(?,?,?,1) ON CONFLICT(company_id,task_id,day) DO UPDATE SET used=used+1", tx.company, t.ID, budgetDay(now)); err != nil {
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
	if status == "UNKNOWN" {
		if err = clearParks(ctx, tx, t, tasks.ParkInspect, runID, "cleared"); err != nil {
			return err
		}
	}
	var used int
	if err = tx.QueryRowContext(ctx, "SELECT coalesce((SELECT used FROM task_budget WHERE company_id=? AND task_id=? AND day=?),0)", tx.company, t.ID, budgetDay(now)).Scan(&used); err != nil {
		return err
	}
	reason, summary := "", ""
	switch {
	case outcome == "no_exit" && t.AttemptsNoProgress < RetryAttempts:
		reason, summary = "no_exit", "the agent's run ended without a factory_exit_* call (for review: the agent did not finish): "+excerpt512(result)
	case t.AttemptsNoProgress >= RetryAttempts:
		reason, summary = "retries_exhausted", fmt.Sprintf("%d attempts in a row without progress; last run %s: %s", t.AttemptsNoProgress, outcome, excerpt512(result))
	case used >= DailyBudget:
		reason, summary = "budget_exhausted", fmt.Sprintf("%d runs today (budget %d); last run %s: %s", used, DailyBudget, outcome, excerpt512(result))
	}
	t.RetryEligible = false
	t.Result = result
	if reason != "" {
		if !review && outcome != "no_exit" {
			t.Status, t.BlockReason = "FAILED", ""
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return err
		}
		return openEscalation(ctx, tx, reason, escalationAnswers[reason], EscalationWant{TaskID: t.ID, Summary: summary, Target: retryTarget(ctx, tx)})
	}
	if !review {
		t.Status, t.BlockReason = "READY", ""
	}
	return saveTask(ctx, tx, &t, "task.retry")
}

func excerpt512(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 512 {
		return s[:512] + "…"
	}
	return s
}

func retryTarget(ctx context.Context, tx *Tx) string {
	if tx.store.EscalationTarget == nil {
		return "owner"
	}
	load := map[string]int{}
	rows, err := tx.QueryContext(ctx, "SELECT target,count(*) FROM escalations WHERE company_id=? AND status='open' GROUP BY target", tx.company)
	if err == nil {
		for rows.Next() {
			var target string
			var n int
			if rows.Scan(&target, &n) == nil {
				load[target] = n
			}
		}
		rows.Close()
	}
	return tx.store.EscalationTarget(load)
}

// resetRetry clears the counters, the backoff and today's budget: authority input or a retry answer.
func resetRetry(ctx context.Context, tx *Tx, t *tasks.Task) error {
	t.AttemptsNoProgress, t.FailStreak, t.NotBefore = 0, 0, 0
	_, err := tx.ExecContext(ctx, "DELETE FROM task_budget WHERE company_id=? AND task_id=? AND day=?", tx.company, t.ID, budgetDay(tx.now()))
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
	if t.Status != "FAILED" && t.Status != "IN_PROGRESS" && t.Status != "IN_REVIEW" {
		return fmt.Errorf("%w: task is %s", ErrConflict, t.Status)
	}
	if err := resetRetry(ctx, tx, &t); err != nil {
		return err
	}
	t.RetryEligible, t.BlockReason = false, ""
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

// releaseLost marks a lost run's runtime as gone (it no longer holds its project, workspace or session)
// and lets its task retry.
func releaseLost(ctx context.Context, tx *Tx, id string) error {
	res, err := tx.ExecContext(ctx, "UPDATE runs SET released=1 WHERE id=? AND status='UNKNOWN' AND released=0", id)
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
	rows, err := tx.QueryContext(ctx, "SELECT id,epoch FROM runs WHERE company_id=? AND node_id=? AND status='UNKNOWN' AND released=0", tx.company, node)
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
