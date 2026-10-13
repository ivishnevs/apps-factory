package storage

import (
	"context"
	"encoding/json"
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
}

func (s *Store) ProjectFeed(ctx context.Context, company, project string, after int64, limit int) (Page[FeedItem], error) {
	out := Page[FeedItem]{Items: []FeedItem{}, NextAfter: after}
	if after < 0 || limit < 1 || limit > 100 {
		return out, inputError("after >= 0 and limit 1..100 required")
	}
	rows, err := s.DB.QueryContext(ctx, "SELECT seq,entity_id,body FROM entity_events WHERE company_id=? AND project_id=? AND seq>? ORDER BY seq LIMIT ?", company, project, after, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var item FeedItem
		var b []byte
		var e protocol.Envelope
		if err = rows.Scan(&item.Sequence, &item.EntityID, &b); err != nil {
			return out, err
		}
		if len(out.Items) == limit {
			out.HasMore = true
			break
		}
		if err = json.Unmarshal(b, &e); err != nil {
			return out, err
		}
		item.EventID = e.ID
		item.Type = e.Type
		item.ProjectID = project
		item.CreatedAt = e.At
		var value struct{ Status, Summary, Title, Topic string }
		if err = json.Unmarshal(e.Payload, &value); err != nil {
			return out, err
		}
		item.Status = value.Status
		item.Summary = value.Summary
		if item.Summary == "" {
			item.Summary = value.Title
		}
		if item.Summary == "" {
			item.Summary = value.Topic
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
