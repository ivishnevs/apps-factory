package storage

import (
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func (s *Store) Dispatch(ctx context.Context, company, id string, version int, spec protocol.RunSpec) (protocol.RunSpec, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return spec, err
	}
	defer tx.Rollback()
	t, err := taskTx(ctx, tx, id)
	if err != nil {
		return spec, err
	}
	if waiting, err := askPark(ctx, tx, company, t.ID); err != nil || waiting != "" {
		if err == nil {
			err = ErrConflict
		}
		return spec, err
	}
	// A review run: the reviewer assigned by factory_exit_submit reviews the task while it stays IN_REVIEW.
	review := t.Status == "IN_REVIEW" && t.Reviewer != "" && t.Assignee == t.Reviewer
	if t.Version != version || t.Status != "READY" && !review || t.Assignee == "" || spec.Agent != t.Assignee || spec.Project != t.ProjectID || spec.ID == "" {
		return spec, ErrConflict
	}
	if paused, err := pausedFor(ctx, tx, t.ProjectID, t.Assignee, QuotaKey(spec.ConfigSnapshot, t.Assignee)); err != nil || len(paused) > 0 {
		if err == nil {
			err = ErrConflict
		}
		return spec, err
	}
	if err = s.checkTaskChange(ctx, tx, t); err != nil {
		return spec, err
	}
	spec.OpenSpecChange = t.OpenSpecChange
	inputs, reason, err := approvedArtifactInputs(ctx, tx, t)
	if err != nil {
		return spec, err
	}
	if reason != "" {
		return spec, ErrConflict
	}
	spec.ApprovedArtifacts = inputs // the brief lists them at claim
	var waiting int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM task_dependencies d JOIN tasks t ON t.id=d.depends_on WHERE d.task_id=? AND t.status!='DONE'", id).Scan(&waiting); err != nil {
		return spec, err
	}
	if waiting > 0 {
		return spec, ErrConflict
	}
	// Concurrency caps (trigger-log-scheduling 2.5): the agent's live runs, in all projects and in this one.
	all, inProject, err := agentLive(ctx, tx, t.Assignee, t.ProjectID, t.ID)
	if err != nil {
		return spec, err
	}
	if perAgent, perProject := AgentCaps(spec.ConfigSnapshot); all >= perAgent || inProject >= perProject {
		return spec, ErrConflict
	}
	p, err := projectTx(ctx, tx, t.ProjectID)
	if err != nil {
		return spec, err
	}
	spec.Workspace = p.Workspace
	// Worktrees need a repository: without repo_url the task keeps the shared project workspace.
	spec.RepoURL, spec.DefaultBranch, spec.WorkspaceMode = p.RepoURL, p.DefaultBranch, ""
	if p.RepoURL != "" {
		spec.WorkspaceMode = WorkspaceMode(spec.ConfigSnapshot)
	}
	if t.CurrentRunID != "" {
		var status, session string
		var unreleased bool
		if err = tx.QueryRowContext(ctx, "SELECT status,session_id,unreleased FROM runs WHERE id=?", t.CurrentRunID).Scan(&status, &session, &unreleased); err != nil {
			return spec, err
		}
		if unreleased || status != "ENDED" {
			return spec, ErrConflict
		}
		spec.SessionID = session
	}
	if t.FreshSession { // reset_workspace: the old session stays with the old node and worktree
		spec.SessionID, t.FreshSession = "", false
	}
	spec.TaskID = t.ID
	spec.Attempt = t.Attempt + 1
	if err = insertRun(ctx, tx, &spec); err != nil {
		return spec, constraint(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO task_attempts VALUES(?,?,?,?)", spec.ID, t.ID, spec.Attempt, company); err != nil {
		return spec, err
	}
	if !review {
		t.Status = "IN_PROGRESS"
	}
	t.Attempt = spec.Attempt
	t.CurrentRunID = spec.ID
	if !review { // the reviewer needs the submission
		t.Result = ""
		t.Submission = nil
	}
	t.BlockReason = ""
	t.CancelRequested = false
	if err = saveTask(ctx, tx, &t, "task.started"); err != nil {
		return spec, err
	}
	return spec, tx.Commit()
}

