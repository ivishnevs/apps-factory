// Package messaging defines durable asynchronous communication, independent of a runtime.
package messaging

type Conversation struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Agent     string `json:"agent"`
	Title     string `json:"title"`
	Kind      string `json:"kind"`
	SessionID string `json:"session_id,omitempty"`
	// TaskID is the agent's conversation task that handles this conversation's messages (hierarchy D5); set on reads.
	TaskID    string `json:"task_id,omitempty"`
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
	// Person is the person who wrote an owner message (people-attribution); empty for agents and older messages.
	Person   string `json:"person,omitempty"`
	PersonID string `json:"person_id,omitempty"`
	// Channel is where a person's message came from: web or telegram (telegram-reply-routing).
	Channel string `json:"channel,omitempty"`
	// DelegatedTo: an approval request an agent decides under delegation (approval-delegation); hidden from the
	// people's Inbox until that agent escalates it.
	DelegatedTo      string         `json:"delegated_to,omitempty"`
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
	// Category is a routed question's decision category; Route lists its earlier recipients (hierarchy D3).
	Category string   `json:"category,omitempty"`
	Route    []string `json:"route,omitempty"`
	// UpChain marks a question routed up the reports_to chain (asks, escalations): unanswered by an agent
	// for escalate_after since RoutedAt, it moves one level up (hierarchy D4).
	UpChain  bool  `json:"up_chain,omitempty"`
	RoutedAt int64 `json:"routed_at,omitempty"`
	// TaskWakeSeq and TaskAssignee are the asking task's state when a question was asked (hierarchy D3); a task
	// changed since (new triggers or another assignee) presents the question MayBeOutdated (set on reads).
	TaskWakeSeq   int64  `json:"task_wake_seq,omitempty"`
	TaskAssignee  string `json:"task_assignee,omitempty"`
	MayBeOutdated bool   `json:"may_be_outdated,omitempty"`
	// Details is the owner's deferred answer to a question (inbox-badge-parity): "promised" keeps the question open
	// ("I'll send details") until the details are "sent" or the promise is "withdrawn"; DetailsNote is what the
	// owner wrote with the promise, DetailsAt when it was made.
	Details     string `json:"details,omitempty"`
	DetailsNote string `json:"details_note,omitempty"`
	DetailsAt   int64  `json:"details_at,omitempty"`
	// Attachments are files of the sender's worktree uploaded with the message, Materials references to existing
	// ones (inbox-visual-previews); a forward keeps both.
	Attachments []Attachment  `json:"attachments,omitempty"`
	Materials   []MaterialRef `json:"materials,omitempty"`
	// People topics (people-topics): the topic the message belongs to, the brief its bringer wrote and its note in
	// the topic; Text is the body without technical lines, filled on reads for people.
	TopicID string    `json:"topic_id,omitempty"`
	Brief   *Brief    `json:"brief,omitempty"`
	Note    *ItemNote `json:"note,omitempty"`
	Text    string    `json:"text,omitempty"`
}

// MaxVisuals bounds the attachments, and separately the material references, of one message (as report
// attachments).
const MaxVisuals = 16

// Preview is a smaller rendition of a large image (inbox-visual-previews 2.2); the original stays downloadable.
type Preview struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Attachment is a file of the sender's worktree the node uploaded with a message (credentials redacted), shown in
// the order given with its caption.
type Attachment struct {
	Path     string   `json:"path"`
	Caption  string   `json:"caption,omitempty"`
	SHA256   string   `json:"sha256"`
	Size     int64    `json:"size"`
	Redacted bool     `json:"redacted,omitempty"`
	Preview  *Preview `json:"preview,omitempty"`
}

// MaterialRef names an existing material: an artifact version (ArtifactID, ArtifactVersion) or a report attachment
// (ReportID, Path). Core fills in Name, SHA256, Size, URI and Redacted from what it names.
type MaterialRef struct {
	ArtifactID      string   `json:"artifact_id,omitempty"`
	ArtifactVersion string   `json:"artifact_version,omitempty"`
	ReportID        string   `json:"report_id,omitempty"`
	Path            string   `json:"path,omitempty"`
	Caption         string   `json:"caption,omitempty"`
	Name            string   `json:"name,omitempty"`
	SHA256          string   `json:"sha256,omitempty"`
	Size            int64    `json:"size,omitempty"`
	URI             string   `json:"uri,omitempty"`
	Redacted        bool     `json:"redacted,omitempty"`
	Preview         *Preview `json:"preview,omitempty"`
}

// Details values of a question and of a reply.
const (
	DetailsPromised  = "promised"
	DetailsSent      = "sent"
	DetailsWithdrawn = "withdrawn"
)

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
	Category       string     `json:"category,omitempty"`
	UpChain        bool       `json:"up_chain,omitempty"`
	// Attachments and Materials travel with the message (inbox-visual-previews).
	Attachments []Attachment  `json:"attachments,omitempty"`
	Materials   []MaterialRef `json:"materials,omitempty"`
	// People topics: the bringer's brief and note, an explicit topic, and what the item is (people-topics D1).
	Brief  *Brief    `json:"brief,omitempty"`
	Note   *ItemNote `json:"note,omitempty"`
	Topic  string    `json:"topic,omitempty"`
	People PeopleRef `json:"-"`
	// Strict: an agent's tool call brings it, so the gate checks the brief it owes (people-topics D3).
	Strict bool `json:"-"`
}
type Reply struct {
	// Details: "promised" defers the answer (the question stays open, the asking task waits for the owner's
	// details), "withdrawn" withdraws such a promise; "" answers (with the details, when they were promised).
	Details   string         `json:"details,omitempty"`
	Version   int            `json:"version"`
	Sender    string         `json:"sender"`
	Body      string         `json:"body"`
	Answers   map[string]any `json:"answers,omitempty"`
	RequestID string         `json:"request_id"`
}
