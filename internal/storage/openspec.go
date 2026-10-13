package storage

import (
	"context"
	"fmt"

	"appfactory/internal/openspec"
	"appfactory/internal/tasks"
)

func (s *Store) checkTaskChange(ctx context.Context, tx *Tx, t tasks.Task) error {
	if t.OpenSpecChange == "" {
		return nil
	}
	p, e := projectTx(ctx, tx, t.ProjectID)
	if e != nil {
		return e
	}
	if _, e = s.OpenSpec.CheckChange(p.Workspace, t.OpenSpecChange); e != nil {
		return fmt.Errorf("%w: openspec: %v", ErrInput, e)
	}
	return nil
}
func (s *Store) InitOpenSpec(ctx context.Context, company, project, actor, runID, key string) (openspec.Initialized, error) {
	input := struct{ Project, Actor, RunID string }{project, actor, runID}
	return mutate(ctx, s, company, "openspec.init:"+project+":"+actor+":"+runID, key, input, func(tx *Tx) (openspec.Initialized, error) {
		out := openspec.Initialized{}
		if key == "" {
			return out, inputError("request_id required")
		}
		p, e := projectTx(ctx, tx, project)
		if e != nil {
			return out, e
		}
		var active int
		e = tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE (project=? OR workspace=?) AND status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN') AND id!=?", project, p.Workspace, runID).Scan(&active)
		if e != nil {
			return out, e
		}
		if active != 0 {
			return out, ErrConflict
		}
		if runID != "" {
			var valid int
			if e = tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE company_id=? AND id=? AND project=? AND agent=? AND status='RUNNING' AND lease_until>=?", tx.company, runID, project, actor, s.leaseCutoff()).Scan(&valid); e != nil {
				return out, e
			}
			if valid != 1 {
				return out, ErrConflict
			}
		} else if actor != "owner" {
			return out, inputError("owner or active run required")
		}
		out, e = s.OpenSpec.Init(p.Workspace)
		if e != nil {
			return out, fmt.Errorf("%w: openspec: %v", ErrInput, e)
		}
		out.ProjectID = project
		return out, entityEvent(ctx, tx, "openspec.initialized", project, project, out)
	})
}

func blockOpenSpecRun(ctx context.Context, tx *Tx, id, taskID, reason string) error {
	if _, e := tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLED',result=?,finished_at=? WHERE id=? AND status='QUEUED'", reason, tx.now().Unix(), id); e != nil {
		return e
	}
	if e := autoRuntimeReport(ctx, tx, id); e != nil {
		return e
	}
	if taskID == "" {
		return nil
	}
	t, e := taskTx(ctx, tx, taskID)
	if e != nil {
		return e
	}
	if t.CurrentRunID == id && t.Status == "IN_PROGRESS" {
		t.Status = "BLOCKED"
		t.BlockReason = "openspec: " + reason
		t.RetryEligible = false
		t.Submission = nil
		return saveTask(ctx, tx, &t, "task.openspec_waiting")
	}
	return nil
}
