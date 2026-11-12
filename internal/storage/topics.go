package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/decisions"
	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
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

// TopicView is a topic as people see it (people-topics D5, D6): its brief, its items with what each is and what can be
// done with it, how many wait for people, and the tasks that wait on them.
type TopicView struct {
	Topic
	Open    bool       `json:"open"`
	Waiting int        `json:"waiting"` // items that wait for people
	Items   []ItemView `json:"items"`
	Blocked []TaskRef  `json:"blocked,omitempty"` // tasks parked on its items
	Tasks   []TaskRef  `json:"tasks,omitempty"`   // tasks its items belong to
	At      int64      `json:"at"`                // the newest item
}

// TaskRef names a task.
type TaskRef struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee,omitempty"`
}

// ItemView is one item for people.
type ItemView struct {
	TopicItem
	Waits    bool                `json:"waits"`             // it waits for people
	Status   string              `json:"status"`            // its object's status
	Actions  []string            `json:"actions,omitempty"` // what people can do (people-topics D5)
	Approval *approvals.Approval `json:"approval,omitempty"`
	Message  *messaging.Message  `json:"message,omitempty"`
	Decision *decisions.Decision `json:"decision,omitempty"`
	Task     *TaskRef            `json:"task,omitempty"`
}

// PeopleTopics lists a company's topics (one project, or all with ""), newest first; open only when asked.
func (s *Store) PeopleTopics(ctx context.Context, company, project string, openOnly bool) ([]TopicView, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT id FROM people_topics WHERE company_id=? AND (?='' OR project_id=?) ORDER BY updated_at DESC LIMIT 500", company, project, project)
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
	out := []TopicView{}
	for _, id := range ids {
		v, err := s.PeopleTopic(ctx, company, id)
		if err != nil {
			return nil, err
		}
		if openOnly && !v.Open {
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// PeopleTopic reads one topic with its items resolved.
func (s *Store) PeopleTopic(ctx context.Context, company, id string) (TopicView, error) {
	t, err := s.Topic(ctx, company, id)
	if err != nil {
		return TopicView{}, err
	}
	items, err := s.TopicItems(ctx, company, id)
	if err != nil {
		return TopicView{}, err
	}
	v := TopicView{Topic: t, Items: []ItemView{}, At: t.CreatedAt}
	tasksSeen := map[string]bool{}
	addTask := func(taskID string) *TaskRef {
		if taskID == "" {
			return nil
		}
		tk, err := s.Task(ctx, company, taskID)
		if err != nil {
			return nil
		}
		ref := TaskRef{ID: tk.ID, Title: tk.Title, Status: tk.Status, Assignee: tk.Assignee}
		if !tasksSeen[tk.ID] {
			tasksSeen[tk.ID] = true
			v.Tasks = append(v.Tasks, ref)
		}
		return &ref
	}
	var messageIDs []string
	for _, it := range items {
		iv := ItemView{TopicItem: it}
		if it.AddedAt > v.At {
			v.At = it.AddedAt
		}
		switch it.Kind {
		case "approval":
			a, err := s.Approval(ctx, company, it.Ref)
			if err != nil {
				continue
			}
			iv.Approval, iv.Status = &a, a.Status
			iv.Waits = a.Status == "PENDING" && it.State == "open"
			if iv.Waits {
				iv.Actions = []string{"approve", "reject"}
				if a.Choice() {
					// A choice is never approved without choosing (artifact-choice-approvals).
					iv.Actions = []string{approvals.ActionChoose, approvals.ActionChanges, approvals.ActionReject}
				}
			}
			if m, err := s.Message(ctx, company, a.MessageID); err == nil {
				m.Text = PeopleText(m.Body)
				iv.Message = &m
				messageIDs = append(messageIDs, m.ID)
			}
			iv.Task = addTask(a.TaskID)
		case "question", "escalation", "update":
			m, err := s.Message(ctx, company, it.Ref)
			if err != nil {
				continue
			}
			m.Text = PeopleText(m.Body)
			iv.Message, iv.Status = &m, m.Status
			iv.Waits = it.Kind != "update" && it.State == "open" && (m.Status == "PENDING" || m.Status == "ESCALATED" || m.Status == "UNREAD" && m.MessageType == "question")
			if iv.Waits {
				if len(m.Questions) == 1 && len(m.Questions[0].Options) > 0 {
					iv.Actions = append([]string{}, m.Questions[0].Options...)
				} else {
					iv.Actions = []string{"answer"}
				}
			}
			messageIDs = append(messageIDs, m.ID)
			iv.Task = addTask(m.TaskID)
		case "decision":
			d, err := s.Decision(ctx, company, it.Ref)
			if err != nil {
				continue
			}
			iv.Decision, iv.Status = &d, d.Status
			iv.Waits = it.State == "open" && (d.Status == "proposed" || d.Status == "under_discussion")
			if iv.Waits {
				iv.Actions = []string{"approve", "reject"}
			}
			if d.MessageID != "" {
				messageIDs = append(messageIDs, d.MessageID)
			}
			iv.Task = addTask(d.TaskID)
		case "review":
			iv.Task = addTask(it.Ref)
			if iv.Task != nil {
				iv.Status = iv.Task.Status
				iv.Waits = it.State == "open" && iv.Task.Status == "IN_REVIEW"
				if iv.Waits {
					iv.Actions = []string{"accept", "return"}
				}
			}
		}
		if iv.Waits {
			v.Waiting++
		}
		v.Items = append(v.Items, iv)
	}
	v.Open = v.Waiting > 0
	// Who waits: the tasks parked on the topic's questions and approvals (people-topics D6).
	if len(messageIDs) > 0 {
		rows, err := s.DB.QueryContext(ctx, "SELECT DISTINCT task_id FROM task_parks WHERE company_id=? AND status='active' AND ref_id = ANY(?)", company, messageIDs)
		if err == nil {
			for rows.Next() {
				var id string
				if rows.Scan(&id) == nil {
					if tk, err := s.Task(ctx, company, id); err == nil {
						v.Blocked = append(v.Blocked, TaskRef{ID: tk.ID, Title: tk.Title, Status: tk.Status, Assignee: tk.Assignee})
					}
				}
			}
			rows.Close()
		}
	}
	return v, nil
}

// TopicDecision is one item decided from a topic (people-topics D5).
type TopicDecision struct {
	Kind    string         `json:"kind"`
	Ref     string         `json:"ref"`
	Version int            `json:"version"`
	Action  string         `json:"action"`
	Reason  string         `json:"reason,omitempty"`
	Answers map[string]any `json:"answers,omitempty"`
	// Option and Options decide a choice approval (artifact-choice-approvals): choose takes the option (from 1),
	// changes may name the options it is about.
	Option  int   `json:"option,omitempty"`
	Options []int `json:"options,omitempty"`
}

// DecideTopic decides several items of a topic in one transaction: all or none (people-topics D5: "approve all",
// "retry all"). Approvals take approve or reject (a rejection needs its reason); questions and escalations take
// one of their options. Decisions and reviews are decided one by one on their own endpoints.
func (s *Store) DecideTopic(ctx context.Context, company, topic, key string, items []TopicDecision) ([]string, error) {
	if len(items) == 0 || len(items) > 64 {
		return nil, inputError("items: 1 to 64")
	}
	return mutate(ctx, s, company, "topic.decide:"+topic, key, items, func(tx *Tx) ([]string, error) {
		var done []string
		for i, d := range items {
			var in int
			if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM people_topic_items WHERE company_id=? AND topic_id=? AND kind=? AND ref_id=? AND state='open'", tx.company, topic, d.Kind, d.Ref).Scan(&in); err != nil {
				return done, err
			}
			if in == 0 {
				return done, inputError(fmt.Sprintf("item %d (%s %s) is not open in this topic", i, d.Kind, d.Ref))
			}
			rk := fmt.Sprintf("%s:%d", key, i)
			switch d.Kind {
			case "approval":
				reason := strings.TrimSpace(d.Reason)
				if reason == "" {
					switch d.Action {
					case "reject", approvals.ActionChanges:
						return done, inputError("a rejection or a request for changes needs its text (" + d.Ref + ")")
					case "approve":
						reason = "approved by owner"
					}
				}
				if _, err := decideApprovalTx(ctx, tx, d.Ref, approvals.Decision{Version: d.Version, Action: d.Action, Reason: reason, RequestID: rk, Option: d.Option, Options: d.Options}); err != nil {
					return done, err
				}
			case "question", "escalation":
				m, err := messageTx(ctx, tx, d.Ref)
				if err != nil {
					return done, err
				}
				answers := d.Answers
				if answers == nil && len(m.Questions) == 1 {
					answers = map[string]any{m.Questions[0].ID: d.Action}
				}
				body := strings.TrimSpace(d.Reason)
				if body == "" {
					body = d.Action
				}
				if _, err = replyMessageTx(ctx, tx, d.Ref, messaging.Reply{Version: d.Version, Sender: "owner", Body: body, Answers: answers, RequestID: rk}); err != nil {
					return done, err
				}
			default:
				return done, inputError(d.Kind + " is decided on its own (decision record, task review)")
			}
			done = append(done, d.Ref)
		}
		return done, nil
	})
}

// BriefTopic completes or restates a topic's brief and its items' notes, and moves items to a new topic
// (factory_people_brief, people-topics D13). Only the topic's bringer, the agents above it and the items' authors
// may: Core checks that before.
func (s *Store) BriefTopic(ctx context.Context, company, topic, actor, key string, brief *messaging.Brief, notes map[string]messaging.ItemNote, move []string) (TopicView, error) {
	out, err := mutate(ctx, s, company, "topic.brief:"+topic, key, struct {
		Brief *messaging.Brief
		Notes map[string]messaging.ItemNote
		Move  []string
	}{brief, notes, move}, func(tx *Tx) (string, error) {
		t, err := topicTx(ctx, tx, topic)
		if err != nil {
			return "", err
		}
		now := tx.now().Unix()
		if brief != nil && !brief.Empty() {
			b := brief.Trimmed()
			if f := b.TooLong(); f != "" {
				return "", &BriefError{TooLong: f}
			}
			merged := t.Brief
			for _, f := range []struct {
				dst *string
				v   string
			}{{&merged.Title, b.Title}, {&merged.Ask, b.Ask}, {&merged.After, b.After}, {&merged.Recommendation, b.Recommendation}, {&merged.Changes, b.Changes}, {&merged.Scope, b.Scope}, {&merged.Context, b.Context}} {
				if f.v != "" {
					*f.dst = f.v
				}
			}
			if miss := merged.Missing(messaging.FieldTitle, messaging.FieldAsk); len(miss) > 0 {
				return "", &BriefError{Missing: miss}
			}
			raw, _ := json.Marshal(merged)
			if _, err = tx.ExecContext(ctx, "UPDATE people_topics SET brief=?,needs_brief=false,updated_at=? WHERE company_id=? AND id=?", string(raw), now, tx.company, topic); err != nil {
				return "", err
			}
		}
		for ref, n := range notes {
			raw, _ := json.Marshal(n)
			if _, err = tx.ExecContext(ctx, "UPDATE people_topic_items SET note=? WHERE company_id=? AND topic_id=? AND ref_id=?", string(raw), tx.company, topic, ref); err != nil {
				return "", err
			}
		}
		if len(move) > 0 {
			// The moved items open a topic of their own; its brief comes with this call or later.
			nt := Topic{ID: protocol.ID(), ProjectID: t.ProjectID, Key: "own:" + topic + ":" + strings.Join(move, ","), BroughtBy: actor, ConversationID: t.ConversationID, CreatedAt: now, UpdatedAt: now}
			if _, err = tx.ExecContext(ctx, "INSERT INTO people_topics(company_id,id,project_id,key,status,brief,needs_brief,brought_by,conversation_id,created_at,updated_at) VALUES(?,?,?,?,'open','{}',true,?,?,?,?)",
				tx.company, nt.ID, nt.ProjectID, nt.Key, nt.BroughtBy, nt.ConversationID, now, now); err != nil {
				return "", err
			}
			for _, ref := range move {
				res, err := tx.ExecContext(ctx, "UPDATE people_topic_items SET topic_id=? WHERE company_id=? AND topic_id=? AND ref_id=?", nt.ID, tx.company, topic, ref)
				if err != nil {
					return "", err
				}
				if n, _ := res.RowsAffected(); n == 0 {
					return "", inputError("item " + ref + " is not in topic " + topic)
				}
				if _, err = tx.ExecContext(ctx, "UPDATE messages SET topic_id=? WHERE company_id=? AND topic_id=? AND (id=? OR id IN (SELECT message_id FROM approvals WHERE company_id=? AND id=?))", nt.ID, tx.company, topic, ref, tx.company, ref); err != nil {
					return "", err
				}
			}
		}
		return topic, feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "topic.briefed", EntityID: topic, ProjectID: t.ProjectID, CreatedAt: tx.now(), Agent: actor, Actor: actor, Summary: "people topic " + topic + " briefed by " + actor})
	})
	if err != nil {
		return TopicView{}, err
	}
	return s.PeopleTopic(ctx, company, out)
}
