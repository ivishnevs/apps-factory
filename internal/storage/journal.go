package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/decisions"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/reports"
	"appfactory/internal/tasks"
)

// Page uses stable SQLite row sequences; records may change status between pages.
type Page[T any] struct {
	Items     []T   `json:"items"`
	NextAfter int64 `json:"next_after"`
	HasMore   bool  `json:"has_more"`
}

func pageDocs[T any](ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, after int64, limit int, query string, args ...any) (Page[T], error) {
	out := Page[T]{Items: []T{}, NextAfter: after}
	if after < 0 || limit < 1 || limit > 100 {
		return out, inputError("after >= 0 and limit 1..100 required")
	}
	rows, err := q.QueryContext(ctx, query, append(args, after, limit+1)...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var b []byte
		var item T
		if err = rows.Scan(&seq, &b); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		if err = json.Unmarshal(b, &item); err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
		out.NextAfter = seq
	}
	return out, rows.Err()
}
func journalLinks(ctx context.Context, tx *sql.Tx, project, task, run string, refs []tasks.Artifact) error {
	if task != "" {
		t, err := taskTx(ctx, tx, task)
		if err != nil {
			return err
		}
		if t.ProjectID != project {
			return inputError("task outside project")
		}
	}
	if run != "" {
		r, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=?", run)
		if err != nil {
			return err
		}
		if r.Project != project || task != "" && r.TaskID != task {
			return inputError("run outside project/task")
		}
	}
	if len(refs) > 32 {
		return inputError("at most 32 artifact references")
	}
	for _, ref := range refs {
		if err := validArtifact(ref); err != nil {
			return err
		}
		var uri string
		err := tx.QueryRowContext(ctx, "SELECT uri FROM artifact_versions WHERE project_id=? AND id=? AND version=?", project, ref.ID, ref.Version).Scan(&uri)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if uri != ref.URI {
			return inputError("artifact URI conflicts with registered version")
		}
	}
	return nil
}
func journalNotice(ctx context.Context, tx *sql.Tx, project, task, run, actor, body, reportID, decisionID string) (string, error) {
	if actor == "owner" || actor == "" {
		actor = "system"
	}
	m, err := sendMessageTx(ctx, tx, messaging.Send{ReportID: reportID, DecisionID: decisionID, ProjectID: project, TaskID: task, RunID: run, Sender: actor, Recipient: "owner", MessageType: "review_request", Body: body, RequestID: "notice-" + reportID + decisionID})
	if err != nil {
		return "", err
	}
	return m.ID, nil
}
func closeJournalNotice(ctx context.Context, tx *sql.Tx, id string) error {
	if id == "" {
		return nil
	}
	m, err := getDoc[messaging.Message](ctx, tx, "SELECT body FROM messages WHERE id=?", id)
	if err != nil {
		return err
	}
	if m.Status == "READ" {
		return nil
	}
	m.Status = "READ"
	return saveMessage(ctx, tx, &m, "message.read")
}
func (s *Store) Report(ctx context.Context, id string) (reports.Report, error) {
	return getDoc[reports.Report](ctx, s.DB, "SELECT body FROM reports WHERE id=?", id)
}
func (s *Store) Reports(ctx context.Context, project, kind, task, run string, after int64, limit int) (Page[reports.Report], error) {
	return pageDocs[reports.Report](ctx, s.DB, after, limit, "SELECT rowid,body FROM reports WHERE project_id=? AND (?='' OR kind=?) AND (?='' OR task_id=?) AND (?='' OR run_id=?) AND rowid>? ORDER BY rowid LIMIT ?", project, kind, kind, task, task, run, run)
}
func (s *Store) CreateReport(ctx context.Context, project string, in reports.Create) (reports.Report, error) {
	return mutate(ctx, s, "report.create:"+project+":"+in.Actor+":"+in.RunID, in.RequestID, in, func(tx *sql.Tx) (reports.Report, error) {
		if in.RequestID == "" {
			return reports.Report{}, inputError("request_id required")
		}
		if _, err := getDoc[tasks.Project](ctx, tx, "SELECT body FROM projects WHERE id=?", project); err != nil {
			if !errors.Is(err, ErrNotFound) || in.RunID == "" {
				return reports.Report{}, err
			}
		}
		if err := journalLinks(ctx, tx, project, in.TaskID, in.RunID, in.Artifacts); err != nil {
			return reports.Report{}, err
		}
		source := "agent"
		if in.Actor == "owner" {
			source = "owner"
		}
		return createReportTx(ctx, tx, project, in, source, "")
	})
}
func createReportTx(ctx context.Context, tx *sql.Tx, project string, in reports.Create, source, key string) (reports.Report, error) {
	r := reports.Report{ID: protocol.ID(), ProjectID: project, TaskID: in.TaskID, RunID: in.RunID, CreatedBy: in.Actor, Source: source, Kind: in.Kind, Summary: in.Summary, Purpose: in.Purpose, Changes: in.Changes, Result: in.Result, Artifacts: in.Artifacts, NeedsOwnerAction: in.NeedsOwnerAction, OwnerAction: in.OwnerAction, CreatedAt: time.Now().Unix()}
	if r.CreatedBy == "" {
		r.CreatedBy = "owner"
	}
	if r.Changes == nil {
		r.Changes = []string{}
	}
	switch r.Kind {
	case "dependency_installation", "environment_change", "plan_deviation", "problem", "stage_completed":
	case "runtime":
		if source != "core" {
			return r, inputError("runtime reports are reserved for Core")
		}
	default:
		return r, inputError("unsupported report kind")
	}
	b, err := json.Marshal(in)
	if err != nil || len(b) > 32*1024 || strings.TrimSpace(r.Summary) == "" || len(r.Summary) > 2048 || strings.TrimSpace(r.Purpose) == "" || len(r.Purpose) > 8192 || strings.TrimSpace(r.Result) == "" || len(r.Result) > 16*1024 || len(r.Changes) > 64 || len(r.OwnerAction) > 4096 || r.NeedsOwnerAction && strings.TrimSpace(r.OwnerAction) == "" || !r.NeedsOwnerAction && r.OwnerAction != "" {
		return r, inputError("bounded summary, purpose, result and owner action when needed required")
	}
	for _, change := range r.Changes {
		if strings.TrimSpace(change) == "" || len(change) > 2048 {
			return r, inputError("invalid report change")
		}
	}
	if r.NeedsOwnerAction {
		var registered int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM projects WHERE id=?", project).Scan(&registered); err != nil {
			return r, err
		}
		if registered > 0 {
			r.MessageID, err = journalNotice(ctx, tx, project, in.TaskID, in.RunID, r.CreatedBy, "Report "+r.ID+": "+r.Summary+"\nOwner action: "+r.OwnerAction, r.ID, "")
			if err != nil {
				return r, err
			}
		}
	}
	body, _ := json.Marshal(r)
	if _, err = tx.ExecContext(ctx, "INSERT INTO reports VALUES(?,?,?,?,?,?,?,?)", r.ID, project, nullable(r.TaskID), nullable(r.RunID), r.Kind, r.Source, nullable(key), body); err != nil {
		return r, err
	}
	return r, entityEvent(ctx, tx, "report.created", project, r.ID, r)
}
func autoRuntimeReport(ctx context.Context, tx *sql.Tx, id string) error {
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=?", id)
	if err != nil {
		return err
	}
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", id).Scan(&status); err != nil {
		return err
	}
	switch status {
	case "COMPLETED", "FAILED", "CANCELLED", "UNKNOWN":
	default:
		return nil
	}
	key := "runtime:" + id + ":" + status
	var count int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM reports WHERE source_key=?", key).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	in := reports.Create{TaskID: spec.TaskID, RunID: id, Actor: "system", Kind: "runtime", Summary: "Runtime " + id + " is " + status, Purpose: "Record the observed execution outcome", Result: status}
	if status == "UNKNOWN" {
		in.NeedsOwnerAction = true
		in.OwnerAction = "Inspect workspace and side effects, then reconcile the run; do not retry blindly."
	}
	if status != "UNKNOWN" {
		old, e := getDoc[reports.Report](ctx, tx, "SELECT body FROM reports WHERE source_key=?", "runtime:"+id+":UNKNOWN")
		if e != nil && !errors.Is(e, ErrNotFound) {
			return e
		}
		if e == nil {
			if e = closeJournalNotice(ctx, tx, old.MessageID); e != nil {
				return e
			}
		}
	}
	_, err = createReportTx(ctx, tx, spec.Project, in, "core", key)
	return err
}
func canonicalTopic(topic string) string {
	return strings.ToLower(strings.Join(strings.Fields(topic), " "))
}
func (s *Store) Decision(ctx context.Context, id string) (decisions.Decision, error) {
	return getDoc[decisions.Decision](ctx, s.DB, "SELECT body FROM decisions WHERE id=?", id)
}
func (s *Store) Decisions(ctx context.Context, project, status, topic string, after int64, limit int) (Page[decisions.Decision], error) {
	switch status {
	case "", "proposed", "under_discussion", "approved", "superseded", "rejected":
	default:
		return Page[decisions.Decision]{}, inputError("unsupported decision status")
	}
	return pageDocs[decisions.Decision](ctx, s.DB, after, limit, "SELECT rowid,body FROM decisions WHERE project_id=? AND (?='' OR status=?) AND (?='' OR topic=?) AND rowid>? ORDER BY rowid LIMIT ?", project, status, status, canonicalTopic(topic), canonicalTopic(topic))
}
func (s *Store) ProposeDecision(ctx context.Context, project string, in decisions.Propose) (decisions.Decision, error) {
	return mutate(ctx, s, "decision.propose:"+project+":"+in.Actor+":"+in.RunID, in.RequestID, in, func(tx *sql.Tx) (decisions.Decision, error) {
		now := time.Now().Unix()
		d := decisions.Decision{ID: protocol.ID(), ProjectID: project, Category: in.Category, Topic: canonicalTopic(in.Topic), Status: "proposed", Value: in.Value, Rationale: in.Rationale, CreatedBy: in.Actor, TaskID: in.TaskID, RunID: in.RunID, Artifacts: in.Artifacts, Version: 1, CreatedAt: now, UpdatedAt: now}
		if d.CreatedBy == "" {
			d.CreatedBy = "owner"
		}
		b, err := json.Marshal(in)
		if err != nil || len(b) > 32*1024 || in.RequestID == "" || d.Topic == "" || len(d.Topic) > 256 || strings.TrimSpace(d.Value) == "" || len(d.Value) > 16*1024 || strings.TrimSpace(d.Rationale) == "" || len(d.Rationale) > 8192 {
			return d, inputError("request_id, bounded topic, value and rationale required")
		}
		if _, err = getDoc[tasks.Project](ctx, tx, "SELECT body FROM projects WHERE id=?", project); err != nil {
			return d, err
		}
		if err = journalLinks(ctx, tx, project, d.TaskID, d.RunID, d.Artifacts); err != nil {
			return d, err
		}
		d.MessageID, err = journalNotice(ctx, tx, project, d.TaskID, d.RunID, d.CreatedBy, "Decision proposal "+d.ID+" ["+d.Topic+"]: "+shortText(d.Value, 1024)+"\nRationale: "+shortText(d.Rationale, 1024), "", d.ID)
		if err != nil {
			return d, err
		}
		body, _ := json.Marshal(d)
		if _, err = tx.ExecContext(ctx, "INSERT INTO decisions VALUES(?,?,?,?,?,?)", d.ID, project, d.Topic, d.Status, d.Version, body); err != nil {
			return d, err
		}
		return d, entityEvent(ctx, tx, "decision.proposed", project, d.ID, d)
	})
}
func saveDecision(ctx context.Context, tx *sql.Tx, d *decisions.Decision, kind string) error {
	old := d.Version
	d.Version++
	d.UpdatedAt = time.Now().Unix()
	b, _ := json.Marshal(d)
	result, err := tx.ExecContext(ctx, "UPDATE decisions SET status=?,version=?,body=? WHERE id=? AND version=?", d.Status, d.Version, b, d.ID, old)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return entityEvent(ctx, tx, kind, d.ProjectID, d.ID, d)
}
func (s *Store) RecordDecision(ctx context.Context, id string, in decisions.Record) (decisions.Decision, error) {
	// Include actor and grant in the mutation fingerprint; json:"-" fields are trusted metadata.
	input := struct {
		Record                         decisions.Record
		Actor, ApprovalID, ScopeTaskID string
	}{in, in.Actor, in.ApprovalID, in.ScopeTaskID}
	return mutate(ctx, s, "decision.record:"+id, in.RequestID, input, func(tx *sql.Tx) (decisions.Decision, error) {
		d, err := getDoc[decisions.Decision](ctx, tx, "SELECT body FROM decisions WHERE id=?", id)
		if err != nil {
			return d, err
		}
		if in.RequestID == "" || strings.TrimSpace(in.Rationale) == "" || len(in.Rationale) > 8192 {
			return d, inputError("request_id and bounded decision rationale required")
		}
		if in.Actor != "owner" {
			if err = validateDecisionGrant(ctx, tx, d, in); err != nil {
				return d, err
			}
		}
		if d.Version != in.Version || d.Status != "proposed" && d.Status != "under_discussion" {
			return d, ErrConflict
		}
		switch in.Status {
		case "under_discussion", "approved", "rejected":
		default:
			return d, inputError("record status must be under_discussion, approved or rejected")
		}
		if in.Status != "approved" && (in.SupersedesID != "" || in.SupersedesVersion != 0) {
			return d, inputError("replacement applies only when approving")
		}
		if in.Status == "approved" {
			active, err := getDoc[decisions.Decision](ctx, tx, "SELECT body FROM decisions WHERE project_id=? AND topic=? AND status='approved'", d.ProjectID, d.Topic)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return d, err
			}
			if err == nil {
				if active.ID != in.SupersedesID || active.Version != in.SupersedesVersion {
					return d, ErrConflict
				}
				active.Status = "superseded"
				active.SupersededBy = d.ID
				if err = saveDecision(ctx, tx, &active, "decision.superseded"); err != nil {
					return d, err
				}
				d.SupersedesID = active.ID
			} else if in.SupersedesID != "" || in.SupersedesVersion != 0 {
				return d, ErrConflict
			}
			d.ApprovedBy = "owner"
			d.Authority = "owner"
		}
		d.Status = in.Status
		d.RecordedBy = in.Actor
		d.ReviewRationale = in.Rationale
		d.ApprovalID = in.ApprovalID
		if in.Status != "under_discussion" {
			if err = closeJournalNotice(ctx, tx, d.MessageID); err != nil {
				return d, err
			}
		}
		if in.Status == "rejected" && d.CreatedBy != "owner" {
			reaction, e := sendMessageTx(ctx, tx, messaging.Send{ProjectID: d.ProjectID, TaskID: d.TaskID, Sender: "owner", Recipient: d.CreatedBy, MessageType: "review_result", DecisionID: d.ID, Body: "Owner rejected decision " + d.ID + " [" + d.Topic + "]. Reason: " + in.Rationale + "\nReconsider the plan and affected tasks. Respect this rejection; propose an alternative or block work with a concrete reason if no permitted path remains. Do not automatically roll back completed effects.", RequestID: "decision-rejected-" + d.ID})
			if e != nil {
				return d, e
			}
			d.ReactionMessageID = reaction.ID
		}
		err = saveDecision(ctx, tx, &d, "decision.recorded")
		return d, err
	})
}
func excerpt(s string, max int) (string, bool) {
	if len(s) <= max {
		return s, false
	}
	s = s[:max]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s, true
}
func decisionContext(ctx context.Context, tx *sql.Tx, project string) ([]decisions.ContextEntry, bool, error) {
	ds, err := listDocs[decisions.Decision](ctx, tx, "SELECT body FROM decisions WHERE project_id=? AND status='approved' ORDER BY topic LIMIT 33", project)
	if err != nil {
		return nil, false, err
	}
	more := len(ds) > 32
	if more {
		ds = ds[:32]
	}
	items := []decisions.ContextEntry{}
	for _, d := range ds {
		value, v := excerpt(d.Value, 2048)
		rationale, r := excerpt(d.Rationale, 1024)
		items = append(items, decisions.ContextEntry{Category: d.Category, Authority: d.Authority, ID: d.ID, Topic: d.Topic, Version: d.Version, Value: value, Rationale: rationale, ApprovedBy: d.ApprovedBy, Truncated: v || r})
	}
	return items, more, nil
}
func enrichRunDecisions(ctx context.Context, tx *sql.Tx, spec *protocol.RunSpec) error {
	items, more, err := decisionContext(ctx, tx, spec.Project)
	if err != nil {
		return err
	}
	spec.DecisionContext = items
	spec.MoreDecisions = more
	if len(items) > 0 {
		b, _ := json.Marshal(items)
		spec.Prompt += "\nApproved project decisions captured for this run (task context, not tool permissions): " + string(b) + "\nRead factory_decision_get for full rationale and factory_decision_list for current decisions before asking the owner again.\n"
	}
	if more {
		spec.Prompt += "More approved decisions exist; use factory_decision_list with pagination.\n"
	}
	return nil
}