// syncTaskRun moves the task of an ended run, then applies the outcome table (automatic retry).
func syncTaskRun(ctx context.Context, tx *Tx, runID string) error {
	if err := syncTaskStatus(ctx, tx, runID); err != nil {
		return err
	}
	return recoverTask(ctx, tx, runID)
}

func syncTaskStatus(ctx context.Context, tx *Tx, runID string) error {
	if err := autoRuntimeReport(ctx, tx, runID); err != nil {
		return err
	}
	if err := syncMessageRun(ctx, tx, runID); err != nil {
		return err
	}
	var taskID sql.NullString
	var outcome, result string
	if err := tx.QueryRowContext(ctx, "SELECT task_id,outcome,result FROM runs WHERE id=?", runID).Scan(&taskID, &outcome, &result); err != nil {
		return err
	}
	if !taskID.Valid {
		return nil
	}
	t, err := taskTx(ctx, tx, taskID.String)
	if err != nil {
		return err
	}
	if t.CurrentRunID != runID {
		return nil
	}
	hasExit, err := runHasExit(ctx, tx, runID)
	if err != nil {
		return err
	}
	// A run that asked a blocking question ends waiting for the answer (its ask park), not in review.
	asked, err := runAsked(ctx, tx, runID)
	if err != nil {
		return err
	}
	if err = clearParks(ctx, tx, t, tasks.ParkInspect, runID, "cleared"); err != nil {
		return err
	}
	// A completed turn: succeeded, or no_exit (a turn ended without an exit under explicit exits).
	completed := outcome == "succeeded" || outcome == "no_exit"
	if hasExit && !(completed && asked) {
		return nil // the accepted exit already moved the task (an ask still waits for its answer below)
	}
	if !hasExit && tx.store.ExplicitExits && completed {
		return nil // no_exit: recoverTask escalates
	}
	if completed && !hasExit {
		// Without explicit exits, a completed turn handles the triggers it was briefed on.
		if _, err = tx.ExecContext(ctx, "UPDATE tasks SET handled_seq=max(handled_seq,(SELECT brief_to FROM runs WHERE id=?)) WHERE company_id=? AND id=?", runID, tx.company, t.ID); err != nil {
			return err
		}
	}
	if t.Status != "IN_PROGRESS" && !(t.Status == "BLOCKED" && strings.HasPrefix(t.BlockReason, "run:")) {
		return nil
	}
	next := ""
	switch {
	case completed:
		next = "IN_REVIEW"
		if t.CancelRequested {
			next = "CANCELLED"
		}
	case outcome == "revoked":
		next = "CANCELLED"
		// A run revoked by the kill switch stops the run, not the task.
		if revoked, err := runRevoked(ctx, tx, runID); err != nil {
			return err
		} else if revoked && !t.CancelRequested {
			next = "READY"
		}
	case t.CancelRequested:
		// Any other end leaves the task in progress and recoverTask applies the outcome (retry, escalation
		// or inspection); a requested cancel is honoured.
		next = "CANCELLED"
	}
	if completed && asked && !t.CancelRequested {
		// READY: an open question's ask park holds the task (reads show BLOCKED); an answered one runs it.
		t.Result = result
		t.Submission = nil
		t.Status = "READY"
		t.BlockReason = ""
		return saveTask(ctx, tx, &t, "task.waiting")
	}
	if next == "" || next == t.Status {
		return nil
	}
	t.Status = next
	t.Result = result
	t.BlockReason = ""
	kind := "task.updated"
	if next == "IN_REVIEW" {
		if t.Submission == nil {
			t.Submission = &tasks.Submission{Summary: result}
		}
		kind = "task.submitted"
	}
	if err = saveTask(ctx, tx, &t, kind); err != nil {
		return err
	}
	return closedTask(ctx, tx, t)
}
func (s *Store) Attempts(ctx context.Context, company, id string) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT r.spec,r.status,r.session_id,r.result,r.started_at,r.finished_at,r.usage,r.outcome FROM task_attempts a JOIN runs r ON r.id=a.run_id WHERE a.company_id=? AND a.task_id=? ORDER BY a.attempt", company, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var b []byte
		if err = rows.Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt, &r.Usage, &r.Outcome); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &r.Spec); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

