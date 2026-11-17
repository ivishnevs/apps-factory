package storage

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

// Failure routing (contracts-at-the-boundary §2). A run that failed before its agent started names who can fix the
// failure (runs.failure_fixer); Core routes the task to them instead of retrying the same agent:
//   - implementer: the handed-over work is missing or unusable; the task goes back to its implementer as rework with
//     the cause, and the failed run's attempt is not counted;
//   - owner: one owner item naming what to provide, without retries first;
//   - node and none: node readiness and the bounded automatic retry, as before.
//
// The same failure (code, or the text of an unclassified one) on the same handed-over head twice in a row is
// deterministic: it routes to the implementer when the task has handed-over work, else to an escalation.

// SetupBlockedReason escalates a task whose run cannot start until someone provides or fixes something.
const SetupBlockedReason = "setup_blocked"

// ReturnAnswer sends a task in review back to its implementer with the escalation's cause.
const ReturnAnswer = "return"

// answersFor are the escalation's answers for the task: retries_exhausted on a task in review also offers return.
func answersFor(reason string, t tasks.Task) []string {
	answers := escalationAnswers[reason]
	if reason == "retries_exhausted" && t.Status == "IN_REVIEW" && t.Implementer != "" && t.Assignee != t.Implementer {
		answers = append(slices.Clone(answers[:len(answers)-1]), ReturnAnswer, answers[len(answers)-1]) // before cancel
	}
	return answers
}

// runFailure is a run's recorded setup failure.
type runFailure struct {
	agent, outcome, result, code, fixer, head string
}

func failureOf(ctx context.Context, tx *Tx, runID string) (runFailure, error) {
	var f runFailure
	err := tx.QueryRowContext(ctx, "SELECT agent,outcome,result,failure_code,failure_fixer,failure_head FROM runs WHERE id=?", runID).Scan(&f.agent, &f.outcome, &f.result, &f.code, &f.fixer, &f.head)
	return f, err
}

// same: two failures are the same when the same agent's runs failed with the same code (or, unclassified, their text) and handed-over head match.
func (f runFailure) same(o runFailure) bool {
	if f.agent != o.agent || f.outcome != o.outcome || f.code != o.code || f.head != o.head {
		return false
	}
	return f.code != "" || f.result == o.result
}

// repeated tells whether the same agent's previous ended run of the task failed the same way as this one.
func repeated(ctx context.Context, tx *Tx, task, runID string, f runFailure) (bool, error) {
	var prev string
	// The same agent's previous ended run of the task (runs of other agents in between, such as the implementer's own,
	// do not break "in a row" for a reviewer: its next run is the one that would repeat the failure).
	err := tx.QueryRowContext(ctx, "SELECT id FROM runs WHERE company_id=? AND task_id=? AND agent=? AND id<>? AND status='ENDED' AND created_at<=(SELECT created_at FROM runs WHERE id=?) ORDER BY created_at DESC, finished_at DESC, id DESC LIMIT 1", tx.company, task, f.agent, runID, runID).Scan(&prev)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	p, err := failureOf(ctx, tx, prev)
	if err != nil {
		return false, err
	}
	return f.same(p), nil
}

