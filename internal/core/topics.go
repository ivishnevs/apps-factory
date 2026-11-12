package core

import (
	"net/http"
	"strings"

	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
	"appfactory/internal/storage"
)

// People topics (people-topics D4–D9): what people decide, grouped; the Inbox and the chat read them here.
func (c *Core) topicRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /topics", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		v, err := c.Store.PeopleTopics(r.Context(), c.companyFrom(r), q.Get("project"), q.Get("open") == "1" || q.Get("open") == "true")
		reply(w, v, err)
	})
	m.HandleFunc("GET /topics/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.PeopleTopic(r.Context(), c.companyFrom(r), r.PathValue("id"))
		reply(w, v, err)
	})
	// Several items decided at once: all or none (people-topics D5).
	m.HandleFunc("POST /topics/{id}/decide", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Items     []storage.TopicDecision `json:"items"`
			RequestID string                  `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		if p.RequestID == "" {
			http.Error(w, "request_id required", 400)
			return
		}
		company := c.companyFrom(r)
		for _, it := range p.Items {
			if it.Kind == "approval" && it.Action == "approve" {
				a, err := c.Store.Approval(r.Context(), company, it.Ref)
				if err != nil {
					reply(w, nil, err)
					return
				}
				if !mayApprove(principalOf(r).Role, a) || destructiveCatalogChange(a.Call) && !(principalOf(r).Role == "" || principalOf(r).Role == storage.RoleOwner) {
					http.Error(w, "approval "+it.Ref+" needs an owner or admin: decide it on its own", http.StatusForbidden)
					return
				}
			}
		}
		c.approvalMu.Lock()
		defer c.approvalMu.Unlock()
		done, err := c.Store.DecideTopic(r.Context(), company, r.PathValue("id"), p.RequestID, p.Items)
		reply(w, map[string]any{"decided": done}, err)
	})
	// A person writes in the topic's thread: a message to the lead of the topic's chat (people-topics D9).
	m.HandleFunc("POST /topics/{id}/messages", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Body      string `json:"body"`
			RequestID string `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		company := c.companyFrom(r)
		t, err := c.Store.Topic(r.Context(), company, r.PathValue("id"))
		if err != nil {
			reply(w, nil, err)
			return
		}
		if t.ConversationID == "" {
			http.Error(w, "this topic has no lead chat: answer its items", 409)
			return
		}
		conv, err := c.Store.Conversation(r.Context(), company, t.ConversationID)
		if err != nil {
			reply(w, nil, err)
			return
		}
		v, err := c.Store.SendMessage(r.Context(), company, messaging.Send{ProjectID: conv.ProjectID, Sender: "owner", Recipient: conv.Agent, ConversationID: conv.ID, MessageType: "question", Body: p.Body, RequestID: p.RequestID, Topic: t.ID})
		reply(w, v, err)
	})
	m.HandleFunc("GET /settings/company/people_language", func(w http.ResponseWriter, r *http.Request) {
		v, err := c.Store.PeopleLanguage(r.Context(), c.companyFrom(r))
		reply(w, map[string]any{"people_language": v, "languages": storage.PeopleLanguages}, err)
	})
	m.HandleFunc("PUT /settings/company/people_language", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			Language string `json:"people_language"`
		}
		if !decode(w, r, &p) {
			return
		}
		err := c.Store.SetPeopleLanguage(r.Context(), c.companyFrom(r), strings.TrimSpace(p.Language))
		reply(w, map[string]any{"people_language": p.Language}, err)
	})
}

// peopleBriefTool is factory_people_brief (people-topics D13): the topic's bringer, an agent of its manager chain or
// the author of one of its items completes or restates what people read.
func (c *Core) peopleBriefTool(w http.ResponseWriter, r *http.Request, p factorytools.Call, run storage.Run) {
	company := c.companyFrom(r)
	id, _ := p.Arguments["topic_id"].(string)
	key, _ := p.Arguments["request_id"].(string)
	t, err := c.Store.Topic(r.Context(), company, id)
	if err != nil {
		reply(w, nil, err)
		return
	}
	if t.ProjectID != run.Spec.Project {
		http.Error(w, "topic of another project", 403)
		return
	}
	items, err := c.Store.TopicItems(r.Context(), company, id)
	if err != nil {
		reply(w, nil, err)
		return
	}
	agent := run.Spec.Agent
	allowed := t.BroughtBy == agent
	for _, it := range items {
		if it.BroughtBy == agent {
			allowed = true
		}
	}
	// The agents above the bringer (its manager chain) may brief it too.
	for who, n := t.BroughtBy, 0; !allowed && who != "" && who != "owner" && who != "factory" && n < 16; n++ {
		who = c.escalationTargetIn(company, who)
		allowed = who == agent
	}
	if !allowed && t.BroughtBy == "factory" {
		allowed = c.leadOfProject(r, t.ProjectID) == agent
	}
	if !allowed {
		http.Error(w, "this topic is not yours to brief: its bringer, the agents above it and its items' authors may", 403)
		return
	}
	brief, _, _ := messaging.PeopleArgs(p.Arguments)
	notes := map[string]messaging.ItemNote{}
	if m, ok := p.Arguments["notes"].(map[string]any); ok {
		for ref, v := range m {
			if n, ok := v.(map[string]any); ok {
				what, _ := n["what"].(string)
				changes, _ := n["changes"].(string)
				notes[ref] = messaging.ItemNote{What: what, Changes: changes}
			}
		}
	}
	var move []string
	if list, ok := p.Arguments["move"].([]any); ok {
		for _, x := range list {
			if s, ok := x.(string); ok && s != "" {
				move = append(move, s)
			}
		}
	}
	v, err := c.Store.BriefTopic(r.Context(), company, id, agent, key, brief, notes, move)
	reply(w, v, err)
}

// leadOfProject is the lead of the project's team.
func (c *Core) leadOfProject(r *http.Request, project string) string {
	if c.Store.LeadOf == nil {
		return ""
	}
	p, err := c.Store.Project(r.Context(), c.companyFrom(r), project)
	if err != nil {
		return ""
	}
	return c.Store.LeadOf(c.companyFrom(r), p.Team)
}
