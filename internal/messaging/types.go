// Package messaging defines durable asynchronous communication, independent of a runtime.
package messaging

type Conversation struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Agent     string `json:"agent"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	SessionID string `json:"session_id,omitempty"`
	CreatedAt int64  `json:"created_at"`
}
type ConversationCreate struct {
	ID        string `json:"id,omitempty"`
	ProjectID string `json:"project_id"`
	Agent     string `json:"agent"`
	Title     string `json:"title"`
	RequestID string `json:"request_id"`
}
type Question struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}
type Message struct {
	ReportID         string         `json:"report_id,omitempty"`
	DecisionID       string         `json:"decision_id,omitempty"`
	ApprovalID       string         `json:"approval_id,omitempty"`
	ID               string         `json:"id"`
	ProjectID        string         `json:"project_id"`
	TaskID           string         `json:"task_id,omitempty"`
	Sender           string         `json:"sender"`
	Recipient        string         `json:"recipient"`
	ConversationID   string         `json:"conversation_id"`
	MessageType      string         `json:"message_type"`
	Body             string         `json:"body"`
	Questions        []Question     `json:"questions,omitempty"`
	Answers          map[string]any `json:"answers,omitempty"`
	InReplyTo        string         `json:"in_reply_to,omitempty"`
	Blocking         bool           `json:"blocking"`
	Status           string         `json:"status"`
	Version          int            `json:"version"`
	CreatedAt        int64          `json:"created_at"`
	Deadline         int64          `json:"deadline,omitempty"`
	DeliveryRunID    string         `json:"delivery_run_id,omitempty"`
	DeliveryStatus   string         `json:"delivery_status"`
	EscalationReason string         `json:"escalation_reason,omitempty"`
}
type Send struct {
	ReportID       string     `json:"-"`
	DecisionID     string     `json:"-"`
	ProjectID      string     `json:"project_id"`
	TaskID         string     `json:"task_id,omitempty"`
	Sender         string     `json:"sender"`
	Recipient      string     `json:"recipient"`
	ConversationID string     `json:"conversation_id,omitempty"`
	MessageType    string     `json:"message_type"`
	Body           string     `json:"body"`
	Questions      []Question `json:"questions,omitempty"`
	Blocking       bool       `json:"blocking"`
	TimeoutSeconds int        `json:"timeout_seconds,omitempty"`
	RequestID      string     `json:"request_id"`
	RunID          string     `json:"run_id,omitempty"`
}
type Reply struct {
	Version   int            `json:"version"`
	Sender    string         `json:"sender"`
	Body      string         `json:"body"`
	Answers   map[string]any `json:"answers,omitempty"`
	RequestID string         `json:"request_id"`
}
