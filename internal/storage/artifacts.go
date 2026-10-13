package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/artifacts"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func validArtifact(a tasks.Artifact) error {
	if !entityName.MatchString(a.ID) || strings.TrimSpace(a.Version) == "" || len(a.Version) > 128 || strings.ContainsAny(a.Version, "\r\n\x00") || strings.TrimSpace(a.URI) == "" || len(a.URI) > 4096 || strings.ContainsAny(a.URI, "\r\n\x00") {
		return inputError("artifact requires valid id, version up to 128 and immutable URI up to 4096 bytes")
	}
	return nil
}
func (s *Store) Artifact(ctx context.Context, company, project, id, version string) (artifacts.Record, error) {
	return getDoc[artifacts.Record](ctx, s.DB, "SELECT body FROM artifact_versions WHERE company_id=? AND project_id=? AND id=? AND version=?", company, project, id, version)
}
func (s *Store) Artifacts(ctx context.Context, company, project string) ([]artifacts.Record, error) {
	return listDocs[artifacts.Record](ctx, s.DB, "SELECT body FROM artifact_versions WHERE company_id=? AND project_id=? ORDER BY rowid", company, project)
}
func artifactVersionTx(ctx context.Context, tx *Tx, project, id, version string) (artifacts.Record, error) {
	return getDoc[artifacts.Record](ctx, tx, "SELECT body FROM artifact_versions WHERE company_id=? AND project_id=? AND id=? AND version=?", tx.company, project, id, version)
}
func registerArtifactTx(ctx context.Context, tx *Tx, project string, in artifacts.Register) (artifacts.Record, error) {
	if in.Actor == "" {
		in.Actor = "owner"
	}
	a := artifacts.Record{Artifact: in.Artifact, ProjectID: project, TaskID: in.TaskID, RunID: in.RunID, CreatedBy: in.Actor, CreatedAt: tx.now().Unix()}
	if err := validArtifact(in.Artifact); err != nil {
		return a, err
	}
	t, err := taskTx(ctx, tx, in.TaskID)
	if err != nil {
		return a, err
	}
	if t.ProjectID != project {
		return a, inputError("artifact task outside project")
	}
	if in.RunID != "" {
		var taskID sql.NullString
		if err = tx.QueryRowContext(ctx, "SELECT task_id FROM runs WHERE company_id=? AND id=?", tx.company, in.RunID).Scan(&taskID); err != nil {
			return a, err
		}
		if taskID.String != in.TaskID {
			return a, inputError("artifact run outside task")
		}
	}
	var conflicts int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM task_artifacts a JOIN tasks t ON t.id=a.task_id WHERE t.project_id=? AND a.id=? AND a.version=? AND a.uri!=?", project, a.ID, a.Version, a.URI).Scan(&conflicts); err != nil {
		return a, err
	}
	if conflicts > 0 {
		return a, ErrConflict
	}
	old, err := artifactVersionTx(ctx, tx, project, a.ID, a.Version)
	if err == nil {
		if old.URI != a.URI {
			return a, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return a, err
	}
	b, _ := json.Marshal(a)
	if _, err = tx.ExecContext(ctx, "INSERT INTO artifact_versions VALUES(?,?,?,?,?,?)", project, a.ID, a.Version, a.URI, b, tx.company); err != nil {
		return a, err
	}
	return a, entityEvent(ctx, tx, "artifact.registered", project, a.ID, a)
}
func (s *Store) RegisterArtifact(ctx context.Context, company, project string, in artifacts.Register) (artifacts.Record, error) {
	return mutate(ctx, s, company, "artifact.register:"+project, in.RequestID, in, func(tx *Tx) (artifacts.Record, error) {
		if in.RequestID == "" {
			return artifacts.Record{}, inputError("request_id required")
		}
		if in.Actor != "owner" {
			t, e := taskTx(ctx, tx, in.TaskID)
			if e != nil {
				return artifacts.Record{}, e
			}
			if t.CurrentRunID != in.RunID || t.Assignee != in.Actor || t.Status != "IN_PROGRESS" || t.CancelRequested || t.WaitMessageID != "" {
				return artifacts.Record{}, ErrConflict
			}
		}
		a, err := registerArtifactTx(ctx, tx, project, in)
		if err == nil && in.RunID != "" {
			_, err = tx.ExecContext(ctx, "INSERT INTO task_artifacts VALUES(?,?,?,?,?,?) ON CONFLICT(task_id,run_id,id,version) DO NOTHING", in.TaskID, in.RunID, in.Artifact.ID, in.Artifact.Version, in.Artifact.URI, tx.company)
		}
		return a, err
	})
}

