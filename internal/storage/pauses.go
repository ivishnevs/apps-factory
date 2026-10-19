package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
)

// Pause is an owner hold on new dispatch for the whole company, a project or an agent.
type Pause struct {
	Scope     string `json:"scope"`
	Target    string `json:"target,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Actor     string `json:"actor"`
	CreatedAt int64  `json:"created_at"`
	// Until ends the pause automatically (quota pauses); 0 means until resumed.
	Until int64 `json:"until,omitempty"`
}

func validPause(scope, target string) error {
	switch {
	case scope == "company" && target == "":
	case (scope == "project" || scope == "agent" || scope == "quota_key") && entityName.MatchString(target):
	default:
		return inputError("pause scope must be company, project ID, agent name or quota key")
	}
	return nil
}

func pauseFeed(ctx context.Context, tx *Tx, kind string, p Pause) error {
	project := ""
	if p.Scope == "project" {
		project = p.Target
	}
	summary := strings.TrimSpace(p.Scope + " " + p.Target)
	if p.Reason != "" {
		summary += ": " + p.Reason
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: kind, EntityID: p.Scope + ":" + p.Target, ProjectID: project, CreatedAt: tx.now(), Actor: p.Actor, Summary: summary, Attention: kind == "owner.paused"})
}

// SetPause pauses a scope (idempotent); new dispatch there stops, live runs continue.
func (s *Store) SetPause(ctx context.Context, company string, p Pause) error {
	if err := validPause(p.Scope, p.Target); err != nil {
		return err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if p.Actor == "" {
		p.Actor = "owner"
	}
	res, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO pauses(company_id,scope,target,reason,actor,created_at,until) VALUES(?,?,?,?,?,?,?)", company, p.Scope, p.Target, p.Reason, p.Actor, tx.now().Unix(), p.Until)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if err = pauseFeed(ctx, tx, "owner.paused", p); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClearPause resumes a scope; resuming a scope that is not paused is a no-op.
func (s *Store) ClearPause(ctx context.Context, company, scope, target, actor string) error {
	if err := validPause(scope, target); err != nil {
		return err
	}
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "DELETE FROM pauses WHERE company_id=? AND scope=? AND target=?", company, scope, target)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		if actor == "" {
			actor = "owner"
		}
		if err = pauseFeed(ctx, tx, "owner.resumed", Pause{Scope: scope, Target: target, Actor: actor}); err != nil {
			return err
		}
	}
	if scope == "quota_key" {
		if err = clearQuotaEscalation(ctx, tx, target, "resumed by "+actor); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Pauses(ctx context.Context, company string) ([]Pause, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT scope,target,reason,actor,created_at,until FROM pauses WHERE company_id=? AND (until=0 OR until>?) ORDER BY scope,target", company, s.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Pause{}
	for rows.Next() {
		var p Pause
		if err = rows.Scan(&p.Scope, &p.Target, &p.Reason, &p.Actor, &p.CreatedAt, &p.Until); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// pausedFor returns the paused scopes covering a project, an agent and its quota key, most general
// first: "company", "project:<id>", "agent:<name>", "quota_key:<key>". An empty quotaKey skips quota pauses.
func pausedFor(ctx context.Context, tx *Tx, project, agent, quotaKey string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT scope,target FROM pauses WHERE company_id=? AND (until=0 OR until>?)
 AND (scope='company' OR scope='project' AND target=? OR scope='agent' AND target=? OR scope='quota_key' AND target=?)
 ORDER BY CASE scope WHEN 'company' THEN 0 WHEN 'project' THEN 1 WHEN 'agent' THEN 2 ELSE 3 END`, tx.company, tx.now().Unix(), project, agent, quotaKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var scope, target string
		if err = rows.Scan(&scope, &target); err != nil {
			return nil, err
		}
		if target != "" {
			scope += ":" + target
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}

// RevokeLive is the kill switch's second half: every QUEUED run is revoked at once and every
// RUNNING run is asked to stop; their tasks return to READY instead of being cancelled.
func (s *Store) RevokeLive(ctx context.Context, company, actor string) (int, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT id,status FROM runs WHERE company_id=? AND status IN ('QUEUED','RUNNING','CANCELLING') AND revoked=0", company)
	if err != nil {
		return 0, err
	}
	type live struct{ id, status string }
	var runs []live
	for rows.Next() {
		var r live
		if err = rows.Scan(&r.id, &r.status); err != nil {
			rows.Close()
			return 0, err
		}
		runs = append(runs, r)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	for _, r := range runs {
		next := "CANCELLING"
		if r.status == "QUEUED" {
			next = "CANCELLED"
		}
		if _, err = tx.ExecContext(ctx, "UPDATE runs SET status=?,revoked=1,result=CASE WHEN ?='CANCELLED' THEN 'revoked by '||? ELSE result END,finished_at=CASE WHEN ?='CANCELLED' THEN ? ELSE finished_at END WHERE id=?", next, next, actor, next, tx.now().Unix(), r.id); err != nil {
			return 0, err
		}
		if next == "CANCELLED" {
			if err = syncTaskRun(ctx, tx, r.id, false); err != nil {
				return 0, err
			}
		}
	}
	return len(runs), tx.Commit()
}

func runRevoked(ctx context.Context, tx *Tx, id string) (bool, error) {
	var revoked bool
	err := tx.QueryRowContext(ctx, "SELECT revoked FROM runs WHERE id=?", id).Scan(&revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return revoked, err
}

// PendingCounts is what waits on the owner.
type PendingCounts struct {
	Inbox     int `json:"inbox"`
	Approvals int `json:"approvals"`
	Alerts    int `json:"alerts"`
	Inspect   int `json:"inspect"`
	Reviews   int `json:"in_review"`
	// Total counts inbox items, active alerts and runs to inspect; approvals are already in the inbox.
	Total int `json:"total"`
}

func (s *Store) PendingCounts(ctx context.Context, company string) (PendingCounts, error) {
	var p PendingCounts
	inbox, err := s.Inbox(ctx, company, "")
	if err != nil {
		return p, err
	}
	p.Inbox = len(inbox)
	for q, dst := range map[string]*int{
		"SELECT count(*) FROM approvals WHERE company_id=? AND status='PENDING'":                    &p.Approvals,
		"SELECT count(*) FROM task_alerts WHERE company_id=? AND status='active'":                   &p.Alerts,
		"SELECT count(*) FROM task_parks WHERE company_id=? AND status='active' AND kind='inspect'": &p.Inspect,
		"SELECT count(*) FROM tasks WHERE company_id=? AND status='IN_REVIEW'":                      &p.Reviews,
	} {
		if err = s.DB.QueryRowContext(ctx, q, company).Scan(dst); err != nil {
			return p, err
		}
	}
	p.Total = p.Inbox + p.Alerts + p.Inspect
	return p, nil
}

// DefaultQuotaPause is how long a quota pause lasts when the runtime gives no reset time.
const DefaultQuotaPause = time.Hour

// QuotaKey is the agent's quota key from its JSON config (a run's config snapshot or a marshalled
// config.Agent), runtime-drivers D5: spec.runtime.quota_key; else the provider's key the catalog resolved
// (default_quota_key: the provider's only declared key, or <provider>-default); else, for a snapshot taken
// before that field existed, <provider>-default; the agent name only when the config names no provider.
func QuotaKey(agentJSON []byte, agent string) string {
	var a struct {
		Spec struct {
			Runtime struct {
				Provider        string `json:"provider"`
				QuotaKey        string `json:"quota_key"`
				DefaultQuotaKey string `json:"default_quota_key"`
			} `json:"runtime"`
		} `json:"spec"`
	}
	if json.Unmarshal(agentJSON, &a) != nil {
		return agent
	}
	switch r := a.Spec.Runtime; {
	case r.QuotaKey != "":
		return r.QuotaKey
	case r.DefaultQuotaKey != "":
		return r.DefaultQuotaKey
	case r.Provider != "":
		return r.Provider + "-default"
	}
	return agent
}

// quotaPause pauses the run's quota key until the limit resets; a later reset extends it, an earlier
// one never shortens it.
func quotaPause(ctx context.Context, tx *Tx, runID string, q protocol.Quota) error {
	spec, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE company_id=? AND id=?", tx.company, runID)
	if err != nil {
		return err
	}
	key := QuotaKey(spec.ConfigSnapshot, spec.Agent)
	if !entityName.MatchString(key) {
		return nil
	}
	now := tx.now()
	until := q.ResetsAt
	if until.IsZero() || !until.After(now) {
		until = now.Add(DefaultQuotaPause)
	}
	reason := "subscription limit"
	if q.Type != "" {
		reason += " " + q.Type
	}
	// A runtime may report one limit hit more than once: only a new pause or a later reset is news.
	var previous int64
	switch err = tx.QueryRowContext(ctx, "SELECT until FROM pauses WHERE company_id=? AND scope='quota_key' AND target=?", tx.company, key).Scan(&previous); {
	case errors.Is(err, sql.ErrNoRows):
		previous = 0
	case err != nil:
		return err
	case until.Unix() <= previous:
		return nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO pauses(company_id,scope,target,reason,actor,created_at,until) VALUES(?,'quota_key',?,?,'runtime',?,?)
 ON CONFLICT(company_id,scope,target) DO UPDATE SET until=max(until,excluded.until),reason=excluded.reason`, tx.company, key, reason, now.Unix(), until.Unix()); err != nil {
		return err
	}
	if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "quota.paused", EntityID: "quota_key:" + key, ProjectID: spec.Project, CreatedAt: now, TaskID: spec.TaskID, RunID: runID, Agent: spec.Agent, Actor: "runtime", Summary: reason + "; claims for quota key " + key + " paused until " + until.UTC().Format(time.RFC3339), Attention: true, Ref: key}); err != nil {
		return err
	}
	return openQuotaEscalation(ctx, tx, key, spec.Project, reason, until)
}

// QuotaPausedReason is the escalation of a paused quota key (runtime-drivers D5): its scope is the key, not a
// task (no task triggers); it goes to the owner, who decides per subscription, and clears itself when the
// pause ends (reset time reached, or the owner resumed the key).
const QuotaPausedReason = "quota_paused"

func init() {
	// wait: keep the pause until the limit resets; resume: lift it now (another account, a raised limit).
	escalationAnswers[QuotaPausedReason] = []string{"wait", "resume"}
}

// openQuotaEscalation opens the key's quota_paused escalation unless one is open: a question to the owner
// with the answers wait and resume, due at the reset.
func openQuotaEscalation(ctx context.Context, tx *Tx, key, project, reason string, until time.Time) error {
	open, err := listEscalations(ctx, tx, "WHERE company_id=? AND task_id IS NULL AND reason=? AND ref_id=? AND status='open'", tx.company, QuotaPausedReason, key)
	if err != nil || len(open) > 0 {
		return err
	}
	// The question lives in the run's project conversation; an ad-hoc run of an unregistered project
	// (POST /runs) pauses the key without one.
	if _, err = projectTx(ctx, tx, project); errors.Is(err, ErrNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	answers := escalationAnswers[QuotaPausedReason]
	id := protocol.ID()
	summary := reason + ": every agent on quota key " + key + " is paused until " + until.UTC().Format(time.RFC3339) + "; agents on other keys keep running"
	timeout := max(1, min(int(until.Sub(tx.now()).Seconds()), 7*24*3600))
	m, err := sendMessageTx(ctx, tx, messaging.Send{ProjectID: project, Sender: "factory", Recipient: "owner", MessageType: "question",
		Body:           "Escalation " + QuotaPausedReason + " on quota key " + key + ": " + summary + ".\nAnswer wait (claims resume at the reset by themselves) or resume (lift the pause now, e.g. after switching the account).",
		Questions:      []messaging.Question{{ID: EscalationAnswerID, Type: "single_choice", Question: "Quota key " + key + " hit its limit. How should it continue?", Options: answers}},
		TimeoutSeconds: timeout, RequestID: "escalation-" + id})
	if err != nil {
		return err
	}
	b, _ := json.Marshal(answers)
	_, err = tx.ExecContext(ctx, "INSERT INTO escalations(company_id,id,task_id,reason,ref_id,summary,answers,target,message_id,status,created_at) VALUES(?,?,NULL,?,?,?,?,'owner',?,'open',?)",
		tx.company, id, QuotaPausedReason, key, summary, string(b), m.ID, tx.now().Unix())
	return err
}

// clearQuotaEscalation clears the key's open quota_paused escalation (the pause ended).
func clearQuotaEscalation(ctx context.Context, tx *Tx, key, why string) error {
	open, err := listEscalations(ctx, tx, "WHERE company_id=? AND task_id IS NULL AND reason=? AND ref_id=? AND status='open'", tx.company, QuotaPausedReason, key)
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

// closeScopeEscalation ends an escalation without a task (quota_paused): a cleared one withdraws its
// unanswered question.
func closeScopeEscalation(ctx context.Context, tx *Tx, e Escalation, status, answer, actor, why string) error {
	m, err := messageTx(ctx, tx, e.MessageID)
	if err != nil {
		return err
	}
	if status == "cleared" && (m.Status == "PENDING" || m.Status == "ESCALATED") {
		m.Status, m.DeliveryStatus, m.EscalationReason = "WITHDRAWN", "NONE", why
		if err = saveMessage(ctx, tx, &m, "message.withdrawn"); err != nil {
			return err
		}
	}
	return feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "quota.escalation_" + status + "." + e.Reason, EntityID: "quota_key:" + e.RefID, ProjectID: m.ProjectID, CreatedAt: tx.now(), Summary: strings.TrimSpace(answer + " " + why), Actor: actor, Ref: e.MessageID})
}

// applyScopeAnswer applies the answer to a quota_paused escalation: resume lifts the key's pause now.
func applyScopeAnswer(ctx context.Context, tx *Tx, e Escalation, answer, actor string) error {
	if e.Reason != QuotaPausedReason || answer != "resume" {
		return nil
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM pauses WHERE company_id=? AND scope='quota_key' AND target=?", tx.company, e.RefID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	return pauseFeed(ctx, tx, "owner.resumed", Pause{Scope: "quota_key", Target: e.RefID, Actor: actor, Reason: "quota_paused answered resume"})
}

// ExpirePauses removes pauses whose until has passed and records their end in the feed.
func (s *Store) ExpirePauses(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := tx.now().Unix()
	rows, err := tx.QueryContext(ctx, "SELECT scope,target,reason FROM pauses WHERE company_id=? AND until>0 AND until<=?", company, now)
	if err != nil {
		return err
	}
	var expired []Pause
	for rows.Next() {
		var p Pause
		if err = rows.Scan(&p.Scope, &p.Target, &p.Reason); err != nil {
			rows.Close()
			return err
		}
		expired = append(expired, p)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(expired) == 0 {
		return nil
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM pauses WHERE company_id=? AND until>0 AND until<=?", company, now); err != nil {
		return err
	}
	for _, p := range expired {
		p.Actor = "system"
		if err = pauseFeed(ctx, tx, "owner.resumed", p); err != nil {
			return err
		}
		if p.Scope == "quota_key" {
			if err = clearQuotaEscalation(ctx, tx, p.Target, "the limit reset"); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// activeQuotaKeys lists the quota keys paused now.
func activeQuotaKeys(ctx context.Context, tx *Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT target FROM pauses WHERE company_id=? AND scope='quota_key' AND (until=0 OR until>?)", tx.company, tx.now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
