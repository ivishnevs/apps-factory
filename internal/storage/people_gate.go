package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
)

// People topics (people-topics D1): toPeople is the only way an item becomes visible to people. sendMessageTx calls
// it for every message to the owner and forwardTx for every question moved to the owner, so nothing reaches the
// people's Inbox or chat around it. It finds the item's topic (version line, task, Core key, explicit topic), checks
// the brief the bringer owes for the item's path (D3), and records the item.

// TopicLineWindow is how long a topic keeps an artifact's version line: a new version brought to people within it
// joins the row of the earlier one, and a settled topic reopens (people-topics D4).
const TopicLineWindow = 30 * 24 * time.Hour

// BriefError refuses an item that reaches people without the brief its bringer owes (people-topics D3); nothing was
// sent.
type BriefError struct {
	Path    string
	Missing []string
	TooLong string
	Joining bool
	Seen    bool
}

func (e *BriefError) Error() string {
	if e.TooLong != "" {
		return "brief." + e.TooLong + " is too long"
	}
	return "brief_required: " + strings.Join(e.Missing, ", ")
}
func (e *BriefError) Unwrap() error { return ErrInput }

// Hint says what to write, for the refusal the agent reads.
func (e *BriefError) Hint() string {
	if e.TooLong != "" {
		return fmt.Sprintf("brief.%s is over its limit (title %d, ask %d, other fields %d characters): shorten it and call again.", e.TooLong, messaging.MaxTitle, messaging.MaxAsk, messaging.MaxField)
	}
	var b strings.Builder
	b.WriteString("This goes to people (the owner), so they read your brief instead of your text; nothing was sent. ")
	if e.Joining {
		b.WriteString("It joins a topic people already have open, so a note is enough. ")
	}
	help := map[string]string{
		messaging.FieldTitle:          "brief.title: one line, what this is about",
		messaging.FieldAsk:            "brief.ask: what they decide in one sentence, naming exactly what is approved (the whole mockup, one element, one state)",
		messaging.FieldAfter:          "brief.after: what happens after each answer (who does what next)",
		messaging.FieldRecommendation: "brief.recommendation: your advice and why",
		messaging.FieldChanges:        "brief.changes (or note.changes): what changed since the version they already saw",
		messaging.FieldContext:        "brief.context: why you need it",
		messaging.FieldWhat:           "note.what: what this item is",
		messaging.FieldNoteChanges:    "note.changes: what changed since the version they already saw",
	}
	b.WriteString("Add ")
	for i, f := range e.Missing {
		if i > 0 {
			b.WriteString("; ")
		}
		b.WriteString(help[f])
	}
	b.WriteString(". Write it in the company's language for people, then call again with the same request_id.")
	return b.String()
}

// gateItem is one item reaching people.
type gateItem struct {
	ref       messaging.PeopleRef
	project   string
	task      string
	bringer   string
	brief     *messaging.Brief
	note      *messaging.ItemNote
	topic     string // explicit topic id
	strict    bool   // an agent brings it from a run: its brief is checked
	messageID string
	options   bool
}

// Topic is a people topic as stored (people-topics D4).
type Topic struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	Key            string          `json:"key"`
	Status         string          `json:"status"`
	Brief          messaging.Brief `json:"brief"`
	NeedsBrief     bool            `json:"needs_brief,omitempty"`
	BroughtBy      string          `json:"brought_by"`
	ConversationID string          `json:"conversation_id,omitempty"`
	CreatedAt      int64           `json:"created_at"`
	UpdatedAt      int64           `json:"updated_at"`
}

// briefNeeds are the fields a bringer owes on path: when the item opens its topic, when it joins one, and whether
// a version people saw makes changes required (people-topics D3).
func briefNeeds(path string, joining, seen, options bool) (brief, note []string) {
	switch path {
	case messaging.PathQuestion:
		if !joining {
			brief = []string{messaging.FieldTitle, messaging.FieldAfter}
			if options {
				brief = append(brief, messaging.FieldRecommendation)
			}
		}
	case messaging.PathForward, messaging.PathArtifact:
		if joining {
			note = []string{messaging.FieldWhat}
		} else {
			brief = []string{messaging.FieldTitle, messaging.FieldAsk, messaging.FieldAfter, messaging.FieldRecommendation}
		}
	case messaging.PathCall:
		if joining {
			note = []string{messaging.FieldWhat}
		} else {
			brief = []string{messaging.FieldTitle, messaging.FieldContext}
		}
	case messaging.PathDecision:
		if !joining {
			brief = []string{messaging.FieldAfter}
		}
	case messaging.PathReview:
		if !joining {
			brief = []string{messaging.FieldAsk}
		}
	}
	if seen && path == messaging.PathArtifact {
		note = append(note, messaging.FieldNoteChanges)
	}
	return brief, note
}