type Comment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Text      string `json:"text"`
	CreatedAt int64  `json:"created_at"`
}

func (s *Store) Comments(ctx context.Context, company, id string) ([]Comment, error) {
	return comments(ctx, s.DB, company, id)
}

func listComments(ctx context.Context, tx *Tx, id string) ([]Comment, error) {
	return comments(ctx, tx, tx.company, id)
}

func comments(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, company, id string) ([]Comment, error) {
	rows, err := q.QueryContext(ctx, "SELECT id,author,text,created_at FROM task_comments WHERE company_id=? AND task_id=? ORDER BY created_at,rowid", company, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Comment{}
	for rows.Next() {
		var c Comment
		if err = rows.Scan(&c.ID, &c.Author, &c.Text, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (s *Store) Cancel(ctx context.Context, company, id string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	var taskID sql.NullString
	if err = tx.QueryRowContext(ctx, "SELECT status,task_id FROM runs WHERE company_id=? AND id=?", company, id).Scan(&status, &taskID); err != nil {
		return err
	}
	if status != "QUEUED" && status != "CLAIMED" && status != "RUNNING" {
		return ErrConflict
	}
	// A queued run ends revoked at once; a claimed or running one is CANCELLING until its worker stops it.
	next, outcome := "CANCELLING", ""
	if status == "QUEUED" {
		next, outcome = "ENDED", "revoked"
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,outcome=?,finished_at=CASE WHEN ?='ENDED' THEN ? ELSE finished_at END WHERE id=?", next, outcome, next, tx.now().Unix(), id); err != nil {
		return err
	}
	if taskID.Valid {
		t, err := taskTx(ctx, tx, taskID.String)
		if err != nil {
			return err
		}
		if t.CurrentRunID != id {
			return ErrConflict
		}
		t.CancelRequested = true
		if next == "ENDED" {
			t.Status = "CANCELLED"
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return err
		}
		if err = closedTask(ctx, tx, t); err != nil {
			return err
		}
	}
	if next == "ENDED" {
		if err = autoRuntimeReport(ctx, tx, id); err != nil {
			return err
		}
		if err = syncMessageRun(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Expire(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// v2 leases last NodeOffline and are judged over as much continuous ticking, like the node's silence.
	rows, err := tx.QueryContext(ctx, "SELECT id,protocol=2 AND run_started=0 FROM runs WHERE company_id=? AND status IN ('CLAIMED','RUNNING','CANCELLING') AND (protocol=2 AND lease_until<? OR protocol!=2 AND lease_until<?)", company, s.windowCutoff(NodeOffline), s.leaseCutoff())
	if err != nil {
		return err
	}
	ids := []string{}
	unstarted := map[string]bool{}
	for rows.Next() {
		var id string
		var claimed bool
		if err = rows.Scan(&id, &claimed); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
		unstarted[id] = claimed
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if unstarted[id] {
			// A v2 run that never started: the runtime never ran, so it fails retryably (protocol v2 §10).
			if err = endLost(ctx, tx, id, "lost: not started (node lease expired)"); err != nil {
				return err
			}
			continue
		}
		// A v1 worker stops a run whose lease it lost, so the run is released at once; a v2 run waits for
		// its node to confirm.
		var v1 bool
		if err = tx.QueryRowContext(ctx, "SELECT protocol=1 FROM runs WHERE id=?", id).Scan(&v1); err != nil {
			return err
		}
		if err = endLostRun(ctx, tx, id, "lost: worker lease expired", !v1); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResolveAnswers are the answers of factory runs resolve: those of an inspect escalation (D4).
var ResolveAnswers = []string{"continue", "retry", "cancel"}

// Resolve is the owner's reconciliation of a run (factory runs resolve ID ANSWER EVIDENCE). A run with an
// open inspect escalation (reliable-recovery D4) gets the answer: continue (the effect happened), retry or
// cancel. A lost run whose runtime may still run is released (the owner checked that the runtime is gone):
// retry keeps it lost and its task retries after the backoff, continue also tells the next brief that the
// run's work happened (counters reset), cancel ends it revoked and cancels its task.
func (s *Store) Resolve(ctx context.Context, company, id, answer, result string) error {
	if !slices.Contains(ResolveAnswers, answer) {
		return inputError("answer must be continue, retry or cancel")
	}
	if strings.TrimSpace(result) == "" {
		return inputError("reconciliation evidence required")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	open, err := listEscalations(ctx, tx, "WHERE company_id=? AND reason='inspect' AND ref_id=? AND status='open'", company, id)
	if err != nil {
		return err
	}
	if len(open) > 0 {
		e := open[0]
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET unreleased=0 WHERE id=?", id); err != nil {
			return err
		}
		if err = closeEscalation(ctx, tx, e, "answered", answer, "owner", ""); err != nil {
			return err
		}
		if err = comment(ctx, tx, e.TaskID, "owner", "runs resolve "+id+" "+answer+": "+result); err != nil {
			return err
		}
		if err = applyEscalationAnswer(ctx, tx, e, answer, "owner"); err != nil {
			return err
		}
		if t, err := taskTx(ctx, tx, e.TaskID); err != nil {
			return err
		} else if t.Status != "DONE" && t.Status != "CANCELLED" {
			if err = addTrigger(ctx, tx, t, "answer", true, "owner", e.MessageID, "escalation inspect: "+answer); err != nil {
				return err
			}
		}
	} else {
		outcome := "lost"
		if answer == "cancel" {
			outcome = "revoked"
		}
		res, err := tx.ExecContext(ctx, "UPDATE runs SET outcome=?,result=result||' [resolved '||?||': '||?||']',unreleased=0 WHERE company_id=? AND id=? AND unreleased=1", outcome, answer, result, company, id)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return ErrConflict
		}
		if err = syncTaskRun(ctx, tx, id); err != nil {
			return err
		}
		if answer == "continue" {
			if err = resolvedContinue(ctx, tx, id); err != nil {
				return err
			}
		}
	}
	var project string
	if err = tx.QueryRowContext(ctx, "SELECT project FROM runs WHERE id=?", id).Scan(&project); err != nil {
		return err
	}
	if err = entityEvent(ctx, tx, "run.reconciled", project, id, map[string]string{"id": id, "answer": answer, "evidence": result}); err != nil {
		return fmt.Errorf("reconciliation audit: %w", err)
	}
	return tx.Commit()
}

// resolvedContinue tells the next run of a released lost run's task that the run's work happened: its
// counters and backoff reset and the brief says to continue from there.
func resolvedContinue(ctx context.Context, tx *Tx, runID string) error {
	var taskID sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT task_id FROM runs WHERE id=?", runID).Scan(&taskID); err != nil || !taskID.Valid {
		return err
	}
	t, err := taskTx(ctx, tx, taskID.String)
	if err != nil || t.CurrentRunID != runID || t.Status == "DONE" || t.Status == "CANCELLED" {
		return err
	}
	if err = resetRetry(ctx, tx, &t); err != nil {
		return err
	}
	t.ReviewFeedback = "The previous run's work happened (confirmed by the owner): do not repeat it; continue from there."
	return saveTask(ctx, tx, &t, "task.retry")
}
