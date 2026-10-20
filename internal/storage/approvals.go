package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
)

func callFingerprint(call factorytools.Call) string {
	b, _ := json.Marshal(call)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Store) Approval(ctx context.Context, company, id string) (approvals.Approval, error) {
	return getDoc[approvals.Approval](ctx, s.DB, "SELECT body FROM approvals WHERE company_id=? AND id=?", company, id)
}
func (s *Store) Approvals(ctx context.Context, company, project string) ([]approvals.Approval, error) {
	return listDocs[approvals.Approval](ctx, s.DB, "SELECT body FROM approvals WHERE company_id=? AND (?='' OR project_id=?) ORDER BY rowid", company, project, project)
}
func (s *Store) RequestApproval(ctx context.Context, company string, in approvals.Request) (approvals.Approval, error) {
	return mutate(ctx, s, company, "approval.request:"+in.ProjectID+":"+in.Agent+":"+in.TaskID, in.RequestID, in, func(tx *Tx) (approvals.Approval, error) {
		a := approvals.Approval{ID: protocol.ID(), Kind: in.Kind, ProjectID: in.ProjectID, Agent: in.Agent, TaskID: in.TaskID, RunID: in.RunID, Call: in.Call, Reason: in.Reason, Status: "PENDING", Version: 1, CreatedAt: tx.now().Unix()}
		kind, fingerprint := "action", callFingerprint(in.Call)
		if in.Kind == "command" {
			// Fingerprinted by ID: after a grant is used the same argv can be requested again.
			kind, fingerprint = "command", "command:"+a.ID
		} else if in.Kind != "" {
			return a, inputError("unknown approval kind")
		}
		b, err := json.Marshal(in.Call)
		if err != nil || len(b) > 12*1024 || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 || in.RequestID == "" || in.TimeoutSeconds < 0 || in.TimeoutSeconds > 7*86400 {
			return a, inputError("reason, request_id and bounded call/timeout required")
		}
		timeout := in.TimeoutSeconds
		if timeout == 0 {
			timeout = 86400
		}
		a.ExpiresAt = a.CreatedAt + int64(timeout)
		m, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: in.ProjectID, TaskID: in.TaskID, RunID: in.RunID, Sender: in.Agent, Recipient: "owner", MessageType: "question", Body: "Approval " + a.ID + ": " + in.Reason + "\nExact call: " + string(b), Blocking: in.Blocking, TimeoutSeconds: timeout, RequestID: in.RequestID})
		if err != nil {
			return a, err
		}
		a.MessageID = m.ID
		m.ApprovalID = a.ID
		mb, _ := json.Marshal(m)
		if _, err = tx.ExecContext(ctx, "UPDATE messages SET body=? WHERE id=?", mb, m.ID); err != nil {
			return a, err
		}
		data, _ := json.Marshal(a)
		if _, err = tx.ExecContext(ctx, "INSERT INTO approvals(id,project_id,agent,task_id,fingerprint,message_id,status,body,company_id,kind) VALUES(?,?,?,?,?,?,?,?,?,?)", a.ID, a.ProjectID, a.Agent, a.TaskID, fingerprint, a.MessageID, a.Status, data, tx.company, kind); err != nil {
			return a, err
		}
		return a, entityEvent(ctx, tx, "approval.requested", a.ProjectID, a.ID, a)
	})
}
func saveApproval(ctx context.Context, tx *Tx, a *approvals.Approval, kind string) error {
	a.Version++
	b, _ := json.Marshal(a)
	if _, err := tx.ExecContext(ctx, "UPDATE approvals SET status=?,body=? WHERE id=?", a.Status, b, a.ID); err != nil {
		return err
	}
	return entityEvent(ctx, tx, kind, a.ProjectID, a.ID, a)
}
func (s *Store) DecideApproval(ctx context.Context, company, id string, in approvals.Decision) (approvals.Approval, error) {
	return mutate(ctx, s, company, "approval.decision:"+id, in.RequestID, in, func(tx *Tx) (approvals.Approval, error) {
		a, err := getDoc[approvals.Approval](ctx, tx, "SELECT body FROM approvals WHERE company_id=? AND id=?", company, id)
		if err != nil {
			return a, err
		}
		if in.RequestID == "" || strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4096 {
			return a, inputError("request_id and decision reason required")
		}
		if a.Version != in.Version {
			return a, ErrConflict
		}
		if in.Action == "revoke" {
			if a.Status != "APPROVED" {
				return a, ErrConflict
			}
			a.Status = "REVOKED"
		} else {
			if a.Status != "PENDING" || a.ExpiresAt <= tx.now().Unix() {
				return a, ErrConflict
			}
			switch in.Action {
			case "approve":
				a.Status = "APPROVED"
				if a.Kind == "artifact" {
					a.ExpiresAt = 0
				}
			case "reject":
				a.Status = "REJECTED"
			default:
				return a, inputError("action must be approve, reject or revoke")
			}
			m, err := messageTx(ctx, tx, a.MessageID)
			if err != nil {
				return a, err
			}
			if _, err = replyMessageTx(ctx, tx, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Approval " + a.ID + " " + a.Status + ": " + in.Reason, RequestID: in.RequestID}); err != nil {
				return a, err
			}
		}
		a.DecidedAt = tx.now().Unix()
		a.DecisionReason = in.Reason
		err = saveApproval(ctx, tx, &a, "approval.resolved")
		if err == nil && a.Kind == "artifact" {
			err = reconcileArtifactTasks(ctx, tx)
		}
		if err == nil && a.Kind == "artifact" && a.Status == "APPROVED" && a.Artifact != nil {
			err = artifactApproved(ctx, tx, a.ProjectID, *a.Artifact, a.ID, in.Urgent)
		}
		return a, err
	})
}

// CallApproval looks up an exact logical intent, including its mutation request ID.
func (s *Store) CallApproval(ctx context.Context, company, project, agent, task string, call factorytools.Call) (approvals.Approval, error) {
	return getDoc[approvals.Approval](ctx, s.DB, "SELECT body FROM approvals WHERE company_id=? AND kind='action' AND project_id=? AND agent=? AND task_id=? AND fingerprint=?", company, project, agent, task, callFingerprint(call))
}
func (s *Store) ExpireApprovals(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT body FROM approvals WHERE company_id=? AND status IN ('PENDING','APPROVED') AND (kind IN ('action','command') OR status='PENDING') AND json_extract(body,'$.expires_at')<=?", company, tx.now().Unix())
	if err != nil {
		return err
	}
	var items []approvals.Approval
	for rows.Next() {
		var b []byte
		var a approvals.Approval
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(b, &a); err != nil {
			rows.Close()
			return err
		}
		items = append(items, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, a := range items {
		if a.Status == "PENDING" {
			m, e := messageTx(ctx, tx, a.MessageID)
			if e != nil {
				return e
			}
			if _, e = replyMessageTx(ctx, tx, m.ID, messaging.Reply{Version: m.Version, Sender: "owner", Body: "Approval " + a.ID + " EXPIRED; action is not authorized.", RequestID: "expire-" + a.ID}); e != nil {
				return e
			}
		}
		a.Status = "EXPIRED"
		if err = saveApproval(ctx, tx, &a, "approval.expired"); err != nil {
			return err
		}
	}
	return tx.Commit()
}