// itemKind is the topic item kind of a path.
func itemKind(ref messaging.PeopleRef) string {
	if ref.Kind != "" {
		return ref.Kind
	}
	switch ref.Path {
	case messaging.PathCall, messaging.PathArtifact:
		return "approval"
	case messaging.PathDecision:
		return "decision"
	case messaging.PathReview:
		return "review"
	case messaging.PathCore:
		return "escalation"
	case messaging.PathUpdate:
		return "update"
	}
	return "question"
}

// toPeople puts an item reaching people into its topic, checking the bringer's brief; it returns the topic id ("" for
// an update without an open topic of its task).
func toPeople(ctx context.Context, tx *Tx, it gateItem) (string, error) {
	if it.brief != nil {
		it.brief = it.brief.Trimmed()
		if f := it.brief.TooLong(); f != "" {
			return "", &BriefError{Path: it.ref.Path, TooLong: f}
		}
	}
	now := tx.now()
	found, err := findTopic(ctx, tx, it, now)
	if err != nil {
		return "", err
	}
	if found == nil && it.ref.Path == messaging.PathUpdate {
		return "", nil // a notification without an open topic of its task stays a chat message
	}
	seen := false
	if it.ref.Lineage != "" {
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM people_topic_items WHERE company_id=? AND lineage=? AND version<>?)", tx.company, it.project+"/"+it.ref.Lineage, it.ref.Version).Scan(&seen); err != nil {
			return "", err
		}
	}
	joining := found != nil
	if it.strict {
		needBrief, needNote := briefNeeds(it.ref.Path, joining, seen, it.options || it.ref.HasOptions)
		missing := it.brief.Missing(needBrief...)
		for _, f := range needNote {
			v := ""
			switch f {
			case messaging.FieldWhat:
				if it.note != nil {
					v = it.note.What
				}
				if v == "" {
					v = it.brief.Get(messaging.FieldTitle) // a full brief also names the item
				}
			case messaging.FieldNoteChanges:
				if it.note != nil {
					v = it.note.Changes
				}
				if v == "" {
					v = it.brief.Get(messaging.FieldChanges)
				}
			}
			if strings.TrimSpace(v) == "" {
				missing = append(missing, f)
			}
		}
		if len(missing) > 0 {
			return "", &BriefError{Path: it.ref.Path, Missing: missing, Joining: joining, Seen: seen}
		}
	}
	topic := found
	if topic == nil {
		topic = &Topic{ID: protocol.ID(), ProjectID: it.project, Key: topicKey(it), Status: "open", BroughtBy: it.bringer, CreatedAt: now.Unix(), UpdatedAt: now.Unix(),
			ConversationID: topicChat(ctx, tx, it)}
		if it.brief != nil {
			topic.Brief = *it.brief
		}
		topic.NeedsBrief = topic.Brief.Empty() || it.ref.Path == messaging.PathTimeout && it.brief == nil
		b, _ := json.Marshal(topic.Brief)
		if _, err = tx.ExecContext(ctx, "INSERT INTO people_topics(company_id,id,project_id,key,status,brief,needs_brief,brought_by,conversation_id,created_at,updated_at) VALUES(?,?,?,?,?,?,(?::int<>0),?,?,?,?)",
			tx.company, topic.ID, topic.ProjectID, topic.Key, "open", string(b), boolInt(topic.NeedsBrief), topic.BroughtBy, topic.ConversationID, topic.CreatedAt, topic.UpdatedAt); err != nil {
			return "", err
		}
	} else {
		restated := it.brief != nil && strings.TrimSpace(it.brief.Title) != "" && strings.TrimSpace(it.brief.Ask) != ""
		if restated {
			// A full brief given when joining restates the topic (people-topics D4).
			b, _ := json.Marshal(it.brief)
			if _, err = tx.ExecContext(ctx, "UPDATE people_topics SET brief=?,needs_brief=false WHERE company_id=? AND id=?", string(b), tx.company, topic.ID); err != nil {
				return "", err
			}
		}
		if _, err = tx.ExecContext(ctx, "UPDATE people_topics SET status='open',updated_at=?,settled_at=0 WHERE company_id=? AND id=?", now.Unix(), tx.company, topic.ID); err != nil {
			return "", err
		}
	}
	kind, ref := itemKind(it.ref), it.ref.Ref
	if ref == "" {
		ref = it.messageID
	}
	note := it.note
	if note == nil && joining && it.brief != nil {
		note = &messaging.ItemNote{What: it.brief.Title, Changes: it.brief.Changes}
	}
	if note == nil && !joining && it.brief != nil && it.brief.Changes != "" {
		note = &messaging.ItemNote{Changes: it.brief.Changes}
	}
	nb, _ := json.Marshal(note)
	if note == nil {
		nb = []byte("{}")
	}
	lineage := ""
	if it.ref.Lineage != "" {
		lineage = it.project + "/" + it.ref.Lineage
		// The earlier versions of the line in this topic are replaced by this one: their rows show it, their buttons go
		// (people-topics D10: their chat and Telegram messages are retired).
		old, err := topicIDs(ctx, tx, `SELECT CASE WHEN i.kind='approval' THEN coalesce((SELECT message_id FROM approvals a WHERE a.company_id=i.company_id AND a.id=i.ref_id),'') ELSE i.ref_id END
			FROM people_topic_items i WHERE i.company_id=? AND i.topic_id=? AND i.lineage=? AND i.version<>? AND i.state='open'`, tx.company, topic.ID, lineage, it.ref.Version)
		if err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE people_topic_items SET state='replaced' WHERE company_id=? AND topic_id=? AND lineage=? AND version<>? AND state='open'", tx.company, topic.ID, lineage, it.ref.Version); err != nil {
			return "", err
		}
		for _, m := range old {
			if m == "" {
				continue
			}
			if err = feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "topic.item_replaced", EntityID: topic.ID, ProjectID: it.project, CreatedAt: now, TaskID: it.task, Actor: it.bringer, Attention: true,
				Summary: it.ref.Lineage + " " + it.ref.Version, Ref: m}); err != nil {
				return "", err
			}
		}
	}
	state := "open"
	if kind == "update" {
		state = "update"
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO people_topic_items(company_id,topic_id,kind,ref_id,lineage,version,note,brought_by,state,added_at) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (company_id,topic_id,kind,ref_id) DO UPDATE SET note=EXCLUDED.note, brought_by=EXCLUDED.brought_by, state=EXCLUDED.state, added_at=EXCLUDED.added_at`,
		tx.company, topic.ID, kind, ref, lineage, it.ref.Version, string(nb), it.bringer, state, now.Unix()); err != nil {
		return "", err
	}
	// An item that waits for people is an attention row: Telegram sends it (people-topics D10).
	return topic.ID, feed(ctx, tx, FeedItem{EventID: protocol.ID(), Type: "topic.item_added", EntityID: topic.ID, ProjectID: it.project, CreatedAt: now, TaskID: it.task, Agent: it.bringer, Actor: it.bringer,
		Summary: kind + " " + ref + " → people topic " + topic.ID + " (" + topic.Key + ")", Ref: ref, Attention: kind != "update"})
}

// topicKey is the key a new topic opens under (people-topics D4).
func topicKey(it gateItem) string {
	switch {
	case it.ref.Key != "":
		return it.ref.Key
	case it.ref.Lineage != "":
		return "line:" + it.ref.Lineage
	case it.task != "":
		return "task:" + it.task
	}
	return "own:" + it.messageID + it.ref.Ref
}

// findTopic is the open topic the item belongs to, nil when it opens one: an explicit topic, the topic of the
// artifact's version line (within TopicLineWindow, settled ones too), Core's key, then the task's open topic.
func findTopic(ctx context.Context, tx *Tx, it gateItem, now time.Time) (*Topic, error) {
	if it.topic != "" {
		t, err := topicTx(ctx, tx, it.topic)
		if errors.Is(err, ErrNotFound) || err == nil && t.ProjectID != it.project {
			return nil, inputError("topic " + it.topic + " is not a people topic of this project")
		}
		return &t, err
	}
	if it.ref.Lineage != "" {
		var id string
		err := tx.QueryRowContext(ctx, "SELECT topic_id FROM people_topic_items WHERE company_id=? AND lineage=? AND added_at>=? ORDER BY seq DESC LIMIT 1", tx.company, it.project+"/"+it.ref.Lineage, now.Add(-TopicLineWindow).Unix()).Scan(&id)
		if err == nil {
			t, err := topicTx(ctx, tx, id)
			return &t, err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	keys := []string{}
	if it.ref.Key != "" {
		keys = append(keys, it.ref.Key)
	}
	if it.task != "" {
		keys = append(keys, "task:"+it.task)
	}
	for _, key := range keys {
		rows, err := topicIDs(ctx, tx, "SELECT id FROM people_topics WHERE company_id=? AND project_id=? AND key=? ORDER BY created_at DESC", tx.company, it.project, key)
		if err != nil {
			return nil, err
		}
		for _, id := range rows {
			open, err := topicOpenTx(ctx, tx, id)
			if err != nil {
				return nil, err
			}
			if open {
				t, err := topicTx(ctx, tx, id)
				return &t, err
			}
		}
	}
	// A task's items also find the topic its artifacts' lines opened (a key line:… topic holding the task's items).
	if it.task != "" {
		var id string
		err := tx.QueryRowContext(ctx, `SELECT i.topic_id FROM people_topic_items i JOIN people_topics t ON t.company_id=i.company_id AND t.id=i.topic_id
			WHERE i.company_id=? AND t.project_id=? AND (i.ref_id=? OR i.ref_id IN (SELECT id FROM approvals WHERE company_id=? AND task_id=?) OR i.ref_id IN (SELECT id FROM messages WHERE company_id=? AND task_id=?))
			ORDER BY i.seq DESC LIMIT 1`, tx.company, it.project, it.task, tx.company, it.task, tx.company, it.task).Scan(&id)
		if err == nil {
			if open, err := topicOpenTx(ctx, tx, id); err != nil || open {
				t, terr := topicTx(ctx, tx, id)
				return &t, errors.Join(err, terr)
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	return nil, nil
}

// topicChat is the chat a new topic lives in: the people's chat with the lead of the bringer's team, else the
// project's lead (people-topics D9; owner 2026-11-12).
func topicChat(ctx context.Context, tx *Tx, it gateItem) string {
	lead := ""
	if tx.store.LeadOf != nil && tx.store.TeamOf != nil && it.bringer != "" && it.bringer != "factory" {
		if team := tx.store.TeamOf(tx.company, it.bringer); team != "" {
			lead = tx.store.LeadOf(tx.company, team)
		}
	}
	if lead == "" {
		lead = leadOf(ctx, tx, it.project)
	}
	if lead == "" {
		return ""
	}
	return ownerChat(ctx, tx, lead, it.project)
}

func topicIDs(ctx context.Context, tx *Tx, query string, args ...any) ([]string, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func topicTx(ctx context.Context, tx *Tx, id string) (Topic, error) {
	return readTopic(ctx, tx, tx.company, id)
}

func readTopic(ctx context.Context, q querier, company, id string) (Topic, error) {
	var t Topic
	var brief string
	err := q.QueryRowContext(ctx, "SELECT id,project_id,key,status,brief,needs_brief,brought_by,conversation_id,created_at,updated_at FROM people_topics WHERE company_id=? AND id=?", company, id).
		Scan(&t.ID, &t.ProjectID, &t.Key, &t.Status, &brief, &t.NeedsBrief, &t.BroughtBy, &t.ConversationID, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	if err == nil {
		_ = json.Unmarshal([]byte(brief), &t.Brief)
	}
	return t, err
}

// openItemsSQL counts a topic's items that still wait for people: pending approvals, open questions and
// escalations, proposed decisions, tasks in review. Replaced rows never wait.
const openItemsSQL = `SELECT count(*) FROM people_topic_items i WHERE i.company_id=? AND i.topic_id=? AND i.state='open' AND (
	(i.kind='approval' AND EXISTS(SELECT 1 FROM approvals a WHERE a.company_id=i.company_id AND a.id=i.ref_id AND a.status='PENDING'))
	OR (i.kind IN ('question','escalation') AND EXISTS(SELECT 1 FROM messages m WHERE m.company_id=i.company_id AND m.id=i.ref_id AND m.status IN ('PENDING','UNREAD','ESCALATED')))
	OR (i.kind='escalation' AND EXISTS(SELECT 1 FROM escalations e WHERE e.company_id=i.company_id AND e.message_id=i.ref_id AND e.status='open'))
	OR (i.kind='decision' AND EXISTS(SELECT 1 FROM decisions d WHERE d.company_id=i.company_id AND d.id=i.ref_id AND d.status IN ('proposed','under_discussion')))
	OR (i.kind='review' AND EXISTS(SELECT 1 FROM tasks t WHERE t.company_id=i.company_id AND t.id=i.ref_id AND t.status='IN_REVIEW')))`

func topicOpenTx(ctx context.Context, tx *Tx, id string) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, openItemsSQL, tx.company, id).Scan(&n)
	return n > 0, err
}
