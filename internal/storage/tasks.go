package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"appfactory/internal/artifacts"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"

	"modernc.org/sqlite"
)

var entityName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,95}$`)

func inputError(message string) error { return fmt.Errorf("%w: %s", ErrInput, message) }
func constraint(err error) error {
	var e *sqlite.Error
	if errors.As(err, &e) && e.Code()&255 == 19 {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}
func getDoc[T any](ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, query string, args ...any) (T, error) {
	var v T
	var b []byte
	err := q.QueryRowContext(ctx, query, args...).Scan(&b)
	if errors.Is(err, sql.ErrNoRows) {
		return v, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(b, &v)
	return v, err
}
func mutate[T any](ctx context.Context, s *Store, company, scope, key string, input any, fn func(*Tx) (T, error)) (T, error) {
	var zero T
	if len(key) > 128 {
		return zero, inputError("request_id exceeds 128 characters")
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return zero, err
	}
	defer tx.Rollback()
	b, err := json.Marshal(input)
	if err != nil {
		return zero, err
	}
	hash := sha256.Sum256(b)
	fingerprint := hex.EncodeToString(hash[:])
	if key != "" {
		var old string
		var response []byte
		err = tx.QueryRowContext(ctx, "SELECT fingerprint,response FROM mutation_results WHERE company_id=? AND scope=? AND request_id=?", company, scope, key).Scan(&old, &response)
		if err == nil {
			if old != fingerprint {
				return zero, ErrConflict
			}
			err = json.Unmarshal(response, &zero)
			return zero, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return zero, err
		}
	}
	v, err := fn(tx)
	if err != nil {
		return zero, constraint(err)
	}
	if key != "" {
		response, err := json.Marshal(v)
		if err != nil {
			return zero, err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO mutation_results VALUES(?,?,?,?,?)", company, scope, key, fingerprint, response); err != nil {
			return zero, err
		}
	}
	if err = tx.Commit(); err != nil {
		return zero, err
	}
	return v, nil
}
func entityEvent(ctx context.Context, tx *Tx, kind, project, id string, v any) error {
	e := protocol.Wrap(kind, id, "", v)
	var state map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &state); err != nil {
		return err
	}
	if strings.HasPrefix(kind, "message.") {
		var conversation string
		if err := json.Unmarshal(state["conversation_id"], &conversation); err != nil {
			return err
		}
		e.CorrelationID = conversation
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO entity_events(id,project_id,entity_id,body,company_id) VALUES(?,?,?,?,?)", e.ID, project, id, b, tx.company); err != nil {
		return err
	}
	if err = feed(ctx, tx, feedRow(e, project, id, state)); err != nil {
		return err
	}
	// Persist full audit snapshots in SQLite; broker notifications carry a bounded pointer.
	e.Payload, err = json.Marshal(map[string]any{"entity_id": id, "project_id": project, "conversation_id": state["conversation_id"], "status": state["status"], "version": state["version"]})
	if err != nil {
		return err
	}
	notification, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", e.ID, "factory.events."+kind, notification, tx.company)
	return err
}
func (s *Store) Project(ctx context.Context, company, id string) (tasks.Project, error) {
	return getDoc[tasks.Project](ctx, s.DB, "SELECT body FROM projects WHERE company_id=? AND id=?", company, id)
}
func projectTx(ctx context.Context, tx *Tx, id string) (tasks.Project, error) {
	return getDoc[tasks.Project](ctx, tx, "SELECT body FROM projects WHERE company_id=? AND id=?", tx.company, id)
}
func (s *Store) Projects(ctx context.Context, company string) ([]tasks.Project, error) {
	return listDocs[tasks.Project](ctx, s.DB, "SELECT body FROM projects WHERE company_id=? ORDER BY id", company)
}
func (s *Store) CreateProject(ctx context.Context, company string, in tasks.ProjectCreate) (tasks.Project, error) {
	return mutate(ctx, s, company, "project.create", in.RequestID, in, func(tx *Tx) (tasks.Project, error) {
		p := tasks.Project{ID: in.ID, Name: in.Name, Description: in.Description, Workspace: filepath.Clean(in.Workspace), Version: 1, CreatedAt: tx.now().Unix(), UpdatedAt: tx.now().Unix()}
		if !entityName.MatchString(p.ID) || strings.TrimSpace(p.Name) == "" || !filepath.IsAbs(in.Workspace) {
			return p, inputError("project requires valid id, name and absolute host workspace")
		}
		b, _ := json.Marshal(p)
		if _, err := tx.ExecContext(ctx, "INSERT INTO projects(id,workspace,version,body,company_id) VALUES(?,?,?,?,?)", p.ID, p.Workspace, p.Version, b, tx.company); err != nil {
			return p, err
		}
		return p, entityEvent(ctx, tx, "project.created", p.ID, p.ID, p)
	})
}
func (s *Store) UpdateProject(ctx context.Context, company, id string, in tasks.ProjectUpdate) (tasks.Project, error) {
	return mutate(ctx, s, company, "project.update:"+id, in.RequestID, in, func(tx *Tx) (tasks.Project, error) {
		p, err := projectTx(ctx, tx, id)
		if err != nil {
			return p, err
		}
		if p.Version != in.Version {
			return p, ErrConflict
		}
		if strings.TrimSpace(in.Name) == "" {
			return p, inputError("project name required")
		}
		p.Name = in.Name
		p.Description = in.Description
		p.Version++
		p.UpdatedAt = tx.now().Unix()
		b, _ := json.Marshal(p)
		if _, err = tx.ExecContext(ctx, "UPDATE projects SET version=?,body=? WHERE id=?", p.Version, b, id); err != nil {
			return p, err
		}
		return p, entityEvent(ctx, tx, "project.updated", id, id, p)
	})
}
func (s *Store) Task(ctx context.Context, company, id string) (tasks.Task, error) {
	return getDoc[tasks.Task](ctx, s.DB, "SELECT body FROM tasks WHERE company_id=? AND id=?", company, id)
}
func taskTx(ctx context.Context, tx *Tx, id string) (tasks.Task, error) {
	return getDoc[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND id=?", tx.company, id)
}
func listDocs[T any](ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	vs := []T{}
	for rows.Next() {
		var b []byte
		var v T
		if err = rows.Scan(&b); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		vs = append(vs, v)
	}
	return vs, rows.Err()
}
func (s *Store) Tasks(ctx context.Context, company, project, status string) ([]tasks.Task, error) {
	return listDocs[tasks.Task](ctx, s.DB, "SELECT body FROM tasks WHERE company_id=? AND (?='' OR project_id=?) AND (?='' OR status=?) ORDER BY priority DESC,created_at,id", company, project, project, status, status)
}
func (s *Store) CreateTask(ctx context.Context, company string, in tasks.Create) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.create", in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t := tasks.Task{ID: in.ID, ProjectID: in.ProjectID, Title: in.Title, Description: in.Description, Priority: in.Priority, Assignee: in.Assignee, CreatedBy: in.CreatedBy, ParentTaskID: in.ParentTaskID, OpenSpecChange: in.OpenSpecChange, Dependencies: in.Dependencies, RequiredArtifacts: in.RequiredArtifacts, OriginTask: in.OriginTask, OriginRun: in.OriginRun, Status: "BACKLOG", Version: 1, CreatedAt: tx.now().Unix(), UpdatedAt: tx.now().Unix()}
		if t.ID == "" {
			t.ID = protocol.ID()
		}
		if t.CreatedBy == "" {
			t.CreatedBy = "owner"
		}
		if t.Dependencies == nil {
			t.Dependencies = []string{}
		}
		if !entityName.MatchString(t.ID) || strings.TrimSpace(t.Title) == "" || len(t.Title) > 1024 || len(t.Description) > 64*1024 || t.Priority < -1000 || t.Priority > 1000 {
			return t, inputError("valid task id/title and priority -1000..1000 required")
		}
		if _, err := projectTx(ctx, tx, t.ProjectID); err != nil {
			return t, err
		}
		if dup, err := duplicateTask(ctx, tx, t, in.Roles); err != nil || dup != nil {
			if dup != nil {
				dup.Deduplicated = true
				return *dup, nil
			}
			return t, err
		}
		if err := s.checkTaskChange(ctx, tx, t); err != nil {
			return t, err
		}
		if t.ParentTaskID != "" {
			parent, err := taskTx(ctx, tx, t.ParentTaskID)
			if err != nil {
				return t, err
			}
			if parent.ProjectID != t.ProjectID {
				return t, inputError("parent must belong to same project")
			}
		}
		if err := requiredArtifacts(ctx, tx, t.ProjectID, t.RequiredArtifacts); err != nil {
			return t, err
		}
		b, _ := json.Marshal(t)
		if _, err := tx.ExecContext(ctx, "INSERT INTO tasks(id,project_id,status,assignee,priority,version,body,created_at,company_id) VALUES(?,?,?,?,?,?,?,?,?)", t.ID, t.ProjectID, t.Status, t.Assignee, t.Priority, t.Version, b, t.CreatedAt, tx.company); err != nil {
			return t, err
		}
		if err := dependencies(ctx, tx, t); err != nil {
			return t, err
		}
		return t, entityEvent(ctx, tx, "task.created", t.ProjectID, t.ID, t)
	})
}

// ErrCycle refuses a parent/dependency combination in which a task would wait on itself.
var ErrCycle = fmt.Errorf("%w: cycle", ErrConflict)

func normalTitle(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }

// duplicateTask finds an open task created by the same origin task with the same normalized
// title and an assignee of the same role, so a retried or repeated agent run reuses it.
func duplicateTask(ctx context.Context, tx *Tx, t tasks.Task, roles map[string]string) (*tasks.Task, error) {
	if t.OriginTask == "" {
		return nil, nil
	}
	open, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND project_id=? AND json_extract(body,'$.origin_task')=? AND status NOT IN ('DONE','CANCELLED') ORDER BY created_at,id", tx.company, t.ProjectID, t.OriginTask)
	if err != nil {
		return nil, err
	}
	role := func(agent string) string {
		if agent == "" {
			return ""
		}
		if r, ok := roles[agent]; ok {
			return "role:" + r
		}
		return "agent:" + agent
	}
	for i := range open {
		if normalTitle(open[i].Title) == normalTitle(t.Title) && role(open[i].Assignee) == role(t.Assignee) {
			return &open[i], nil
		}
	}
	return nil, nil
}

// waitsFor reports whether target is reachable from start along "waits for" edges:
// a task waits for its dependencies and a parent waits for its children.
func waitsFor(ctx context.Context, tx *Tx, start, target string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `WITH RECURSIVE reachable(id) AS (
 SELECT ?
 UNION SELECT d.depends_on FROM task_dependencies d JOIN reachable r ON d.task_id=r.id
 UNION SELECT c.id FROM tasks c JOIN reachable r ON json_extract(c.body,'$.parent_task_id')=r.id WHERE c.company_id=?
) SELECT count(*) FROM reachable WHERE id=?`, start, tx.company, target).Scan(&n)
	return n > 0, err
}

func dependencies(ctx context.Context, tx *Tx, t tasks.Task) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM task_dependencies WHERE task_id=?", t.ID); err != nil {
		return err
	}
	if len(t.Dependencies) > 128 {
		return inputError("at most 128 dependencies per task")
	}
	seen := map[string]bool{}
	for _, dep := range t.Dependencies {
		if dep == t.ID || seen[dep] {
			return inputError("self/duplicate dependency")
		}
		seen[dep] = true
		d, err := taskTx(ctx, tx, dep)
		if err != nil {
			return err
		}
		if d.ProjectID != t.ProjectID {
			return inputError("dependencies must belong to same project")
		}
		// The task row (with its parent) already exists, so reaching the parent reaches the task.
		cycle, err := waitsFor(ctx, tx, dep, t.ID)
		if err != nil {
			return err
		}
		if cycle {
			return ErrCycle
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO task_dependencies VALUES(?,?,?)", t.ID, dep, tx.company); err != nil {
			return err
		}
	}
	return nil
}
func saveTask(ctx context.Context, tx *Tx, t *tasks.Task, kind string) error {
	old := t.Version
	t.Version++
	t.UpdatedAt = tx.now().Unix()
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "UPDATE tasks SET status=?,assignee=?,priority=?,version=?,body=? WHERE id=? AND version=?", t.Status, t.Assignee, t.Priority, t.Version, b, t.ID, old)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return entityEvent(ctx, tx, kind, t.ProjectID, t.ID, t)
}
func (s *Store) UpdateTask(ctx context.Context, company, id string, in tasks.Update) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.update:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return t, err
		}
		if t.Version != in.Version {
			return t, ErrConflict
		}
		if t.Status != "BACKLOG" && t.Status != "READY" && t.Status != "REWORK" && t.Status != "BLOCKED" && t.Status != "FAILED" {
			return t, ErrConflict
		}
		if err = editableRun(ctx, tx, t); err != nil {
			return t, err
		}
		if in.Title != nil {
			t.Title = *in.Title
		}
		if in.Description != nil {
			t.Description = *in.Description
		}
		if in.Priority != nil {
			t.Priority = *in.Priority
		}
		previousAssignee := t.Assignee
		if in.Assignee != nil {
			t.Assignee = *in.Assignee
		}
		if in.OpenSpecChange != nil {
			t.OpenSpecChange = *in.OpenSpecChange
			if err = s.checkTaskChange(ctx, tx, t); err != nil {
				return t, err
			}
		}
		if strings.TrimSpace(t.Title) == "" || len(t.Title) > 1024 || len(t.Description) > 64*1024 || t.Priority < -1000 || t.Priority > 1000 {
			return t, inputError("invalid title or priority")
		}
		if in.Dependencies != nil {
			t.Dependencies = *in.Dependencies
			if t.Dependencies == nil {
				t.Dependencies = []string{}
			}
			if err = dependencies(ctx, tx, t); err != nil {
				return t, err
			}
		}
		if in.RequiredArtifacts != nil {
			t.RequiredArtifacts = *in.RequiredArtifacts
			if err = requiredArtifacts(ctx, tx, t.ProjectID, t.RequiredArtifacts); err != nil {
				return t, err
			}
		}
		if t.Status == "READY" || t.Status == "BLOCKED" && strings.HasPrefix(t.BlockReason, "artifact:") {
			if err = applyArtifactWait(ctx, tx, &t); err != nil {
				return t, err
			}
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return t, err
		}
		if t.Assignee != "" && t.Assignee != previousAssignee {
			err = addTrigger(ctx, tx, t, "assignment", isOwner(in.Actor), in.Actor, "", "assigned to "+t.Assignee)
		}
		return t, err
	})
}
func editableRun(ctx context.Context, tx *Tx, t tasks.Task) error {
	if t.CurrentRunID == "" {
		return nil
	}
	var status string
	if err := tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", t.CurrentRunID).Scan(&status); err != nil {
		return err
	}
	if status == "QUEUED" || status == "RUNNING" || status == "CANCELLING" || status == "UNKNOWN" {
		return ErrConflict
	}
	return nil
}
func (s *Store) TaskAction(ctx context.Context, company, id string, in tasks.Action) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.action:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return t, err
		}
		if t.Version != in.Version {
			return t, ErrConflict
		}
		switch in.Action {
		case "ready":
			if t.WaitMessageID != "" {
				answered, err := messageWaitState(ctx, tx, t.WaitMessageID)
				if err != nil {
					return t, err
				}
				if !answered {
					return t, ErrConflict
				}
			}
			if t.Status != "BACKLOG" && t.Status != "REWORK" && t.Status != "FAILED" && t.Status != "BLOCKED" {
				return t, ErrConflict
			}
			if t.Assignee == "" {
				return t, inputError("assignee required")
			}
			if err = editableRun(ctx, tx, t); err != nil {
				return t, err
			}
			if err = s.checkTaskChange(ctx, tx, t); err != nil {
				return t, err
			}
			t.Status = "READY"
			t.BlockReason = ""
			t.CancelRequested = false
			t.RetryEligible = false
			if err = applyArtifactWait(ctx, tx, &t); err != nil {
				return t, err
			}
		case "block":
			if t.Status != "BACKLOG" && t.Status != "READY" && t.Status != "REWORK" && t.Status != "FAILED" {
				return t, ErrConflict
			}
			if strings.TrimSpace(in.Reason) == "" {
				return t, inputError("block reason required")
			}
			if err = editableRun(ctx, tx, t); err != nil {
				return t, err
			}
			t.Status = "BLOCKED"
			t.BlockReason = "manual: " + in.Reason
		case "cancel":
			if t.Status == "DONE" || t.Status == "CANCELLED" {
				return t, ErrConflict
			}
			if t.CurrentRunID != "" {
				var status string
				if err = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", t.CurrentRunID).Scan(&status); err != nil {
					return t, err
				}
				if status == "UNKNOWN" {
					return t, ErrConflict
				}
				if status == "RUNNING" || status == "CANCELLING" {
					if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLING' WHERE id=?", t.CurrentRunID); err != nil {
						return t, err
					}
					t.CancelRequested = true
					break
				}
				if status == "QUEUED" {
					if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLED',finished_at=? WHERE id=?", tx.now().Unix(), t.CurrentRunID); err != nil {
						return t, err
					}
					if err = autoRuntimeReport(ctx, tx, t.CurrentRunID); err != nil {
						return t, err
					}
				}
			}
			t.CancelRequested = true
			t.Status = "CANCELLED"
		default:
			return t, inputError("unknown task action")
		}
		if in.Reason != "" {
			if err = comment(ctx, tx, t.ID, in.Actor, in.Reason); err != nil {
				return t, err
			}
		}
		if err = saveTask(ctx, tx, &t, "task.updated"); err != nil {
			return t, err
		}
		switch in.Action {
		case "ready":
			if err = clearParks(ctx, tx, t, tasks.ParkBlock, anyRef, "cleared"); err == nil {
				err = addTrigger(ctx, tx, t, "ready", isOwner(in.Actor), in.Actor, "", in.Reason)
			}
		case "block":
			err = addPark(ctx, tx, t, tasks.ParkBlock, "", t.BlockReason)
		case "cancel":
			err = clearParks(ctx, tx, t, anyKind, anyRef, "overridden")
		}
		return t, err
	})
}
func (s *Store) SubmitTask(ctx context.Context, company, id string, in tasks.Submit) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.submit:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return t, err
		}
		if t.Version != in.Version || (t.Status != "IN_REVIEW" && t.Status != "IN_PROGRESS") || t.CancelRequested || t.WaitMessageID != "" || t.CurrentRunID != in.RunID {
			return t, ErrConflict
		}
		if strings.TrimSpace(in.Submission.Summary) == "" {
			return t, inputError("submission summary required")
		}
		for _, a := range in.Submission.Artifacts {
			if _, err = registerArtifactTx(ctx, tx, t.ProjectID, artifacts.Register{Artifact: a, TaskID: t.ID, RunID: in.RunID, Actor: in.Actor}); err != nil {
				return t, err
			}
			if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO task_artifacts VALUES(?,?,?,?,?,?)", t.ID, in.RunID, a.ID, a.Version, a.URI, tx.company); err != nil {
				return t, err
			}
		}
		t.Submission = &in.Submission
		err = saveTask(ctx, tx, &t, "task.submitted")
		return t, err
	})
}
func (s *Store) ReviewTask(ctx context.Context, company, id string, in tasks.Review) (tasks.Task, error) {
	return mutate(ctx, s, company, "task.review:"+id, in.RequestID, in, func(tx *Tx) (tasks.Task, error) {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			return t, err
		}
		if t.Version != in.Version || t.Status != "IN_REVIEW" {
			return t, ErrConflict
		}
		if !in.Accept && strings.TrimSpace(in.Feedback) == "" {
			return t, inputError("rejection feedback required")
		}
		if in.Accept {
			t.Status = "DONE"
		} else {
			t.Status = "REWORK"
		}
		t.ReviewFeedback = in.Feedback
		if err = comment(ctx, tx, id, in.Actor, in.Feedback); err != nil {
			return t, err
		}
		if err = saveTask(ctx, tx, &t, "task.reviewed"); err != nil {
			return t, err
		}
		if in.Accept {
			if err = clearParks(ctx, tx, t, anyKind, anyRef, "overridden"); err == nil {
				err = dependencyDone(ctx, tx, t)
			}
		} else {
			err = addTrigger(ctx, tx, t, "review_result", isOwner(in.Actor), in.Actor, "", in.Feedback)
		}
		return t, err
	})
}
func comment(ctx context.Context, tx *Tx, id, author, text string) error {
	if text == "" {
		return nil
	}
	if author == "" {
		author = "owner"
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO task_comments VALUES(?,?,?,?,?,?)", protocol.ID(), id, author, text, tx.now().Unix(), tx.company)
	return err
}
func (s *Store) EntityEvents(ctx context.Context, company, project, id string) ([]protocol.Envelope, error) {
	return listDocs[protocol.Envelope](ctx, s.DB, "SELECT body FROM entity_events WHERE company_id=? AND (?='' OR project_id=?) AND (?='' OR entity_id=?) ORDER BY seq", company, project, project, id, id)
}
