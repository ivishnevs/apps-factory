package storage

import (
	"context"
	"encoding/json"

	"appfactory/internal/messaging"
)

// TopicItem is one item of a people topic: a reference to an approval, question, escalation, decision, review or
// update, with its bringer's note (people-topics D4, D5).
type TopicItem struct {
	Kind      string             `json:"kind"`
	Ref       string             `json:"ref"`
	Lineage   string             `json:"lineage,omitempty"`
	Version   string             `json:"version,omitempty"`
	Note      messaging.ItemNote `json:"note"`
	BroughtBy string             `json:"brought_by,omitempty"`
	State     string             `json:"state"` // open | replaced | update (whether it still waits comes from its object)
	AddedAt   int64              `json:"added_at"`
}

// Topic reads a people topic.
func (s *Store) Topic(ctx context.Context, company, id string) (Topic, error) {
	return readTopic(ctx, s.DB, company, id)
}

// TopicItems lists a topic's items in the order they came.
func (s *Store) TopicItems(ctx context.Context, company, id string) ([]TopicItem, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT kind,ref_id,lineage,version,note,brought_by,state,added_at FROM people_topic_items WHERE company_id=? AND topic_id=? ORDER BY seq", company, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TopicItem{}
	for rows.Next() {
		var it TopicItem
		var note string
		if err = rows.Scan(&it.Kind, &it.Ref, &it.Lineage, &it.Version, &note, &it.BroughtBy, &it.State, &it.AddedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(note), &it.Note)
		out = append(out, it)
	}
	return out, rows.Err()
}
