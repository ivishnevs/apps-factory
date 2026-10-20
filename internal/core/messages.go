package core

import (
	"encoding/json"
	"net/http"

	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

func (c *Core) messageRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /conversations", func(w http.ResponseWriter, r *http.Request) {
		var p messaging.ConversationCreate
		if !decode(w, r, &p) {
			return
		}
		if !c.agentExists(w, p.Agent) {
			return
		}
		v, err := c.Store.CreateConversation(r.Context(), c.companyFrom(r), p)
		reply(w, v, err)
	})
	m.HandleFunc("GET /conversations", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Conversations(r.Context(), c.companyFrom(r), r.URL.Query().Get("project"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /conversations/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Conversation(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		if _, err := c.Store.Conversation(r.Context(), c.companyFrom(r), r.PathValue("id")); err != nil {
			reply(w, nil, err)
			return
		}
		v, err := c.Store.Messages(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("POST /conversations/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Body           string               `json:"body"`
			MessageType    string               `json:"message_type"`
			Questions      []messaging.Question `json:"questions,omitempty"`
			RequestID      string               `json:"request_id"`
			TimeoutSeconds int                  `json:"timeout_seconds,omitempty"`
		}
		if !decode(w, r, &p) {
			return
		}
		conv, err := c.Store.Conversation(r.Context(), c.companyFrom(r), r.PathValue("id"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		if !c.agentExists(w, conv.Agent) {
			return
		}
		if p.MessageType == "" {
			p.MessageType = "question"
		}
		v, err := c.Store.SendMessage(r.Context(), c.companyFrom(r), messaging.Send{ProjectID: conv.ProjectID, Sender: "owner", Recipient: conv.Agent, ConversationID: conv.ID, MessageType: p.MessageType, Body: p.Body, Questions: p.Questions, RequestID: p.RequestID, TimeoutSeconds: p.TimeoutSeconds})
		reply(w, v, err)
	})
	m.HandleFunc("GET /messages/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Message(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	m.HandleFunc("GET /inbox", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.Inbox(r.Context(), c.companyFrom(r), r.URL.Query().Get("project"))
		reply(w, v, err)
	})
	m.HandleFunc("POST /inbox/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Version   int    `json:"version"`
			RequestID string `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.ReadInbox(r.Context(), c.companyFrom(r), r.PathValue("id"), p.Version, p.RequestID)
		reply(w, v, err)
	})
	m.HandleFunc("POST /inbox/{id}/answer", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Version   int            `json:"version"`
			Body      string         `json:"body"`
			Answers   map[string]any `json:"answers,omitempty"`
			RequestID string         `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.Store.ReplyMessage(r.Context(), c.companyFrom(r), r.PathValue("id"), messaging.Reply{Version: p.Version, Sender: "owner", Body: p.Body, Answers: p.Answers, RequestID: p.RequestID})
		reply(w, v, err)
	})
}

func (c *Core) messageTool(w http.ResponseWriter, r *http.Request, pName string, args map[string]any, run storage.Run, snapshot, current config.Agent) {
	ctx := r.Context()
	messageID, _ := args["message_id"].(string)
	conversationID, _ := args["conversation_id"].(string)
	var origin messaging.Message
	if run.Spec.MessageID != "" {
		v, err := c.Store.Message(ctx, c.companyFrom(r), run.Spec.MessageID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		origin = v
		if conversationID == "" {
			conversationID = v.ConversationID
		}
	}
	if messageID != "" {
		m, err := c.Store.Message(ctx, c.companyFrom(r), messageID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if m.ProjectID != run.Spec.Project {
			http.Error(w, "message outside run project", 403)
			return
		}
	}
	if conversationID != "" {
		conv, err := c.Store.Conversation(ctx, c.companyFrom(r), conversationID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if conv.ProjectID != run.Spec.Project {
			http.Error(w, "conversation outside run project", 403)
			return
		}
	}
	switch pName {
	case "factory_message_get":
		v, err := c.Store.Message(ctx, c.companyFrom(r), messageID)
		reply(w, v, err)
		return
	case "factory_message_list":
		if conversationID == "" {
			http.Error(w, "conversation_id required outside message run", 400)
			return
		}
		v, err := c.Store.Messages(ctx, c.companyFrom(r), conversationID)
		reply(w, v, err)
		return
	case "factory_question_forward":
		target, err := c.Store.Message(ctx, c.companyFrom(r), messageID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if target.Recipient != run.Spec.Agent {
			http.Error(w, "question is not pending for this recipient", 403)
			return
		}
		version, _ := args["version"].(float64)
		note, _ := args["note"].(string)
		key, _ := args["request_id"].(string)
		v, err := c.Store.ForwardQuestion(ctx, c.companyFrom(r), messageID, run.Spec.Agent, int(version), note, key)
		reply(w, v, err)
		return
	case "factory_message_reply":
		target, err := c.Store.Message(ctx, c.companyFrom(r), messageID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if target.Recipient != run.Spec.Agent || target.Status == "ESCALATED" {
			http.Error(w, "question is not pending for this recipient", 403)
			return
		}
		// A categorized question is answered only with that category delegated (captured and current
		// policy); otherwise it goes up the chain (hierarchy D3).
		if target.MessageType == "question" && target.Category != "" && (len(snapshot.DecisionAuthority(target.Category)) == 0 || len(current.DecisionAuthority(target.Category)) == 0) {
			http.Error(w, "decisions in category "+target.Category+" are not delegated to you: forward the question with factory_question_forward", 403)
			return
		}
		v, err := c.Store.ReplyMessage(ctx, c.companyFrom(r), messageID, messaging.Reply{Version: int(args["version"].(float64)), Sender: run.Spec.Agent, Body: args["body"].(string), Answers: asAnswers(args["answers"]), RequestID: args["request_id"].(string)})
		reply(w, v, err)
		return
	}
	recipient, _ := args["recipient"].(string)
	if pName == "factory_owner_ask" {
		recipient = "owner"
	}
	if recipient != "owner" {
		if !c.agentExists(w, recipient) {
			return
		}
	}
	for _, a := range []config.Agent{snapshot, current} {
		found := false
		for _, v := range a.Spec.Communication.CanMessage {
			if v == recipient {
				found = true
				break
			}
		}
		if !found {
			http.Error(w, "communication target denied", 403)
			return
		}
	}
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		taskID = run.Spec.TaskID
		if taskID == "" {
			taskID = origin.TaskID
		}
	}
	blocking, _ := args["blocking"].(bool)
	if blocking && (run.Spec.TaskID == "" || taskID != run.Spec.TaskID) {
		http.Error(w, "blocking question must refer to current task", 403)
		return
	}
	if taskID != "" {
		task, err := c.Store.Task(ctx, c.companyFrom(r), taskID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		if task.ProjectID != run.Spec.Project {
			http.Error(w, "task outside run project", 403)
			return
		}
	}
	messageType := "question"
	if pName == "factory_message_send" {
		messageType = args["message_type"].(string)
		if messageType == "question" || messageType == "answer" {
			http.Error(w, "use ask/reply for questions/answers", 400)
			return
		}
	}
	var questions []messaging.Question
	if value, ok := args["questions"]; ok {
		b, _ := json.Marshal(value)
		if err := json.Unmarshal(b, &questions); err != nil {
			http.Error(w, "invalid form", 400)
			return
		}
	}
	timeout := 0
	if v, ok := args["timeout_seconds"].(float64); ok {
		timeout = int(v)
	}
	v, err := c.Store.SendMessage(ctx, c.companyFrom(r), messaging.Send{ProjectID: run.Spec.Project, TaskID: taskID, Sender: run.Spec.Agent, Recipient: recipient, ConversationID: conversationID, MessageType: messageType, Body: args["body"].(string), Questions: questions, Blocking: blocking, TimeoutSeconds: timeout, RequestID: args["request_id"].(string), RunID: run.Spec.ID})
	reply(w, v, err)
}
func asAnswers(v any) map[string]any { m, _ := v.(map[string]any); return m }
