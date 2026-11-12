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
	// factory:// names content Factory holds: only an uploaded version (with its digest) may use it.
	if strings.HasPrefix(a.URI, "factory://") && a.URI != UploadURI(a.ID, a.Version) || a.SHA256 != "" && len(a.SHA256) != 64 || len(a.Name) > 255 || strings.ContainsAny(a.Name, "/\\\r\n\x00") {
		return inputError("factory:// URIs are factory://artifacts/<id>/<version> of an uploaded version (factory_artifact_upload)")
	}
	return nil
}

// UploadURI is the URI of an uploaded artifact version (core-owned-project-data D4).
func UploadURI(id, version string) string { return "factory://artifacts/" + id + "/" + version }

// sameArtifact: an approved or registered version satisfies a reference with the same id, version and URI and,
// when the reference pins one, the same content digest.
func sameArtifact(have, ref tasks.Artifact) bool {
	return have.ID == ref.ID && have.Version == ref.Version && have.URI == ref.URI && (ref.SHA256 == "" || ref.SHA256 == have.SHA256)
}
func (s *Store) Artifact(ctx context.Context, company, project, id, version string) (artifacts.Record, error) {
	return getDoc[artifacts.Record](ctx, s.DB, "SELECT body FROM artifact_versions WHERE company_id=? AND project_id=? AND id=? AND version=?", company, project, id, version)
}
func (s *Store) Artifacts(ctx context.Context, company, project string) ([]artifacts.Record, error) {
	return listDocs[artifacts.Record](ctx, s.DB, "SELECT body FROM artifact_versions WHERE company_id=? AND project_id=? ORDER BY seq", company, project)
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
		// A version is immutable: other content needs a new version (its approval does not carry over). A
		// reference without a digest (a submission naming an uploaded version) is the registered version.
		if !sameArtifact(old.Artifact, a.Artifact) {
			return a, ErrConflict
		}
		if old.ArchivedAt > 0 && old.SHA256 != "" {
			// The same content uploaded again restores an archived version (artifact-retention).
			if _, e := blobRow(ctx, tx, tx.company, old.SHA256); e == nil {
				if ok, e := tx.store.Blobs.Exists(ctx, tx.company, old.SHA256); e == nil && ok {
					old.ArchivedAt, old.ArchivedReason = 0, ""
					b, _ := json.Marshal(old)
					if _, err = tx.ExecContext(ctx, "UPDATE artifact_versions SET archived_at=0, archived_reason='', body=? WHERE company_id=? AND project_id=? AND id=? AND version=?", b, tx.company, project, old.ID, old.Version); err != nil {
						return a, err
					}
				}
			}
		}
		return old, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return a, err
	}
	if (a.SHA256 != "") != (a.URI == UploadURI(a.ID, a.Version)) {
		return a, inputError("factory://artifacts URIs and content digests belong to uploaded versions (factory_artifact_upload)")
	}
	if a.SHA256 != "" {
		blob, err := blobRow(ctx, tx, tx.company, a.SHA256)
		if errors.Is(err, ErrNotFound) {
			return a, inputError("artifact content was not uploaded")
		}
		if err != nil {
			return a, err
		}
		a.Size = blob.Size
		if in.Dedup {
			// The same content as the artifact's latest version is that version, not a new one.
			latest, err := getDoc[artifacts.Record](ctx, tx, "SELECT body FROM artifact_versions WHERE company_id=? AND project_id=? AND id=? ORDER BY seq DESC LIMIT 1", tx.company, project, a.ID)
			if err == nil && latest.SHA256 == a.SHA256 {
				latest.Unchanged = true
				return latest, nil
			}
			if err != nil && !errors.Is(err, ErrNotFound) {
				return a, err
			}
		}
	}
	b, _ := json.Marshal(a)
	if _, err = tx.ExecContext(ctx, "INSERT INTO artifact_versions(project_id,id,version,uri,body,company_id,sha256,size) VALUES(?,?,?,?,?,?,?,?)", project, a.ID, a.Version, a.URI, b, tx.company, a.SHA256, a.Size); err != nil {
		return a, err
	}
	return a, entityEvent(ctx, tx, "artifact.registered", project, a.ID, a)
}
func (s *Store) RegisterArtifact(ctx context.Context, company, project string, in artifacts.Register) (artifacts.Record, error) {
	in.Dedup = true
	rec, err := s.registerArtifact(ctx, company, project, in)
	if err == nil && !rec.Unchanged {
		rec, _ = s.ListArchiveImages(ctx, company, project, rec) // best effort: the version is registered either way
	}
	return rec, err
}