// routeFailure routes the task of a run that failed at setup by its fixer; handled reports that it did (the caller
// applies the bounded retry otherwise).
func routeFailure(ctx context.Context, tx *Tx, runID string, t *tasks.Task, review bool, outcome, result, day string) (bool, error) {
	f, err := failureOf(ctx, tx, runID)
	if err != nil || f.fixer == protocol.FixerNode || f.fixer == protocol.FixerNone {
		return false, err
	}
	if outcome != "setup_failed" || f.code == protocol.FailureWorkspaceDiverged || strings.HasPrefix(result, "workspace_diverged") {
		return false, nil // workspace_diverged keeps its own escalation (task-worktrees D4)
	}
	fixer := f.fixer
	if fixer == "" {
		again, err := repeated(ctx, tx, t.ID, runID, f)
		if err != nil || !again {
			return false, err
		}
		// Deterministic and unclassified: the implementer when there is handed-over work, else an escalation.
		fixer = "escalate"
		if f.head != "" || t.Submission != nil {
			fixer = protocol.FixerImplementer
		}
	}
	if err = countBudget(ctx, tx, t.ID, day); err != nil {
		return true, err
	}
	t.Result = result
	cause := excerpt512(result)
	switch fixer {
	case protocol.FixerImplementer:
		known := t.Implementer != "" && (tasks.IsPerson(t.Implementer) || tx.store.AgentKnown == nil || tx.store.AgentKnown(tx.company, t.Implementer))
		if known && t.Implementer != f.agent {
			return true, returnToImplementer(ctx, tx, *t, "factory", runID, cause)
		}
		// The implementer cannot be reached (removed from the team) or is the one whose run failed: its manager decides.
		target := "owner"
		if t.Implementer != "" && t.Implementer != f.agent {
			target = retryTarget(tx, t.Implementer)
		} else {
			target = retryTarget(tx, t.Assignee)
		}
		return true, blockSetup(ctx, tx, t, review, target, fmt.Sprintf("run %s could not start because the handed-over work is missing or unusable, and its implementer %s cannot fix it: %s — reassign the task to someone who can redo the work, retry once it is back, or cancel", runID, cmp.Or(t.Implementer, "(none)"), cause))
	case protocol.FixerOwner:
		return true, blockSetup(ctx, tx, t, review, "owner", fmt.Sprintf("run %s could not start: %s — %s, then retry", runID, cause, provide(f.code)))
	default:
		return true, blockSetup(ctx, tx, t, review, retryTarget(tx, t.Assignee), fmt.Sprintf("run %s failed the same way twice before its agent started: %s — fix the cause, then retry, reassign or cancel", runID, cause))
	}
}

// provide names what the owner provides for a setup failure code.
func provide(code string) string {
	switch code {
	case protocol.FailureLocalPolicy:
		return "allow the run in that node's node.yaml or run the project on another node"
	case protocol.FailureSecret:
		return "set the missing secret on the Secrets page"
	case protocol.FailureSkill:
		return "fix the skill in the company's catalog"
	}
	return "provide what the run needs"
}

// blockSetup holds the task behind a setup_blocked escalation (no retries first).
func blockSetup(ctx context.Context, tx *Tx, t *tasks.Task, review bool, target, summary string) error {
	if !review {
		t.Status, t.BlockReason = "READY", ""
	}
	t.NotBefore = 0
	if err := saveTask(ctx, tx, t, "task.updated"); err != nil {
		return err
	}
	return openEscalation(ctx, tx, SetupBlockedReason, escalationAnswers[SetupBlockedReason], EscalationWant{TaskID: t.ID, Summary: summary, Target: target})
}

// returnToImplementer gives a task back to its implementer as rework with the cause in the brief (the review
// feedback), without counting an attempt: a failure route or the return answer of an escalation.
func returnToImplementer(ctx context.Context, tx *Tx, t tasks.Task, actor, ref, cause string) error {
	if t.Implementer == "" {
		return inputError("the task has no implementer to return it to")
	}
	brief := "your handed-over work could not be used for review: " + cause + ". Push your work to the task branch and submit again."
	t.Assignee, t.Status, t.ReviewFeedback, t.Reviewer, t.BlockReason, t.NotBefore = t.Implementer, "READY", brief, "", "", 0
	t.AttemptsNoProgress, t.FailStreak = 0, 0
	if err := comment(ctx, tx, t.ID, actor, "returned to "+t.Implementer+": "+brief); err != nil {
		return err
	}
	if err := saveTask(ctx, tx, &t, "task.updated"); err != nil {
		return err
	}
	return addTrigger(ctx, tx, &t, "assignment", false, actor, ref, "returned to you: "+brief)
}
