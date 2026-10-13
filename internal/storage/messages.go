package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/protocol"
	"appfactory/internal/tasks"
)

func (s *Store) Conversation(ctx context.Context, company, id string) (messaging.Conversation, error) {
	return getDoc[messaging.Conversation](ctx, s.DB, "SELECT body FROM conversations WHERE company_id=? AND id=?", company, id)
}
func conversationTx(ctx context.Context, tx *Tx, id string) (messaging.Conversation, error) {
	return getDoc[messaging.Conversation](ctx, tx, "SELECT body FROM conversations WHERE company_id=? AND id=?", tx.company, id)
}
func (s *Store) Conversations(ctx context.Context, company, project string) ([]messaging.Conversation, error) {
	return listDocs[messaging.Conversation](ctx, s.DB, "SELECT body FROM conversations WHERE company_id=? AND project_id=? ORDER BY rowid", company, project)
}
func (s *Store) CreateConversation(ctx context.Context, company string, in messaging.ConversationCreate) (messaging.Conversation, error) {
	return mutate(ctx, s, company, "conversation.create", in.RequestID, in, func(tx *Tx) (messaging.Conversation, error) {
		if in.RequestID == "" || strings.TrimSpace(in.Title) == "" || len(in.Title) > 1024 || in.Agent == "" || in.Agent == "owner" {
			return messaging.Conversation{}, inputError("request_id, title and recipient agent required")
		}
		return createConversation(ctx, tx, in, "owner")
	})
}
func createConversation(ctx context.Context, tx *Tx, in messaging.ConversationCreate, kind string) (messaging.Conversation, error) {
	v := messaging.Conversation{ID: in.ID, ProjectID: in.ProjectID, Agent: in.Agent, Title: in.Title, Kind: kind, CreatedAt: tx.now().Unix()}
	if v.ID == "" {
		v.ID = protocol.ID()
	}
	if !entityName.MatchString(v.ID) {
		return v, inputError("invalid conversation id")
	}
	if _, err := projectTx(ctx, tx, v.ProjectID); err != nil {
		return v, err
	}
	b, _ := json.Marshal(v)
	if _, err := tx.ExecContext(ctx, "INSERT INTO conversations VALUES(?,?,?,?)", v.ID, v.ProjectID, b, tx.company); err != nil {
		return v, err
	}
	return v, entityEvent(ctx, tx, "conversation.created", v.ProjectID, v.ID, v)
}
func (s *Store) Message(ctx context.Context, company, id string) (messaging.Message, error) {
	return getDoc[messaging.Message](ctx, s.DB, "SELECT body FROM messages WHERE company_id=? AND id=?", company, id)
}
func messageTx(ctx context.Context, tx *Tx, id string) (messaging.Message, error) {
	return getDoc[messaging.Message](ctx, tx, "SELECT body FROM messages WHERE company_id=? AND id=?", tx.company, id)
}
func (s *Store) Messages(ctx context.Context, company, conversation string) ([]messaging.Message, error) {
	return listDocs[messaging.Message](ctx, s.DB, "SELECT body FROM messages WHERE company_id=? AND conversation_id=? ORDER BY rowid", company, conversation)
}
func (s *Store) Inbox(ctx context.Context, company, project string) ([]messaging.Message, error) {
	return listDocs[messaging.Message](ctx, s.DB, "SELECT body FROM messages WHERE company_id=? AND (?='' OR project_id=?) AND (status='ESCALATED' OR (recipient='owner' AND status IN ('PENDING','UNREAD'))) ORDER BY created_at,rowid", company, project, project)
}
func (s *Store) PendingMessages(ctx context.Context, company string) ([]messaging.Message, error) {
	return listDocs[messaging.Message](ctx, s.DB, "SELECT body FROM messages WHERE company_id=? AND delivery_status='PENDING' AND recipient!='owner' AND status IN ('PENDING','UNREAD') ORDER BY created_at,rowid", company)
}
func validQuestions(questions []messaging.Question) error {
	if len(questions) > 16 {
		return inputError("maximum 16 structured questions")
	}
	seen := map[string]bool{}
	for _, q := range questions {
		if !entityName.MatchString(q.ID) || seen[q.ID] || strings.TrimSpace(q.Question) == "" || len(q.Question) > 4096 {
			return inputError("invalid or duplicate question id/text")
		}
		seen[q.ID] = true
		switch q.Type {
		case "text", "confirmation":
			if len(q.Options) > 0 {
				return inputError("text/confirmation cannot have options")
			}
		case "single_choice", "multiple_choice":
			if len(q.Options) < 2 || len(q.Options) > 20 {
				return inputError("choice questions require 2..20 options")
			}
		default:
			return inputError("unsupported question type")
		}
		opts := map[string]bool{}
		for _, o := range q.Options {
			if strings.TrimSpace(o) == "" || len(o) > 1024 || opts[o] {
				return inputError("invalid/duplicate option")
			}
			opts[o] = true
		}
	}
	return nil
}
func (s *Store) SendMessage(ctx context.Context, company string, in messaging.Send) (messaging.Message, error) {
	return mutate(ctx, s, company, "message.send:"+in.Sender+":"+in.ProjectID+":"+in.RunID, in.RequestID, in, func(tx *Tx) (messaging.Message, error) {
		return sendMessageTx(ctx, tx, in)
	})
}
func sendMessageTx(ctx context.Context, tx *Tx, in messaging.Send) (messaging.Message, error) {
	m := messaging.Message{ReportID: in.ReportID, DecisionID: in.DecisionID, ID: protocol.ID(), ProjectID: in.ProjectID, TaskID: in.TaskID, Sender: in.Sender, Recipient: in.Recipient, ConversationID: in.ConversationID, MessageType: in.MessageType, Body: in.Body, Questions: in.Questions, Blocking: in.Blocking, Status: "UNREAD", DeliveryStatus: "PENDING", Version: 1, CreatedAt: tx.now().Unix()}
	if in.RequestID == "" || in.Sender == "" || in.Recipient == "" || in.Sender == in.Recipient || strings.TrimSpace(in.Body) == "" || len(in.Body) > 16*1024 {
		return m, inputError("request_id, distinct sender/recipient and body up to 16 KiB required")
	}
	switch in.MessageType {
	case "question", "notification", "review_request", "review_result", "escalation":
	default:
		return m, inputError("invalid message_type; use reply for answers")
	}
	if in.MessageType != "question" && (in.Blocking || len(in.Questions) > 0) {
		return m, inputError("only questions may block or contain a form")
	}
	if err := validQuestions(in.Questions); err != nil {
		return m, err
	}
	if in.TimeoutSeconds < 0 || in.TimeoutSeconds > 7*24*3600 {
		return m, inputError("timeout must be 1 second to 7 days (0 defaults to 24h)")
	}
	if in.MessageType == "question" {
		m.Status = "PENDING"
		timeout := in.TimeoutSeconds
		if timeout == 0 {
			timeout = 86400
		}
		m.Deadline = m.CreatedAt + int64(timeout)
	}
	if m.ConversationID == "" {
		chain := in.RunID
		if in.TaskID != "" {
			chain = in.TaskID
		}
		if chain == "" {
			chain = in.RequestID
		}
		hash := sha256.Sum256([]byte(m.ProjectID + "\x00" + m.Sender + "\x00" + chain))
		cid := "chain-" + hex.EncodeToString(hash[:16])
		conv, err := conversationTx(ctx, tx, cid)
		if err == ErrNotFound {
			runes := []rune(in.Body)
			conv, err = createConversation(ctx, tx, messaging.ConversationCreate{ID: cid, ProjectID: m.ProjectID, Agent: m.Recipient, Title: string(runes[:min(len(runes), 128)])}, "agent")
		}
		if err != nil {
			return m, err
		}
		m.ConversationID = conv.ID
	}
	conv, err := conversationTx(ctx, tx, m.ConversationID)
	if err != nil {
		return m, err
	}
	if conv.ProjectID != m.ProjectID {
		return m, inputError("conversation outside project")
	}
	if conv.Kind == "owner" && in.Sender == "owner" && in.Recipient != conv.Agent {
		return m, inputError("owner conversation recipient must match its agent")
	}
	if in.TaskID != "" {
		t, err := taskTx(ctx, tx, in.TaskID)
		if err != nil {
			return m, err
		}
		if t.ProjectID != m.ProjectID {
			return m, inputError("task outside project")
		}
	}
	if in.Blocking {
		if in.TaskID == "" || in.RunID == "" {
			return m, inputError("blocking requires current task/run")
		}
		t, err := taskTx(ctx, tx, in.TaskID)
		if err != nil {
			return m, err
		}
		if t.Status != "IN_PROGRESS" || t.CurrentRunID != in.RunID || t.Assignee != in.Sender || t.WaitMessageID != "" || t.CancelRequested {
			return m, ErrConflict
		}
		var status, agent, project string
		var until int64
		if err = tx.QueryRowContext(ctx, "SELECT status,agent,project,lease_until FROM runs WHERE company_id=? AND id=?", tx.company, in.RunID).Scan(&status, &agent, &project, &until); err != nil {
			return m, err
		}
		if status != "RUNNING" || agent != in.Sender || project != in.ProjectID || !tx.store.LeaseLive(until) {
			return m, ErrConflict
		}
		t.WaitMessageID = m.ID
		t.BlockReason = "message: waiting for answer"
		t.Submission = nil
		if err = saveTask(ctx, tx, &t, "task.waiting"); err != nil {
			return m, err
		}
		if err = addPark(ctx, tx, t, tasks.ParkAsk, m.ID, "waiting for the answer to question "+m.ID+" from "+m.Recipient, m.Recipient == "owner"); err != nil {
			return m, err
		}
	}
	if m.MessageType == "question" && m.Sender != "owner" {
		var count int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM messages WHERE (conversation_id=? OR (?!='' AND task_id=?)) AND message_type='question' AND sender!='owner'", m.ConversationID, m.TaskID, m.TaskID).Scan(&count); err != nil {
			return m, err
		}
		if count >= 8 {
			m.Status = "ESCALATED"
			m.DeliveryStatus = "NONE"
			m.EscalationReason = "automatic clarification limit (8) reached"
		}
	}
	if m.Recipient == "owner" {
		m.DeliveryStatus = "NONE"
	}
	canonical, _ := json.Marshal(m.Questions)
	fingerprint := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(m.Body)), " ") + "\x00" + m.Sender + "\x00" + m.Recipient + "\x00" + m.TaskID + "\x00" + string(canonical)))
	b, _ := json.Marshal(m)
	if _, err = tx.ExecContext(ctx, "INSERT INTO messages VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", m.ID, m.ProjectID, m.ConversationID, nullable(m.TaskID), m.Sender, m.Recipient, m.MessageType, m.Status, m.DeliveryStatus, m.Version, b, m.CreatedAt, hex.EncodeToString(fingerprint[:]), tx.company); err != nil {
		return m, err
	}
	return m, entityEvent(ctx, tx, "message.created", m.ProjectID, m.ID, m)
}
func saveMessage(ctx context.Context, tx *Tx, m *messaging.Message, kind string) error {
	old := m.Version
	m.Version++
	b, _ := json.Marshal(m)
	result, err := tx.ExecContext(ctx, "UPDATE messages SET status=?,delivery_status=?,version=?,body=? WHERE id=? AND version=?", m.Status, m.DeliveryStatus, m.Version, b, m.ID, old)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrConflict
	}
	return entityEvent(ctx, tx, kind, m.ProjectID, m.ID, m)
}
func validAnswers(qs []messaging.Question, answers map[string]any) error {
	if len(answers) != len(qs) {
		return inputError("answer every structured question exactly once")
	}
	for _, q := range qs {
		v, ok := answers[q.ID]
		if !ok {
			return inputError("missing answer " + q.ID)
		}
		option := func(value string) bool {
			for _, o := range q.Options {
				if value == o {
					return true
				}
			}
			return false
		}
		switch q.Type {
		case "text":
			s, ok := v.(string)
			if !ok || strings.TrimSpace(s) == "" || len(s) > 4096 {
				return inputError("text answer required")
			}
		case "confirmation":
			if _, ok := v.(bool); !ok {
				return inputError("boolean confirmation required")
			}
		case "single_choice":
			s, ok := v.(string)
			if !ok || !option(s) {
				return inputError("answer must be a listed option")
			}
		case "multiple_choice":
			a, ok := v.([]any)
			if !ok || len(a) == 0 {
				return inputError("nonempty array of options required")
			}
			seen := map[string]bool{}
			for _, v := range a {
				s, ok := v.(string)
				if !ok || !option(s) || seen[s] {
					return inputError("invalid or repeated choice")
				}
				seen[s] = true
			}
		}
	}
	return nil
}
func (s *Store) ReplyMessage(ctx context.Context, company, id string, in messaging.Reply) (messaging.Message, error) {
	return mutate(ctx, s, company, "message.reply:"+id, in.RequestID, in, func(tx *Tx) (messaging.Message, error) {
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM approvals WHERE company_id=? AND message_id=?", company, id).Scan(&count); err != nil {
			return messaging.Message{}, err
		}
		if count > 0 {
			return messaging.Message{}, inputError("use approval decision endpoint")
		}
		return replyMessageTx(ctx, tx, id, in)
	})
}
func replyMessageTx(ctx context.Context, tx *Tx, id string, in messaging.Reply) (messaging.Message, error) {
	m, err := messageTx(ctx, tx, id)
	if err != nil {
		return m, err
	}
	if m.MessageType != "question" || m.Status != "PENDING" && m.Status != "ESCALATED" || m.Version != in.Version {
		return m, ErrConflict
	}
	if in.Sender != m.Recipient && in.Sender != "owner" {
		return m, inputError("only recipient or owner can answer")
	}
	if (m.Status == "ESCALATED" || m.Deadline <= tx.now().Unix()) && in.Sender != "owner" {
		return m, ErrConflict
	}
	if in.RequestID == "" || strings.TrimSpace(in.Body) == "" || len(in.Body) > 16*1024 {
		return m, inputError("request_id and answer body up to 16 KiB required")
	}
	if err = validAnswers(m.Questions, in.Answers); err != nil {
		return m, err
	}
	answer := messaging.Message{ID: protocol.ID(), ProjectID: m.ProjectID, TaskID: m.TaskID, Sender: in.Sender, Recipient: m.Sender, ConversationID: m.ConversationID, MessageType: "answer", Body: in.Body, Answers: in.Answers, InReplyTo: m.ID, Status: "READ", Version: 1, CreatedAt: tx.now().Unix(), DeliveryStatus: "NONE"}
	if !m.Blocking && m.Sender != "owner" {
		answer.Status = "UNREAD"
		answer.DeliveryStatus = "PENDING"
		if in.Sender == "owner" {
			// A manager that escalated can receive the owner's answer in a new run.
			// Reopen only a known-completed delivery, never timeout/UNKNOWN work.
			rows, e := tx.QueryContext(ctx, "SELECT body FROM messages WHERE conversation_id=? AND recipient=? AND status='ESCALATED' AND delivery_status='COMPLETED'", m.ConversationID, m.Sender)
			if e != nil {
				return answer, e
			}
			reopen := []messaging.Message{}
			for rows.Next() {
				var data []byte
				var previous messaging.Message
				if e = rows.Scan(&data); e != nil {
					rows.Close()
					return answer, e
				}
				if e = json.Unmarshal(data, &previous); e != nil {
					rows.Close()
					return answer, e
				}
				if strings.HasPrefix(previous.EscalationReason, "recipient run ended without") && previous.Deadline > tx.now().Unix() {
					reopen = append(reopen, previous)
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return answer, e
			}
			for _, previous := range reopen {
				previous.Status = "PENDING"
				previous.EscalationReason = ""
				if e = saveMessage(ctx, tx, &previous, "message.reopened"); e != nil {
					return answer, e
				}
			}
		}
	}
	b, _ := json.Marshal(answer)
	if _, err = tx.ExecContext(ctx, "INSERT INTO messages VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)", answer.ID, answer.ProjectID, answer.ConversationID, nullable(answer.TaskID), answer.Sender, answer.Recipient, answer.MessageType, answer.Status, answer.DeliveryStatus, answer.Version, b, answer.CreatedAt, "", tx.company); err != nil {
		return answer, err
	}
	m.Status = "ANSWERED"
	if m.DeliveryStatus == "PENDING" {
		m.DeliveryStatus = "NONE"
	}
	if err = saveMessage(ctx, tx, &m, "message.answered"); err != nil {
		return answer, err
	}
	if err = entityEvent(ctx, tx, "message.created", answer.ProjectID, answer.ID, answer); err != nil {
		return answer, err
	}
	if err = resumeWaitingTask(ctx, tx, m); err != nil {
		return answer, err
	}
	if m.Blocking && m.TaskID != "" {
		t, err := taskTx(ctx, tx, m.TaskID)
		if err != nil {
			return answer, err
		}
		if err = clearParks(ctx, tx, t, tasks.ParkAsk, m.ID, "cleared"); err != nil {
			return answer, err
		}
		if err = addTrigger(ctx, tx, t, "answer", isOwner(in.Sender), in.Sender, m.ID, in.Body); err != nil {
			return answer, err
		}
	}
	return answer, nil
}
func resumeWaitingTask(ctx context.Context, tx *Tx, m messaging.Message) error {
	if !m.Blocking || m.TaskID == "" {
		return nil
	}
	t, err := taskTx(ctx, tx, m.TaskID)
	if err != nil {
		return err
	}
	if t.WaitMessageID != m.ID || t.Status != "BLOCKED" || !strings.HasPrefix(t.BlockReason, "message:") {
		return nil
	}
	var status string
	if err = tx.QueryRowContext(ctx, "SELECT status FROM runs WHERE id=?", t.CurrentRunID).Scan(&status); err != nil {
		return err
	}
	if status != "COMPLETED" {
		return nil
	}
	t.Status = "READY"
	t.BlockReason = ""
	t.RetryEligible = false
	return saveTask(ctx, tx, &t, "task.resumed")
}
func (s *Store) DispatchMessage(ctx context.Context, company string, m messaging.Message, spec protocol.RunSpec) (protocol.RunSpec, error) {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return spec, err
	}
	defer tx.Rollback()
	saved, err := messageTx(ctx, tx, m.ID)
	if err != nil {
		return spec, err
	}
	if saved.Version != m.Version || saved.DeliveryStatus != "PENDING" || saved.Recipient != spec.Agent || saved.ProjectID != spec.Project || saved.Status != "PENDING" && saved.Status != "UNREAD" {
		return spec, ErrConflict
	}
	if paused, err := pausedFor(ctx, tx, saved.ProjectID, spec.Agent); err != nil || len(paused) > 0 {
		if err == nil {
			err = ErrConflict
		}
		return spec, err
	}
	var busy int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE company_id=? AND agent=? AND status IN ('QUEUED','RUNNING','CANCELLING')", company, spec.Agent).Scan(&busy); err != nil {
		return spec, err
	}
	if busy > 0 {
		return spec, ErrConflict
	}
	conv, err := getDoc[messaging.Conversation](ctx, tx, "SELECT body FROM conversations WHERE id=?", m.ConversationID)
	if err != nil {
		return spec, err
	}
	if conv.Kind == "owner" && m.Sender == "owner" && spec.Agent == conv.Agent {
		spec.SessionID = conv.SessionID
	}
	spec.MessageID = m.ID
	spec.TaskID = ""
	if err = insertRun(ctx, tx, &spec); err != nil {
		return spec, constraint(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO message_runs VALUES(?,?,?)", spec.ID, m.ID, company); err != nil {
		return spec, err
	}
	saved.DeliveryRunID = spec.ID
	saved.DeliveryStatus = "RUNNING"
	if err = saveMessage(ctx, tx, &saved, "message.dispatched"); err != nil {
		return spec, err
	}
	return spec, tx.Commit()
}
func syncMessageRun(ctx context.Context, tx *Tx, runID string) error {
	var id string
	if err := tx.QueryRowContext(ctx, "SELECT message_id FROM message_runs WHERE run_id=?", runID).Scan(&id); err == sql.ErrNoRows {
		return nil
	} else if err != nil {
		return err
	}
	m, err := getDoc[messaging.Message](ctx, tx, "SELECT body FROM messages WHERE id=?", id)
	if err != nil {
		return err
	}
	r, err := getDoc[protocol.RunSpec](ctx, tx, "SELECT spec FROM runs WHERE id=?", runID)
	if err != nil {
		return err
	}
	var status, session string
	if err = tx.QueryRowContext(ctx, "SELECT status,session_id FROM runs WHERE id=?", runID).Scan(&status, &session); err != nil {
		return err
	}
	if status == "RUNNING" || status == "QUEUED" || status == "CANCELLING" {
		return nil
	}
	// Only the first known terminal result projects delivery; late events don't invent an answer.
	if m.DeliveryStatus != "RUNNING" {
		return nil
	}
	m.DeliveryStatus = status
	if m.MessageType == "question" && m.Status == "PENDING" {
		m.Status = "ESCALATED"
		m.EscalationReason = "recipient run ended without an explicit answer: " + status
	}
	if m.MessageType != "question" && status == "COMPLETED" {
		m.Status = "READ"
	}
	if status == "COMPLETED" && session != "" {
		conv, err := getDoc[messaging.Conversation](ctx, tx, "SELECT body FROM conversations WHERE id=?", m.ConversationID)
		if err != nil {
			return err
		}
		if conv.Kind == "owner" && m.Sender == "owner" && r.Agent == conv.Agent {
			conv.SessionID = session
			b, _ := json.Marshal(conv)
			if _, err = tx.ExecContext(ctx, "UPDATE conversations SET body=? WHERE id=?", b, conv.ID); err != nil {
				return err
			}
		}
	}
	return saveMessage(ctx, tx, &m, "message.delivery_completed")
}
func (s *Store) ExpireMessages(ctx context.Context, company string) error {
	tx, err := s.begin(ctx, company)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, "SELECT body FROM messages WHERE company_id=? AND message_type='question' AND status='PENDING'", company)
	if err != nil {
		return err
	}
	pending := []messaging.Message{}
	for rows.Next() {
		var b []byte
		var m messaging.Message
		if err = rows.Scan(&b); err != nil {
			rows.Close()
			return err
		}
		if err = json.Unmarshal(b, &m); err != nil {
			rows.Close()
			return err
		}
		if m.Deadline <= tx.now().Unix() {
			pending = append(pending, m)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range pending {
		m.Status = "ESCALATED"
		m.EscalationReason = "answer deadline exceeded"
		if m.DeliveryStatus == "PENDING" {
			m.DeliveryStatus = "NONE"
		}
		if err = saveMessage(ctx, tx, &m, "message.escalated"); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func messageWaitState(ctx context.Context, tx *Tx, id string) (bool, error) {
	var status string
	err := tx.QueryRowContext(ctx, "SELECT status FROM messages WHERE id=?", id).Scan(&status)
	return status == "ANSWERED", err
}
func (s *Store) AnswerFor(ctx context.Context, company, id string) (messaging.Message, error) {
	return getDoc[messaging.Message](ctx, s.DB, "SELECT body FROM messages WHERE company_id=? AND message_type='answer' AND json_extract(body,'$.in_reply_to')=? ORDER BY rowid DESC LIMIT 1", company, id)
}

// ReadInbox acknowledges owner notifications without answering or dismissing a question.
func (s *Store) ReadInbox(ctx context.Context, company, id string, version int, key string) (messaging.Message, error) {
	input := struct {
		Version   int
		RequestID string
	}{version, key}
	return mutate(ctx, s, company, "message.read:"+id, key, input, func(tx *Tx) (messaging.Message, error) {
		m, err := messageTx(ctx, tx, id)
		if err != nil {
			return m, err
		}
		if key == "" {
			return m, inputError("request_id required")
		}
		if m.Recipient != "owner" || m.MessageType == "question" || m.Status != "UNREAD" || m.Version != version {
			return m, ErrConflict
		}
		m.Status = "READ"
		return m, saveMessage(ctx, tx, &m, "message.read")
	})
}

// EscalateMessage stops an undispatchable delivery and makes it visible to the owner.
func (s *Store) EscalateMessage(ctx context.Context, company, id string, version int, reason string) error {
	input := struct {
		Version int
		Reason  string
	}{version, reason}
	_, err := mutate(ctx, s, company, "message.escalate:"+id, "scheduler-escalation", input, func(tx *Tx) (messaging.Message, error) {
		m, err := messageTx(ctx, tx, id)
		if err != nil {
			return m, err
		}
		if m.Version != version || m.DeliveryStatus != "PENDING" {
			return m, ErrConflict
		}
		m.Status = "ESCALATED"
		m.DeliveryStatus = "NONE"
		m.EscalationReason = reason
		return m, saveMessage(ctx, tx, &m, "message.escalated")
	})
	return err
}