func (s *Store) registerArtifact(ctx context.Context, company, project string, in artifacts.Register) (artifacts.Record, error) {
	return mutate(ctx, s, company, "artifact.register:"+project, in.RequestID, in, func(tx *Tx) (artifacts.Record, error) {
		if in.RequestID == "" {
			return artifacts.Record{}, inputError("request_id required")
		}
		if in.Actor != "owner" {
			t, e := taskTx(ctx, tx, in.TaskID)
			if e != nil {
				return artifacts.Record{}, e
			}
			waiting, e := askPark(ctx, tx, tx.company, t.ID)
			if e != nil {
				return artifacts.Record{}, e
			}
			if t.CurrentRunID != in.RunID || t.Assignee != in.Actor || t.Status != "IN_PROGRESS" || t.CancelRequested || waiting != "" {
				return artifacts.Record{}, ErrConflict
			}
		}
		a, err := registerArtifactTx(ctx, tx, project, in)
		if err == nil && in.RunID != "" {
			_, err = tx.ExecContext(ctx, "INSERT INTO task_artifacts VALUES(?,?,?,?,?,?) ON CONFLICT(company_id,task_id,run_id,id,version) DO NOTHING", in.TaskID, in.RunID, in.Artifact.ID, in.Artifact.Version, in.Artifact.URI, tx.company)
		}
		return a, err
	})
}

const artifactApprovalQuery = "SELECT body FROM approvals WHERE company_id=? AND project_id=? AND kind='artifact' AND artifact_id=? AND artifact_version=? ORDER BY seq DESC LIMIT 1"

func artifactApprovalTx(ctx context.Context, tx *Tx, project, id, version string) (approvals.Approval, error) {
	return getDoc[approvals.Approval](ctx, tx, artifactApprovalQuery, tx.company, project, id, version)
}
func (s *Store) ArtifactApproval(ctx context.Context, company, project, id, version string) (approvals.Approval, error) {
	return getDoc[approvals.Approval](ctx, s.DB, artifactApprovalQuery, company, project, id, version)
}
func (s *Store) RequestArtifactApproval(ctx context.Context, company, project string, in artifacts.ApprovalRequest) (approvals.Approval, error) {
	// The question shows the version itself (inbox-visual-previews), in the chat and in Telegram too.
	_, shown := s.withPreviews(ctx, company, project, nil, []messaging.MaterialRef{{ArtifactID: in.ArtifactID, ArtifactVersion: in.ArtifactVersion}})
	return mutate(ctx, s, company, "artifact.approval:"+project+":"+in.Agent+":"+in.TaskID, in.RequestID, in, func(tx *Tx) (approvals.Approval, error) {
		a := approvals.Approval{ID: protocol.ID(), Kind: "artifact", ProjectID: project, Agent: in.Agent, TaskID: in.TaskID, RunID: in.RunID, Reason: in.Reason, Status: "PENDING", Version: 1, CreatedAt: tx.now().Unix(),
			Audience: in.Audience, Delegate: in.Delegate, Setting: in.Setting}
		if in.RequestID == "" || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 || in.Agent == "" || in.Agent == "owner" || in.TimeoutSeconds < 0 || in.TimeoutSeconds > 7*86400 {
			return a, inputError("agent, reason, request_id and bounded timeout required")
		}
		record, err := artifactVersionTx(ctx, tx, project, in.ArtifactID, in.ArtifactVersion)
		if err != nil {
			return a, err
		}
		if record.ArchivedAt > 0 {
			return a, inputError(tx.store.archivedError(ctx, tx.company, project, record).Error())
		}
		a.Artifact = &record.Artifact
		old, err := artifactApprovalTx(ctx, tx, project, in.ArtifactID, in.ArtifactVersion)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return a, err
		}
		if err == nil && (old.Status == "PENDING" || old.Status == "APPROVED") {
			return a, ErrConflict
		}
		// Older versions' open requests are superseded by this one (artifact-duplicates 1.3): they leave the Inbox and
		// the agent is told; the agent waits for the newest only.
		older, err := listDocs[approvals.Approval](ctx, tx, "SELECT body FROM approvals WHERE company_id=? AND project_id=? AND kind='artifact' AND artifact_id=? AND artifact_version<>? AND status='PENDING'", tx.company, project, in.ArtifactID, in.ArtifactVersion)
		if err != nil {
			return a, err
		}
		for _, o := range older {
			if m, e := messageTx(ctx, tx, o.MessageID); e == nil && (m.Status == "PENDING" || m.Status == "ESCALATED") {
				if _, e = replyMessageTx(ctx, tx, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Approval " + o.ID + " SUPERSEDED by version " + in.ArtifactVersion + ": the newer version is under review.", RequestID: "superseded-" + o.ID}); e != nil {
					return a, e
				}
			}
			o.Status, o.DecisionReason, o.DecidedAt = "SUPERSEDED", "superseded by version "+in.ArtifactVersion, tx.now().Unix()
			if err = saveApproval(ctx, tx, &o, "approval.superseded"); err != nil {
				return a, err
			}
		}
		timeout := in.TimeoutSeconds
		if timeout == 0 {
			timeout = 86400
		}
		a.ExpiresAt = a.CreatedAt + int64(timeout)
		ref, _ := json.Marshal(a.Artifact)
		m, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, TaskID: in.TaskID, RunID: in.RunID, Sender: in.Agent, Recipient: "owner", MessageType: "question", Body: "Artifact approval " + a.ID + ": " + in.Reason + "\nExact artifact version: " + string(ref), Blocking: in.Blocking, TimeoutSeconds: timeout, RequestID: in.RequestID, Materials: shown})
		if err != nil {
			return a, err
		}
		a.MessageID = m.ID
		m.ApprovalID = a.ID
		m.DelegatedTo = a.Delegate
		mb, _ := json.Marshal(m)
		if _, err = tx.ExecContext(ctx, "UPDATE messages SET body=? WHERE id=?", mb, m.ID); err != nil {
			return a, err
		}
		b, _ := json.Marshal(a)
		if _, err = tx.ExecContext(ctx, "INSERT INTO approvals(id,project_id,agent,task_id,fingerprint,message_id,status,body,kind,artifact_id,artifact_version,company_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)", a.ID, project, a.Agent, a.TaskID, "artifact:"+a.ID, m.ID, a.Status, b, "artifact", in.ArtifactID, in.ArtifactVersion, tx.company); err != nil {
			return a, err
		}
		if a.Delegate != "" {
			// The delegate decides it (approval-delegation): it gets the request; people see it once it escalates.
			body := fmt.Sprintf("Approval %s for you to decide: %s asks to approve artifact %s version %s: %s\nYou decide it under %s. Use factory_approval_decide with approval_id %s and action approve or reject (with a reason; cite the owner's earlier answers or recorded decisions you rely on in cites), or escalate when you are unsure, it is out of your remit or it conflicts with a recorded decision: then people decide.", a.ID, in.Agent, in.ArtifactID, in.ArtifactVersion, in.Reason, a.Setting, a.ID)
			if _, err = sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, Sender: "factory", Recipient: a.Delegate, MessageType: "review_request", Body: body, RequestID: "delegate-" + a.ID}); err != nil {
				return a, err
			}
		}
		return a, entityEvent(ctx, tx, "approval.requested", project, a.ID, a)
	})
}

