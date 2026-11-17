package storage

import (
	"context"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// Person items (owner-items-one-source): everything a person is asked to act on is an item of their people topics,
// reviews where a person is the reviewer included. Core computes them here once; every surface (the Inbox, the task
// page, Home, the board, counts, Telegram, the TUI) renders them and derives nothing of its own.

// PersonItem is one open item of a task that waits for a person.
type PersonItem struct {
	Topic   string   `json:"topic"`
	Kind    string   `json:"kind"`
	Ref     string   `json:"ref"`
	Person  string   `json:"person"`            // "owner" or "member:USER"
	Title   string   `json:"title"`             // the item in people words (its note, else its topic's title)
	Actions []string `json:"actions,omitempty"` // what the person can do, as on the Inbox card
}

// personReview reports whether the task's review waits for a person (no agent reviewer) and which one: the task's
// member reviewer, else the owner.
func personReview(t tasks.Task) (string, bool) {
	if t.Status != "IN_REVIEW" || t.Reviewer != "" {
		return "", false
	}
	if strings.HasPrefix(t.ReviewBy, tasks.PersonPrefix) {
		return t.ReviewBy, true
	}
	return "owner", true
}

// reviewWaitSQL is when a review item waits (alias i for people_topic_items): its task is in a person's review and no
// park holds the task first (a parked task waits for the park's own item).
const reviewWaitSQL = `EXISTS(SELECT 1 FROM tasks t WHERE t.company_id=i.company_id AND t.id=i.ref_id AND t.status='IN_REVIEW' AND coalesce(t.body->>'reviewer','')=''
	AND NOT EXISTS(SELECT 1 FROM task_parks p WHERE p.company_id=t.company_id AND p.task_id=t.id AND p.status='active'))`

// waitingItemSQL is when a topic item waits for people (alias i); PeopleTopic resolves the same per item.
const waitingItemSQL = `i.state='open' AND (
	(i.kind='approval' AND EXISTS(SELECT 1 FROM approvals a WHERE a.company_id=i.company_id AND a.id=i.ref_id AND a.status='PENDING'))
	OR (i.kind IN ('question','escalation') AND EXISTS(SELECT 1 FROM messages m WHERE m.company_id=i.company_id AND m.id=i.ref_id AND (m.status IN ('PENDING','ESCALATED') OR m.status='UNREAD' AND m.message_type='question')))
	OR (i.kind='decision' AND EXISTS(SELECT 1 FROM decisions d WHERE d.company_id=i.company_id AND d.id=i.ref_id AND d.status IN ('proposed','under_discussion')))
	OR (i.kind='review' AND ` + reviewWaitSQL + `))`

// syncReviewItemTx keeps the task's review item equal to its state (owner-items-one-source 1.1): a task in a person's
// review has an open review item in its topics; one that left it (accepted, sent back, given to an agent reviewer,
// cancelled) has its item settled. Called on every task save and by the sweep.
func syncReviewItemTx(ctx context.Context, tx *Tx, t tasks.Task) error {
	var open bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM people_topic_items WHERE company_id=? AND kind='review' AND ref_id=? AND state='open')", tx.company, t.ID).Scan(&open); err != nil {
		return err
	}
	_, wants := personReview(t)
	switch {
	case wants && !open:
		bringer := t.Implementer
		if bringer == "" {
			bringer = t.Assignee
		}
		_, err := toPeople(ctx, tx, gateItem{ref: messaging.PeopleRef{Path: messaging.PathReview, Kind: "review", Ref: t.ID}, project: t.ProjectID, task: t.ID, bringer: bringer,
			brief: reviewBrief(peopleLanguageTx(ctx, tx), t), note: &messaging.ItemNote{What: t.Title}})
		return err
	case !wants && open:
		_, err := tx.ExecContext(ctx, "UPDATE people_topic_items SET state='settled' WHERE company_id=? AND kind='review' AND ref_id=? AND state='open'", tx.company, t.ID)
		return err
	}
	return nil
}