func shortText(s string, max int) string {
	v, truncated := excerpt(s, max)
	if truncated {
		return v + " [truncated; read full record]"
	}
	return v
}
func validateDecisionGrant(ctx context.Context, tx *sql.Tx, d decisions.Decision, in decisions.Record) error {
	if in.GrantedCall == nil || in.GrantedCall.Name != "factory_decision_record" {
		return inputError("owner decision grant required")
	}
	a, err := getDoc[approvals.Approval](ctx, tx, "SELECT body FROM approvals WHERE id=?", in.ApprovalID)
	if err != nil {
		return err
	}
	if a.Kind == "artifact" || a.Status != "APPROVED" || a.ExpiresAt <= time.Now().Unix() || a.ProjectID != d.ProjectID || a.Agent != in.Actor || a.TaskID != in.ScopeTaskID || callFingerprint(a.Call) != callFingerprint(*in.GrantedCall) {
		return ErrConflict
	}
	var recorded struct {
		decisions.Record
		DecisionID string `json:"decision_id"`
	}
	b, _ := json.Marshal(a.Call.Arguments)
	if err = json.Unmarshal(b, &recorded); err != nil {
		return err
	}
	expected, _ := json.Marshal(recorded.Record)
	actual, _ := json.Marshal(in)
	if recorded.DecisionID != d.ID || string(expected) != string(actual) {
		return ErrConflict
	}
	return nil
}