// EscalateApproval hands a delegated approval to people (approval-delegation): it appears in their Inbox.
func (s *Store) EscalateApproval(ctx context.Context, company, id, agent, reason string) (approvals.Approval, error) {
	return mutate(ctx, s, company, "approval.escalate:"+id, "", nil, func(tx *Tx) (approvals.Approval, error) {
		a, err := getDoc[approvals.Approval](ctx, tx, "SELECT body FROM approvals WHERE company_id=? AND id=?", company, id)
		if err != nil {
			return a, err
		}
		if a.Status != "PENDING" || a.Delegate != agent || a.Escalated {
			return a, ErrConflict
		}
		a.Escalated = true
		m, err := messageTx(ctx, tx, a.MessageID)
		if err != nil {
			return a, err
		}
		m.DelegatedTo = ""
		m.Body += "\n" + agent + " handed it to you: " + reason
		if err = saveMessage(ctx, tx, &m, "message.escalated_by_delegate"); err != nil {
			return a, err
		}
		return a, saveApproval(ctx, tx, &a, "approval.escalated")
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
		if err == nil && !sameArtifact(a.Artifact, ref) {
			return inputError("required artifact conflicts with the registered version (URI or content digest)")
		}
		if err == nil && a.ArchivedAt > 0 {
			return inputError(tx.store.archivedError(ctx, tx.company, project, a).Error())
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
		if err != nil || a.Status != "APPROVED" || a.Artifact == nil || !sameArtifact(*a.Artifact, ref) {
			return nil, fmt.Sprintf("artifact: waiting for owner approval of %s@%s", ref.ID, ref.Version), nil
		}
		inputs = append(inputs, tasks.ApprovedArtifact{Artifact: *a.Artifact, ApprovalID: a.ID, ApprovalVersion: a.Version})
	}
	return inputs, "", nil
}

// reconcileArtifactTasks ends the QUEUED runs of tasks whose approved artifact inputs no longer hold (a
// revoked or replaced approval): the run ends stale and the task is READY again, held by its artifacts.
func reconcileArtifactTasks(ctx context.Context, tx *Tx) error {
	ts, err := listDocs[tasks.Task](ctx, tx, "SELECT body FROM tasks WHERE company_id=? AND status='IN_PROGRESS' ORDER BY priority DESC,created_at,id", tx.company)
	if err != nil {
		return err
	}
	for _, t := range ts {
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
		_, reason, err := approvedArtifactInputs(ctx, tx, t)
		if err != nil {
			return err
		}
		saved, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=?", t.CurrentRunID)
		if err != nil {
			return err
		}
		valid, err := approvedRunArtifacts(ctx, tx, saved)
		if err != nil {
			return err
		}
		if reason == "" && valid {
			continue
		}
		if reason == "" {
			reason = "approved artifact inputs changed"
		}
		if _, err = endQueued(ctx, tx, t.CurrentRunID, "stale", reason); err != nil {
			return err
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