// SyncReviewItems is the sweep of review items (owner-items-one-source): tasks already in a person's review get their
// item, items of tasks that left it settle.
func (s *Store) SyncReviewItems(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ids, err := topicIDs(ctx, tx, `SELECT t.id FROM tasks t WHERE t.company_id=? AND t.status='IN_REVIEW' AND coalesce(t.body->>'reviewer','')=''
		AND NOT EXISTS(SELECT 1 FROM people_topic_items i WHERE i.company_id=t.company_id AND i.kind='review' AND i.ref_id=t.id AND i.state='open')
		UNION SELECT i.ref_id FROM people_topic_items i WHERE i.company_id=? AND i.kind='review' AND i.state='open'
		AND NOT EXISTS(SELECT 1 FROM tasks t WHERE t.company_id=i.company_id AND t.id=i.ref_id AND t.status='IN_REVIEW' AND coalesce(t.body->>'reviewer','')='')`, company, company)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		t, err := taskTx(ctx, tx, id)
		if err != nil {
			if _, err = tx.ExecContext(ctx, "UPDATE people_topic_items SET state='settled' WHERE company_id=? AND kind='review' AND ref_id=? AND state='open'", company, id); err != nil {
				return err
			}
			continue
		}
		if err = syncReviewItemTx(ctx, tx, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// PersonItems lists the open items that wait for a person, by task (owner-items-one-source 1.2): "this task needs
// you" means exactly that the task has one waiting for you. The items are resolved as the topics show them.
func (s *Store) PersonItems(ctx context.Context, company string) (map[string][]PersonItem, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT DISTINCT i.topic_id FROM people_topic_items i WHERE i.company_id=? AND "+waitingItemSQL, company)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := map[string][]PersonItem{}
	for _, id := range ids {
		v, err := s.PeopleTopic(ctx, company, id)
		if err != nil {
			return nil, err
		}
		for _, it := range v.Items {
			if !it.Waits || it.Task == nil {
				continue
			}
			out[it.Task.ID] = append(out[it.Task.ID], PersonItem{Topic: v.ID, Kind: it.Kind, Ref: it.Ref, Person: it.Person, Title: itemTitle(v, it), Actions: it.Actions})
		}
	}
	return out, nil
}

// TaskPersonItems is PersonItems of one task (never nil).
func (s *Store) TaskPersonItems(ctx context.Context, company, task string) ([]PersonItem, error) {
	all, err := s.PersonItems(ctx, company)
	if err != nil {
		return nil, err
	}
	if l := all[task]; l != nil {
		return l, nil
	}
	return []PersonItem{}, nil
}

// itemTitle is an item in people words: its note, else its topic's title, else its message's headline.
func itemTitle(v TopicView, it ItemView) string {
	if w := strings.TrimSpace(it.Note.What); w != "" {
		return w
	}
	if t := strings.TrimSpace(v.Brief.Title); t != "" {
		return t
	}
	if it.Message != nil {
		return it.Message.Headline()
	}
	if it.Task != nil {
		return it.Task.Title
	}
	return ""
}

// waitingTasks is the set of tasks with an item waiting for a person.
func (s *Store) waitingTasks(ctx context.Context, company string) (map[string]bool, error) {
	items, err := s.PersonItems(ctx, company)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for id := range items {
		out[id] = true
	}
	return out, nil
}

// InPeopleTopic reports whether ref (a message, approval, decision or task id) is an item of a people topic, or the
// message of one: such a cause reaches people as its item, so Telegram sends no raw attention line for it
// (owner-items-one-source 2.5).
func (s *Store) InPeopleTopic(ctx context.Context, company, ref string) (bool, error) {
	if ref == "" {
		return false, nil
	}
	var in bool
	err := s.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM people_topic_items WHERE company_id=? AND ref_id=?)
		OR EXISTS(SELECT 1 FROM messages WHERE company_id=? AND id=? AND topic_id<>'')
		OR EXISTS(SELECT 1 FROM people_topic_items i JOIN approvals a ON a.company_id=i.company_id AND a.id=i.ref_id WHERE i.company_id=? AND i.kind='approval' AND a.message_id=?)`,
		company, ref, company, ref, company, ref).Scan(&in)
	return in, err
}
