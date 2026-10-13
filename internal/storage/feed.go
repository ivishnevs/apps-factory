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
	// Company-prefixed subjects (factory.<company>.feed...) arrive with the NATS subject change.
	_, err = tx.ExecContext(ctx, "INSERT INTO outbox(id,subject,body,company_id) VALUES(?,?,?,?)", "feed-"+item.EventID, "factory.events.feed."+item.ProjectID+"."+item.Type, b, tx.company)
	return err
}

func (s *Store) ProjectFeed(ctx context.Context, company, project string, after int64, limit int) (Page[FeedItem], error) {
	out := Page[FeedItem]{Items: []FeedItem{}, NextAfter: after}
	if after < 0 || limit < 1 || limit > 100 {
		return out, inputError("after >= 0 and limit 1..100 required")
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT seq,id,at,kind,project_id,entity_id,task_id,run_id,agent,actor,status,summary,attention,ref FROM feed_events WHERE company_id=? AND project_id=? AND seq>? ORDER BY seq LIMIT ?", company, project, after, limit+1)
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
