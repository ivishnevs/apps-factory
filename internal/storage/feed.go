package storage

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"appfactory/internal/decisions"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

type FeedItem struct {
	Sequence  int64     `json:"sequence"`
	EventID   string    `json:"event_id"`
	Type      string    `json:"type"`
	EntityID  string    `json:"entity_id"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
	Status    string    `json:"status,omitempty"`
	Summary   string    `json:"summary,omitempty"`
	TaskID    string    `json:"task_id,omitempty"`
	RunID     string    `json:"run_id,omitempty"`
	Agent     string    `json:"agent,omitempty"`
	Actor     string    `json:"actor,omitempty"`
	Attention bool      `json:"attention,omitempty"`
	Ref       string    `json:"ref,omitempty"`
}

// feedRow derives the feed columns of an entity event from the entity snapshot. The backfill in
// migration 0009 applies the same rules to older events.
func feedRow(e protocol.Envelope, project, id string, state map[string]json.RawMessage) FeedItem {
	str := func(keys ...string) string {
		for _, k := range keys {
			var v string
			if json.Unmarshal(state[k], &v) == nil && v != "" {
				return v
			}
		}
		return ""
	}
	item := FeedItem{EventID: e.ID, Type: e.Type, EntityID: id, ProjectID: project, CreatedAt: e.At,
		TaskID: str("task_id"), RunID: str("run_id", "current_run_id"), Agent: str("assignee", "agent"),
		Actor: str("sender", "recorded_by", "created_by"), Status: str("status")}
	switch {
	case strings.HasPrefix(e.Type, "task."):
		item.TaskID = id
	case strings.HasPrefix(e.Type, "run."):
		item.RunID = id
	}
	item.Summary, _ = excerpt(str("summary", "title", "topic"), 4096)
	// Attention marks what waits on the owner; fuller rules arrive with waits-events-cost-cli.
	var needsOwner bool
	_ = json.Unmarshal(state["needs_owner_action"], &needsOwner)
	switch {
	case e.Type == "approval.requested", e.Type == "report.created" && needsOwner:
		item.Attention = true
	case e.Type == "message.created" && str("recipient") == "owner" && (str("message_type") == "question" || str("status") == "ESCALATED"):
		item.Attention = true
	case e.Type == "message.escalated":
		item.Attention = true
	}
	return item
}

// feed records a feed row and its broker notification in the caller's transaction.
func feed(ctx context.Context, tx *Tx, item FeedItem) error {
	res, err := tx.ExecContext(ctx, "INSERT INTO feed_events(company_id,id,at,kind,project_id,entity_id,task_id,run_id,agent,actor,status,summary,attention,ref) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
		tx.company, item.EventID, item.CreatedAt.UTC().Format(time.RFC3339Nano), item.Type, item.ProjectID, item.EntityID, item.TaskID, item.RunID, item.Agent, item.Actor, item.Status, item.Summary, item.Attention, item.Ref)
	if err != nil {
		return err
	}
	if item.Sequence, err = res.LastInsertId(); err != nil {
		return err
	}
	item.Summary = shortText(item.Summary, 512)
	e := protocol.Wrap("feed."+item.Type, item.EntityID, "", item)
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// Company-prefixed subjects (factory.<company>.feed...) arrive with the NATS subject change. A company-wide
	// event (no project, e.g. a pause) uses the token _company: an empty token is not a valid subject, and the
	// outbox publisher would stop at it.
	_, err = tx.ExecContext(ctx, "INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", "feed-"+item.EventID, feedSubject(item), b, tx.company)
	return err
}

// FeedFilter narrows a feed read; empty fields match everything.
type FeedFilter struct {
	Project, Task string
	// Attention keeps only events that need the owner.
	Attention bool
}

// AddFeed records one feed event Core decides outside a storage transaction (e.g. an ignored model override).
func (s *Store) AddFeed(ctx context.Context, company string, item FeedItem) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = tx.now()
	}
	if err = feed(ctx, tx, item); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ProjectFeed(ctx context.Context, company, project string, after int64, limit int) (Page[FeedItem], error) {
	return s.Feed(ctx, company, FeedFilter{Project: project}, after, limit)
}

// Feed reads the company's event feed in sequence order.
func (s *Store) Feed(ctx context.Context, company string, f FeedFilter, after int64, limit int) (Page[FeedItem], error) {
	out := Page[FeedItem]{Items: []FeedItem{}, NextAfter: after}
	if after < 0 || limit < 1 || limit > 100 {
		return out, inputError("after >= 0 and limit 1..100 required")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT seq,id,at,kind,project_id,entity_id,task_id,run_id,agent,actor,status,summary,attention,ref FROM feed_events
 WHERE company_id=? AND (?='' OR project_id=?) AND (?='' OR task_id=?) AND (?=0 OR attention=1) AND seq>? ORDER BY seq LIMIT ?`,
		company, f.Project, f.Project, f.Task, f.Task, f.Attention, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item FeedItem
		var at string
		if err = rows.Scan(&item.Sequence, &item.EventID, &at, &item.Type, &item.ProjectID, &item.EntityID, &item.TaskID, &item.RunID, &item.Agent, &item.Actor, &item.Status, &item.Summary, &item.Attention, &item.Ref); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		if item.CreatedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
			return out, err
		}
		item.Summary = shortText(item.Summary, 512)
		out.Items = append(out.Items, item)
		out.NextAfter = item.Sequence
	}
	return out, rows.Err()
}

// ActiveRuns lists runs that are queued, running, cancelling or awaiting reconciliation (UNKNOWN).
func (s *Store) ActiveRuns(ctx context.Context, company string) ([]Run, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT spec,status,session_id,result,started_at,finished_at,usage,outcome FROM runs WHERE company_id=? AND status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN') ORDER BY created_at,id", company)
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

type ProjectContext struct {
	ProjectTruncated bool                     `json:"project_truncated,omitempty"`
	Project          tasks.Project            `json:"project"`
	Decisions        []decisions.ContextEntry `json:"approved_decisions"`
	MoreDecisions    bool                     `json:"more_decisions"`
}

func (s *Store) ProjectContext(ctx context.Context, company, project string) (ProjectContext, error) {
	var out ProjectContext
	tx, err := s.begin(ctx, company)
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	out.Project, err = projectTx(ctx, tx, project)
	if err != nil {
		return out, err
	}
	out.Decisions, out.MoreDecisions, err = decisionContext(ctx, tx, project)
	return out, err
}

// CompanyFeedToken is the project token of company-wide feed events in NATS subjects.
const CompanyFeedToken = "_company"

func feedSubject(item FeedItem) string {
	project := item.ProjectID
	if project == "" {
		project = CompanyFeedToken
	}
	return "factory.events.feed." + project + "." + item.Type
}
