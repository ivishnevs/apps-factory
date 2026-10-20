package core

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/messaging"
)

// Conversation tasks (hierarchy D5) on the Core side: hooks that give storage the tree, catalog-load creation,
// owner messages to a direct report (the default one, or @name).

// ScheduleMessages expires overdue questions. Delivery to agents is a trigger on the recipient's conversation
// task, scheduled like any task; separate message delivery runs are retired.
func (c *Core) ScheduleMessages(ctx context.Context) error {
	return c.Store.ExpireMessages(ctx, c.company())
}

// hierarchyHooks gives storage what it needs from the agent catalog.
func (c *Core) hierarchyHooks() {
	c.Store.EscalationTarget = c.escalationTarget
	c.Store.ManagerOf = c.escalationTarget
	c.Store.SubtreeOf = func(agent string) []string { return config.Subtree(c.Agents, agent) }
	c.Store.EscalateAfterFor = func(agent, category string) time.Duration { return c.Agents[agent].EscalateAfter(category) }
	c.Store.ExplicitExits = c.ExplicitExits
}

// EnsureConversations creates every agent's conversation task in every project (catalog load).
func (c *Core) EnsureConversations(ctx context.Context) error {
	return c.Store.EnsureConversationTasks(ctx, c.company(), config.Names(c.Agents))
}

// defaultReport is the owner's default direct report: owner.default_report, or the only direct report.
func (c *Core) defaultReport() string {
	if c.DefaultReport != "" {
		return c.DefaultReport
	}
	if top := config.DirectReports(c.Agents, config.Owner); len(top) == 1 {
		return top[0]
	}
	return ""
}

// ownerAddressee picks the direct report an owner message goes to: "@name ..." names one, otherwise the
// default report. The address stays in the body.
func (c *Core) ownerAddressee(body string) (string, bool) {
	if strings.HasPrefix(body, "@") {
		name, _, _ := strings.Cut(strings.TrimPrefix(body, "@"), " ")
		name = strings.TrimRight(name, ",:")
		if slices.Contains(config.DirectReports(c.Agents, config.Owner), name) {
			return name, true
		}
		return "", false
	}
	d := c.defaultReport()
	return d, d != ""
}

func (c *Core) conversationRoutes(m *http.ServeMux) {
	// An owner message without a conversation (CLI, Telegram): to the default direct report or @name, on the
	// owner conversation with that agent.
	m.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			ProjectID   string `json:"project_id"`
			Body        string `json:"body"`
			MessageType string `json:"message_type,omitempty"`
			RequestID   string `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		agent, ok := c.ownerAddressee(strings.TrimSpace(p.Body))
		if !ok {
			http.Error(w, "address a direct report with @name, or set owner.default_report", 400)
			return
		}
		ctx, company := r.Context(), c.companyFrom(r)
		conv, err := c.Store.CreateConversation(ctx, company, messaging.ConversationCreate{ID: "owner-" + agent + "-" + p.ProjectID, ProjectID: p.ProjectID, Agent: agent, Title: "Owner and " + agent, RequestID: "owner-conversation-" + agent + "-" + p.ProjectID})
		if err != nil {
			reply(w, nil, err)
			return
		}
		if p.MessageType == "" {
			p.MessageType = "question"
		}
		v, err := c.Store.SendMessage(ctx, company, messaging.Send{ProjectID: p.ProjectID, Sender: "owner", Recipient: agent, ConversationID: conv.ID, MessageType: p.MessageType, Body: p.Body, RequestID: p.RequestID})
		reply(w, v, err)
	})
}
