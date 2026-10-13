package storage

import (
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Store) Dispatch(ctx context.Context, id string, version int, spec protocol.RunSpec) (protocol.RunSpec, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return spec, err
	}
	defer tx.Rollback()
	t, err := taskTx(ctx, tx, id)
	if err != nil {
		return spec, err
	}
	if t.WaitMessageID != "" {
		answered, e := messageWaitState(ctx, tx, t.WaitMessageID)
		if e != nil {
			return spec, e
		}
		if !answered {
			return spec, ErrConflict
		}
	}
	if t.Version != version || t.Status != "READY" || t.Assignee == "" || spec.Agent != t.Assignee || spec.Project != t.ProjectID || spec.ID == "" {
		return spec, ErrConflict
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
	spec.ApprovedArtifacts = inputs
	if len(inputs) > 0 {
		b, _ := json.Marshal(inputs)
		spec.Prompt += "\nOwner-approved artifact inputs (use these exact versions and URIs): " + string(b) + "\n"
	}
	var waiting int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM task_dependencies d JOIN tasks t ON t.id=d.depends_on WHERE d.task_id=? AND t.status!='DONE'", id).Scan(&waiting); err != nil {
		return spec, err
	}
	if waiting > 0 {
		return spec, ErrConflict
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE agent=? AND status IN ('QUEUED','RUNNING','CANCELLING')", t.Assignee).Scan(&waiting); err != nil {
		return spec, err
	}
	if waiting > 0 {
		return spec, ErrConflict
	}
	p, err := getDoc[tasks.Project](ctx, tx, "SELECT body FROM projects WHERE id=?", t.ProjectID)
	if err != nil {
		return spec, err
	}
	spec.Workspace = p.Workspace
	if t.CurrentRunID != "" {
		var status, session string
		if err = tx.QueryRowContext(ctx, "SELECT status,session_id FROM runs WHERE id=?", t.CurrentRunID).Scan(&status, &session); err != nil {
			return spec, err
		}
		if status == "UNKNOWN" || status == "RUNNING" || status == "QUEUED" || status == "CANCELLING" {
			return spec, ErrConflict
		}
		spec.SessionID = session
	}
	spec.TaskID = t.ID
	spec.Attempt = t.Attempt + 1
	if err = insertRun(ctx, tx, &spec); err != nil {
		return spec, constraint(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO task_attempts VALUES(?,?,?)", spec.ID, t.ID, spec.Attempt); err != nil {
		return spec, err
	}
	t.WaitMessageID = ""
	t.Status = "IN_PROGRESS"
	t.Attempt = spec.Attempt
	t.CurrentRunID = spec.ID
	t.Result = ""
	t.Submission = nil
	t.BlockReason = ""
	t.CancelRequested = false
	t.RetryEligible = false
	if err = saveTask(ctx, tx, &t, "task.started"); err != nil {
		return spec, err
	}
	return spec, tx.Commit()
}
func syncTaskRun(ctx context.Context, tx *sql.Tx, runID string, knownFailure bool) error {
	if err := autoRuntimeReport(ctx, tx, runID); err != nil {
		return err
	}
	if err := syncMessageRun(ctx, tx, runID); err != nil {
		return err
	}
	var taskID sql.NullString
	var status, result string
	if err := tx.QueryRowContext(ctx, "SELECT task_id,status,result FROM runs WHERE id=?", runID).Scan(&taskID, &status, &result); err != nil {
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
	if t.Status != "IN_PROGRESS" && !(t.Status == "BLOCKED" && strings.HasPrefix(t.BlockReason, "run:")) {
		return nil
	}
	next := ""
	switch status {
	case "COMPLETED":
		next = "IN_REVIEW"
		if t.CancelRequested {
			next = "CANCELLED"
		}
	case "FAILED":
		next = "FAILED"
	case "CANCELLED":
		next = "CANCELLED"
	case "UNKNOWN":
		next = "BLOCKED"
	}
	if status == "COMPLETED" && t.WaitMessageID != "" && !t.CancelRequested {
		answered, err := messageWaitState(ctx, tx, t.WaitMessageID)
		if err != nil {
			return err
		}
		t.Result = result
		t.Submission = nil
		t.RetryEligible = false
		t.Status = "BLOCKED"
		t.BlockReason = "message: waiting for answer"
		if answered {
			t.Status = "READY"
			t.BlockReason = ""
		}
		return saveTask(ctx, tx, &t, "task.waiting")
	}
	if next == "" || next == t.Status {
		return nil
	}
	t.Status = next
	t.RetryEligible = next == "FAILED" && knownFailure && !t.CancelRequested && t.WaitMessageID == ""
	t.Result = result
	t.BlockReason = ""
	kind := "task.updated"
	if next == "BLOCKED" {
		t.BlockReason = "run: outcome unknown; inspect workspace and reconcile run"
	}
	if next == "IN_REVIEW" {
		if t.Submission == nil {
			t.Submission = &tasks.Submission{Summary: result}
		}
		kind = "task.submitted"
	}
	return saveTask(ctx, tx, &t, kind)
}
func (s *Store) Attempts(ctx context.Context, id string) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT r.spec,r.status,r.session_id,r.result,r.started_at,r.finished_at FROM task_attempts a JOIN runs r ON r.id=a.run_id WHERE a.task_id=? ORDER BY a.attempt", id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var b []byte
		if err = rows.Scan(&b, &r.Status, &r.SessionID, &r.Result, &r.StartedAt, &r.FinishedAt); err != nil {
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

func (s *Store) Comments(ctx context.Context, id string) ([]Comment, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id,author,text,created_at FROM task_comments WHERE task_id=? ORDER BY created_at,rowid", id)
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
func (s *Store) Cancel(ctx context.Context, id string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var status string
	var taskID sql.NullString
	if err = tx.QueryRowContext(ctx, "SELECT status,task_id FROM runs WHERE id=?", id).Scan(&status, &taskID); err != nil {
		return err
	}
	if status != "QUEUED" && status != "RUNNING" {
		return ErrConflict
	}
	next := "CANCELLING"
	if status == "QUEUED" {
		next = "CANCELLED"
	}
	if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,finished_at=CASE WHEN ?='CANCELLED' THEN ? ELSE finished_at END WHERE id=?", next, next, time.Now().Unix(), id); err != nil {
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
		if next == "CANCELLED" {
			t.Status = "CANCELLED"
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return err
		}
	}
	if next == "CANCELLED" {
		if err = autoRuntimeReport(ctx, tx, id); err != nil {
			return err
		}
		if err = syncMessageRun(ctx, tx, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Expire(ctx context.Context) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id FROM runs WHERE status IN ('RUNNING','CANCELLING') AND lease_until<?", time.Now().Unix())
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='UNKNOWN',result='Worker lease expired; inspect workspace before reconciliation' WHERE id=?", id); err != nil {
			return err
		}
		if err = syncTaskRun(ctx, tx, id, false); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) Resolve(ctx context.Context, id, status, result string) error {
	if status != "COMPLETED" && status != "FAILED" && status != "CANCELLED" {
		return ErrConflict
	}
	if strings.TrimSpace(result) == "" {
		return inputError("reconciliation evidence required")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE runs SET status=?,result=?,finished_at=? WHERE id=? AND status='UNKNOWN'", status, result, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	if err = syncTaskRun(ctx, tx, id, false); err != nil {
		return err
	}
	var project string
	if err = tx.QueryRowContext(ctx, "SELECT project FROM runs WHERE id=?", id).Scan(&project); err != nil {
		return err
	}
	if err = entityEvent(ctx, tx, "run.reconciled", project, id, map[string]string{"id": id, "status": status, "evidence": result}); err != nil {
		return fmt.Errorf("reconciliation audit: %w", err)
	}
	return tx.Commit()
}