const artifactApprovalQuery = "SELECT body FROM approvals WHERE company_id=? AND project_id=? AND kind='artifact' AND artifact_id=? AND artifact_version=? ORDER BY rowid DESC LIMIT 1"

func artifactApprovalTx(ctx context.Context, tx *Tx, project, id, version string) (approvals.Approval, error) {
	return getDoc[approvals.Approval](ctx, tx, artifactApprovalQuery, tx.company, project, id, version)
}
func (s *Store) ArtifactApproval(ctx context.Context, company, project, id, version string) (approvals.Approval, error) {
	return getDoc[approvals.Approval](ctx, s.DB, artifactApprovalQuery, company, project, id, version)
}
func (s *Store) RequestArtifactApproval(ctx context.Context, company, project string, in artifacts.ApprovalRequest) (approvals.Approval, error) {
	return mutate(ctx, s, company, "artifact.approval:"+project+":"+in.Agent+":"+in.TaskID, in.RequestID, in, func(tx *Tx) (approvals.Approval, error) {
		a := approvals.Approval{ID: protocol.ID(), Kind: "artifact", ProjectID: project, Agent: in.Agent, TaskID: in.TaskID, RunID: in.RunID, Reason: in.Reason, Status: "PENDING", Version: 1, CreatedAt: tx.now().Unix()}
		if in.RequestID == "" || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 || in.Agent == "" || in.Agent == "owner" || in.TimeoutSeconds < 0 || in.TimeoutSeconds > 7*86400 {
			return a, inputError("agent, reason, request_id and bounded timeout required")
		}
		record, err := artifactVersionTx(ctx, tx, project, in.ArtifactID, in.ArtifactVersion)
		if err != nil {
			return a, err
		}
		a.Artifact = &record.Artifact
		old, err := artifactApprovalTx(ctx, tx, project, in.ArtifactID, in.ArtifactVersion)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return a, err
		}
		if err == nil && (old.Status == "PENDING" || old.Status == "APPROVED") {
			return a, ErrConflict
		}
		timeout := in.TimeoutSeconds
		if timeout == 0 {
			timeout = 86400
		}
		a.ExpiresAt = a.CreatedAt + int64(timeout)
		ref, _ := json.Marshal(a.Artifact)
		m, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, TaskID: in.TaskID, RunID: in.RunID, Sender: in.Agent, Recipient: "owner", MessageType: "question", Body: "Artifact approval " + a.ID + ": " + in.Reason + "\nExact artifact version: " + string(ref), Blocking: in.Blocking, TimeoutSeconds: timeout, RequestID: in.RequestID})
		if err != nil {
			return a, err
		}
		a.MessageID = m.ID
		m.ApprovalID = a.ID
		mb, _ := json.Marshal(m)
		if _, err = tx.ExecContext(ctx, "UPDATE messages SET body=? WHERE id=?", mb, m.ID); err != nil {
			return a, err
		}
		b, _ := json.Marshal(a)
		if _, err = tx.ExecContext(ctx, "INSERT INTO approvals(id,project_id,agent,task_id,fingerprint,message_id,status,body,kind,artifact_id,artifact_version,company_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", a.ID, project, a.Agent, a.TaskID, "artifact:"+a.ID, m.ID, a.Status, b, "artifact", in.ArtifactID, in.ArtifactVersion, tx.company); err != nil {
			return a, err
		}
		return a, entityEvent(ctx, tx, "approval.requested", project, a.ID, a)
	})
}
func requiredArtifacts(ctx context.Context, tx *Tx, project string, refs []tasks.Artifact) error {
	if len(refs) > 64 {
		return inputError("at most 64 required artifacts")
	}
	seen := map[string]bool{}
	for _, ref := range refs {
		if err := validArtifact(ref); err != nil {
			return err
		}
		key := ref.ID + "\x00" + ref.Version
		if seen[key] {
			return inputError("duplicate required artifact version")
		}
		seen[key] = true
		a, err := artifactVersionTx(ctx, tx, project, ref.ID, ref.Version)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && a.URI != ref.URI {
			return inputError("required artifact URI conflicts with registered version")
		}
	}
	return nil
}
func approvedArtifactInputs(ctx context.Context, tx *Tx, t tasks.Task) ([]tasks.ApprovedArtifact, string, error) {
	inputs := []tasks.ApprovedArtifact{}
	for _, ref := range t.RequiredArtifacts {
		a, err := artifactApprovalTx(ctx, tx, t.ProjectID, ref.ID, ref.Version)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, "", err
		}
		if err != nil || a.Status != "APPROVED" || a.Artifact == nil || *a.Artifact != ref {
			return nil, fmt.Sprintf("artifact: waiting for owner approval of %s@%s", ref.ID, ref.Version), nil
		}
		inputs = append(inputs, tasks.ApprovedArtifact{Artifact: ref, ApprovalID: a.ID, ApprovalVersion: a.Version})
	}
	return inputs, "", nil
}
func applyArtifactWait(ctx context.Context, tx *Tx, t *tasks.Task) error {
	_, reason, err := approvedArtifactInputs(ctx, tx, *t)
	if err != nil {
		return err
	}
	if reason != "" {
		t.Status = "BLOCKED"
		t.BlockReason = reason
		t.RetryEligible = false
	} else if t.Status == "BLOCKED" && strings.HasPrefix(t.BlockReason, "artifact:") {
		t.Status = "READY"
		t.BlockReason = ""
	}
	return nil
}
func reconcileArtifactTasks(ctx context.Context, tx *Tx) error {
	ts, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND status IN ('READY','BLOCKED','IN_PROGRESS')", tx.company)
	if err != nil {
		return err
	}
	for _, t := range ts {
		if t.Status == "BLOCKED" && !strings.HasPrefix(t.BlockReason, "artifact:") {
			continue
		}
		if t.Status == "IN_PROGRESS" {
			if len(t.RequiredArtifacts) == 0 {
				continue
			}
			var status string
			if err = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", t.CurrentRunID).Scan(&status); err != nil {
				return err
			}
			if status != "QUEUED" {
				continue
			}
			_, reason, e := approvedArtifactInputs(ctx, tx, t)
			if e != nil {
				return e
			}
			saved, e := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=?", t.CurrentRunID)
			if e != nil {
				return e
			}
			valid, e := approvedRunArtifacts(ctx, tx, saved)
			if e != nil {
				return e
			}
			if reason == "" && valid {
				continue
			}
			if _, err = tx.ExecContext(ctx, "UPDATE runs SET status='CANCELLED',finished_at=? WHERE id=? AND status='QUEUED'", tx.now().Unix(), t.CurrentRunID); err != nil {
				return err
			}
			if err = autoRuntimeReport(ctx, tx, t.CurrentRunID); err != nil {
				return err
			}
			t.Status = "BLOCKED"
			if reason == "" {
				t.Status = "READY"
			}
			t.BlockReason = reason
			t.RetryEligible = false
			if err = saveTask(ctx, tx, &t, "task.artifact_waiting"); err != nil {
				return err
			}
			continue
		}
		if err = editableRun(ctx, tx, t); errors.Is(err, ErrConflict) {
			continue
		} else if err != nil {
			return err
		}
		oldStatus, oldReason := t.Status, t.BlockReason
		if err = applyArtifactWait(ctx, tx, &t); err != nil {
			return err
		}
		if oldStatus != t.Status || oldReason != t.BlockReason {
			if err = saveTask(ctx, tx, &t, "task.artifact_waiting"); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) ReconcileArtifactTasks(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = reconcileArtifactTasks(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Runtime startup validates the pinned decisions again, atomically with claiming.
func approvedRunArtifacts(ctx context.Context, tx *Tx, spec protocol.RunSpec) (bool, error) {
	for _, input := range spec.ApprovedArtifacts {
		a, err := getDoc[approvals.Approval](ctx, tx, "SELECT body FROM approvals WHERE company_id=? AND id=?", tx.company, input.ApprovalID)
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if a.Kind != "artifact" || a.ProjectID != spec.Project || a.Status != "APPROVED" || a.Version != input.ApprovalVersion || a.Artifact == nil || *a.Artifact != input.Artifact {
			return false, nil
		}
	}
	return true, nil
}
