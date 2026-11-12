package core

import (
	"context"
	"errors"
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
	for _, co := range c.companies() {
		if err := c.Store.ExpireMessages(ctx, co); err != nil {
			return err
		}
	}
	return nil
}

// hierarchyHooks gives storage what it needs from the agent catalog.
func (c *Core) hierarchyHooks() {
	c.Store.EscalationTarget = c.escalationTargetIn
	c.Store.ManagerOf = c.escalationTargetIn
	c.Store.SubtreeOf = func(company, agent string) []string { return config.Subtree(c.cat(company).Agents, agent) }
	c.Store.TeamOf = func(company, agent string) string { return c.cat(company).TeamOf(agent) }
	c.Store.LeadOf = func(company, team string) string {
		if lead := c.cat(company).Lead(team); lead != "" {
			return lead
		}
		return c.defaultReport(company)
	}
	c.Store.EscalateAfterFor = func(company, agent, category string) time.Duration {
		return c.cat(company).Agents[agent].EscalateAfter(category)
	}
	c.Store.ExplicitExits = c.ExplicitExits
}

// EnsureConversations creates every agent's conversation task in every project (catalog load).
func (c *Core) EnsureConversations(ctx context.Context) error {
	for _, co := range c.companies() {
		if err := c.Store.EnsureConversationTasks(ctx, co, config.Names(c.cat(co).Agents)); err != nil {
			return err
		}
	}
	return nil
}

// defaultReport is the owner's default direct report when the company has one team: its lead (company-teams
// D5); with several teams the project's team decides (ownerAddressee).
func (c *Core) defaultReport(company string) string {
	cat := c.cat(company)
	if lead := cat.Lead(""); lead != "" {
		return lead
	}
	if len(cat.Teams) == 0 {
		if top := config.DirectReports(cat.Agents, config.Owner); len(top) == 1 {
			return top[0]
		}
	}
	return ""
}

// teamLead is the lead the owner's unaddressed message goes to (company-teams D5): the lead of the project's
// team; outside a team's project the lead of the team the owner picked; with one team, its lead.
func (c *Core) teamLead(ctx context.Context, company, project, team string) string {
	cat := c.cat(company)
	if project != "" {
		if p, err := c.Store.Project(ctx, company, project); err == nil && p.Team != "" {
			return cat.Lead(p.Team)
		}
	}
	if team != "" {
		return cat.Lead(team)
	}
	return c.defaultReport(company)
}

// ownerAddressee picks the direct report an owner message goes to: "@name ..." names one (a team lead),
// otherwise the lead of the project's team (teamLead). The address stays in the body.
func (c *Core) ownerAddressee(ctx context.Context, company, project, team, body string) (string, bool) {
	if strings.HasPrefix(body, "@") {
		name, _, _ := strings.Cut(strings.TrimPrefix(body, "@"), " ")
		name = strings.TrimRight(name, ",:")
		cat := c.cat(company)
		if !slices.Contains(config.DirectReports(cat.Agents, config.Owner), name) {
			return "", false
		}
		// In a team's project only that team's lead: another team does not work there (company-teams D4).
		if project != "" {
			if p, err := c.Store.Project(ctx, company, project); err == nil && p.Team != "" && cat.TeamOf(name) != "" && cat.TeamOf(name) != p.Team {
				return "", false
			}
		}
		return name, true
	}
	d := c.teamLead(ctx, company, project, team)
	return d, d != ""
}

func (c *Core) conversationRoutes(m *http.ServeMux) {
	// An owner message without a conversation (CLI, Telegram): to the default direct report or @name, on the
	// owner conversation with that agent.
	m.HandleFunc("POST /messages", func(w http.ResponseWriter, r *http.Request) {
		var p struct {
			ProjectID   string `json:"project_id"`
			Team        string `json:"team,omitempty"`
			Body        string `json:"body"`
			MessageType string `json:"message_type,omitempty"`
			RequestID   string `json:"request_id"`
		}
		if !decode(w, r, &p) {
			return
		}
		v, err := c.ownerMessage(r.Context(), c.companyFrom(r), p.ProjectID, p.Team, p.Body, p.MessageType, p.RequestID)
		if errors.Is(err, errNoAddressee) {
			http.Error(w, err.Error(), 400)
			return
		}
		reply(w, v, err)
	})
}

var errNoAddressee = errors.New("address a team lead with @name (the project team's lead in a team's project), or name the team")

// ownerMessage sends an owner message without a conversation (CLI, Telegram) to the default direct report or
// @name, on the owner conversation with that agent in project; delivery is a trigger on the agent's
// conversation task there.
func (c *Core) ownerMessage(ctx context.Context, company, project, team, body, messageType, requestID string) (messaging.Message, error) {
	agent, ok := c.ownerAddressee(ctx, company, project, team, strings.TrimSpace(body))
	if !ok {
		return messaging.Message{}, errNoAddressee
	}
	return c.ownerMessageTo(ctx, company, project, agent, body, messageType, requestID)
}

// ownerMessageTo sends an owner message to agent on the owner conversation with it in project (Telegram routes
// to an agent it already chose).
func (c *Core) ownerMessageTo(ctx context.Context, company, project, agent, body, messageType, requestID string) (messaging.Message, error) {
	conv, err := c.Store.CreateConversation(ctx, company, messaging.ConversationCreate{ID: "owner-" + agent + "-" + project, ProjectID: project, Agent: agent, Title: "Owner and " + agent, RequestID: "owner-conversation-" + agent + "-" + project})
	if err != nil {
		return messaging.Message{}, err
	}
	if messageType == "" {
		messageType = "question"
	}
	return c.Store.SendMessage(ctx, company, messaging.Send{ProjectID: project, Sender: "owner", Recipient: agent, ConversationID: conv.ID, MessageType: messageType, Body: body, RequestID: requestID})
}