// DecideDecision rechecks captured and current delegation inside the atomic registry mutation.
// current is trusted Core configuration, never tool arguments.
func (s *Store) DecideDecision(ctx context.Context, project string, in decisions.Decide, current config.Agent) (decisions.Decision, error) {
	input := struct {
		Decision     decisions.Decide
		Actor, RunID string
		Current      config.Agent
	}{in, in.Actor, in.RunID, current}
	return mutate(ctx, s, "decision.decide:"+project+":"+in.Actor+":"+in.RunID, in.RequestID, input, func(tx *sql.Tx) (decisions.Decision, error) {
		d := decisions.Decision{}
		spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=? AND status='RUNNING' AND lease_until>=?", in.RunID, time.Now().Unix())
		if err != nil {
			return d, err
		}
		var snapshot config.Agent
		if err = json.Unmarshal(spec.ConfigSnapshot, &snapshot); err != nil {
			return d, err
		}
		if spec.Project != project || spec.Agent != in.Actor || spec.TaskID != in.TaskID || current.Metadata.Name != in.Actor || snapshot.Metadata.Name != in.Actor {
			return d, inputError("decision run identity mismatch")
		}
		rules := snapshot.DecisionAuthority(in.Category)
		liveRules := current.DecisionAuthority(in.Category)
		if len(rules) == 0 || len(liveRules) == 0 {
			return d, inputError("explicit decision category delegation required in captured and current policy")
		}
		b, _ := json.Marshal(in)
		topic := canonicalTopic(in.Topic)
		if len(b) > 32*1024 || in.RequestID == "" || topic == "" || len(topic) > 256 || strings.TrimSpace(in.Value) == "" || len(in.Value) > 16*1024 || strings.TrimSpace(in.Rationale) == "" || len(in.Rationale) > 8192 {
			return d, inputError("bounded topic, value, rationale and request_id required")
		}
		if err = journalLinks(ctx, tx, project, in.TaskID, in.RunID, in.Artifacts); err != nil {
			return d, err
		}
		// Owner review is authoritative for a topic, regardless of category/value relabelling.
		history, err := listDocs[decisions.Decision](ctx, tx, "SELECT body FROM decisions WHERE project_id=? AND topic=?", project, topic)
		if err != nil {
			return d, err
		}
		var active *decisions.Decision
		for i := range history {
			old := &history[i]
			if old.Status == "proposed" || old.Status == "under_discussion" || old.Status == "rejected" {
				return d, ErrConflict
			}
			if old.Status == "approved" {
				active = old
			}
		}
		if active != nil {
			if active.Authority != "policy" || active.Category != in.Category || active.ID != in.SupersedesID || active.Version != in.SupersedesVersion {
				return d, ErrConflict
			}
		} else if in.SupersedesID != "" || in.SupersedesVersion != 0 {
			return d, ErrConflict
		}
		capturedHash := sha256.Sum256(spec.ConfigSnapshot)
		live, _ := json.Marshal(current)
		liveHash := sha256.Sum256(live)
		now := time.Now().Unix()
		d = decisions.Decision{ID: protocol.ID(), ProjectID: project, Topic: topic, Category: in.Category, Status: "approved", Value: in.Value, Rationale: in.Rationale, CreatedBy: in.Actor, ApprovedBy: in.Actor, RecordedBy: in.Actor, Authority: "policy", AuthorityRules: rules, CurrentAuthorityRules: liveRules, ConfigHash: hex.EncodeToString(capturedHash[:]), CurrentConfigHash: hex.EncodeToString(liveHash[:]), TaskID: in.TaskID, RunID: in.RunID, Artifacts: in.Artifacts, Version: 1, CreatedAt: now, UpdatedAt: now}
		if active != nil {
			active.Status = "superseded"
			active.SupersededBy = d.ID
			d.SupersedesID = active.ID
			if err = saveDecision(ctx, tx, active, "decision.superseded"); err != nil {
				return d, err
			}
		}
		body, _ := json.Marshal(d)
		if _, err = tx.ExecContext(ctx, "INSERT INTO decisions VALUES(?,?,?,?,?,?)", d.ID, project, topic, d.Status, d.Version, body); err != nil {
			return d, err
		}
		return d, entityEvent(ctx, tx, "decision.decided", project, d.ID, d)
	})
}
