package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"appfactory/internal/config"
	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
	"appfactory/internal/storage"
)

// Telegram (waits-events-cost-cli D4): Core is the notifier's backend. Free owner messages go to a direct
// report through an owner conversation in TelegramProject (conversation tasks of hierarchy D5 replace it).

const telegramChannel = "telegram"

// TelegramConfig routes owner messages from Telegram.
type TelegramConfig struct {
	// Project holds the owner conversations ("telegram-<agent>").
	Project string
	// DefaultReport receives unaddressed messages; empty means the first direct report of the owner.
	DefaultReport string
}

// telegramBackend implements telegram.Backend for the company.
type telegramBackend struct {
	c       *Core
	company string
}

// TelegramBackend is the notifier's view of this Core.
func (c *Core) TelegramBackend() telegram.Backend { return telegramBackend{c, c.company()} }

func (c *Core) telegramRoutes(m *http.ServeMux) {
	// factory telegram bind: a one-time code the owner sends to the bot as /start CODE (15 minutes).
	m.HandleFunc("POST /telegram/bind", func(w http.ResponseWriter, r *http.Request) {
		b := telegramBackend{c, c.companyFrom(r)}
		st, err := b.LoadState(r.Context())
		if err != nil {
			reply(w, nil, err)
			return
		}
		raw := make([]byte, 4)
		_, _ = rand.Read(raw)
		st.BindCode, st.BindExpires = hex.EncodeToString(raw), c.Store.Now().Add(15*time.Minute).Unix()
		if err = b.SaveState(r.Context(), st); err != nil {
			reply(w, nil, err)
			return
		}
		write(w, map[string]any{"code": st.BindCode, "expires_at": st.BindExpires, "instruction": "send /start " + st.BindCode + " to the factory bot from the chat to bind"})
	})
}

func (b telegramBackend) LoadState(ctx context.Context) (telegram.State, error) {
	var st telegram.State
	raw, err := b.c.Store.NotifyState(ctx, b.company, telegramChannel)
	if err == nil && raw != "" {
		err = json.Unmarshal([]byte(raw), &st)
	}
	return st, err
}

func (b telegramBackend) SaveState(ctx context.Context, st telegram.State) error {
	raw, _ := json.Marshal(st)
	return b.c.Store.SaveNotifyState(ctx, b.company, telegramChannel, string(raw))
}

func (b telegramBackend) FeedTail(ctx context.Context) (int64, error) {
	return b.c.Store.FeedTail(ctx, b.company)
}

// questionOf names the question a feed row is about, if it still waits for an answer.
func (b telegramBackend) questionOf(ctx context.Context, item storage.FeedItem) (messaging.Message, bool) {
	id := ""
	switch {
	case item.Type == "message.created" || item.Type == "message.escalated" || item.Type == "message.forwarded":
		id = item.EntityID
	case strings.HasPrefix(item.Type, "task.escalation.") || strings.HasPrefix(item.Type, "task.parked."):
		id = item.Ref
	}
	if id == "" {
		return messaging.Message{}, false
	}
	m, err := b.c.Store.Message(ctx, b.company, id)
	if err != nil || m.MessageType != "question" || (m.Status != "PENDING" && m.Status != "ESCALATED") {
		return m, false
	}
	return m, true
}

func (b telegramBackend) Attention(ctx context.Context, after int64) ([]telegram.Item, error) {
	page, err := b.c.Store.Feed(ctx, b.company, storage.FeedFilter{Attention: true}, after, 50)
	if err != nil {
		return nil, err
	}
	out := []telegram.Item{}
	asked := map[string]bool{}
	for _, f := range page.Items {
		where := strings.Trim(f.ProjectID+"/"+f.TaskID, "/")
		it := telegram.Item{Seq: f.Sequence, Text: fmt.Sprintf("⚠ %s %s: %s", where, f.Type, f.Summary)}
		if m, ok := b.questionOf(ctx, f); ok {
			if asked[m.ID] { // one message per question (its message and escalation rows); no text sends nothing
				out = append(out, telegram.Item{Seq: f.Sequence})
				continue
			}
			asked[m.ID] = true
			it.QuestionID, it.Text = m.ID, fmt.Sprintf("❓ %s question from %s:\n%s", where, m.Sender, m.Body)
			if len(m.Questions) == 1 && m.Questions[0].Type == "single_choice" {
				it.Options = m.Questions[0].Options
			}
		}
		out = append(out, it)
	}
	return out, nil
}

func (b telegramBackend) PendingQuestions(ctx context.Context) ([]telegram.Question, error) {
	inbox, err := b.c.Store.Inbox(ctx, b.company, "")
	if err != nil {
		return nil, err
	}
	out := []telegram.Question{}
	for _, m := range inbox {
		if m.MessageType == "question" && (m.Status == "PENDING" || m.Status == "ESCALATED") {
			out = append(out, telegram.Question{ID: m.ID, Text: m.Body, CreatedAt: time.Unix(m.CreatedAt, 0)})
		}
	}
	return out, nil
}

func (b telegramBackend) Answer(ctx context.Context, id string, choice int, text, key string) (string, error) {
	m, err := b.c.Store.Message(ctx, b.company, id)
	if err != nil {
		return "", err
	}
	if m.Status != "PENDING" && m.Status != "ESCALATED" {
		return "Question " + id + " is already " + strings.ToLower(m.Status) + ".", nil
	}
	in := messaging.Reply{Version: m.Version, Sender: "owner", Body: text, RequestID: key}
	switch {
	case len(m.Questions) == 1 && m.Questions[0].Type == "single_choice":
		q := m.Questions[0]
		if choice < 0 {
			choice = slices.IndexFunc(q.Options, func(o string) bool { return strings.EqualFold(o, strings.TrimSpace(text)) })
		}
		if choice < 0 || choice >= len(q.Options) {
			return "", fmt.Errorf("answer one of: %s", strings.Join(q.Options, ", "))
		}
		in.Answers, in.Body = map[string]any{q.ID: q.Options[choice]}, q.Options[choice]
	case len(m.Questions) > 0:
		return "", fmt.Errorf("this question has a form: factory inbox answer %s JSON_FILE", id)
	case strings.TrimSpace(text) == "":
		return "", fmt.Errorf("an answer needs text")
	}
	if _, err = b.c.Store.ReplyMessage(ctx, b.company, id, in); err != nil {
		return "", err
	}
	return "Answered " + id + ": " + in.Body, nil
}

func (b telegramBackend) Status(ctx context.Context) (string, error) {
	r, _ := http.NewRequestWithContext(ctx, "GET", "/status", nil)
	projects, err := b.c.status(r, "")
	if err != nil {
		return "", err
	}
	var s strings.Builder
	for _, p := range projects {
		fmt.Fprintf(&s, "%s: %d running, %d runnable, %d waiting, inbox %d\n", p.Project.ID, len(p.Runs), p.Runnable, len(p.Waiting), p.Inbox)
	}
	pending, err := b.c.Store.PendingCounts(ctx, b.company)
	if err != nil {
		return "", err
	}
	pauses, err := b.c.Store.Pauses(ctx, b.company)
	if err != nil {
		return "", err
	}
	fmt.Fprintf(&s, "waiting for you: %d", pending.Total)
	for _, p := range pauses {
		fmt.Fprintf(&s, "\npaused: %s %s", p.Scope, p.Target)
	}
	return s.String(), nil
}

func (b telegramBackend) Stop(ctx context.Context) (string, error) {
	n, err := b.c.killSwitch(ctx, b.company, "telegram /stop")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Stopped: %d live runs revoked, nothing new starts until factory start.", n), nil
}

func (b telegramBackend) OwnerInput(ctx context.Context, agent, text, key string) (string, error) {
	reports := config.DirectReports(b.c.Agents, config.Owner)
	if agent == "" {
		agent = b.c.Telegram.DefaultReport
		if agent == "" && len(reports) > 0 {
			agent = reports[0]
		}
	}
	if !slices.Contains(reports, agent) {
		return "", fmt.Errorf("@%s is not one of your direct reports (%s)", agent, strings.Join(reports, ", "))
	}
	if b.c.Telegram.Project == "" {
		return "", fmt.Errorf("no project for Telegram conversations (FACTORY_TELEGRAM_PROJECT)")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("empty message")
	}
	id := "telegram-" + agent
	if _, err := b.c.Store.Conversation(ctx, b.company, id); err != nil {
		if _, err = b.c.Store.CreateConversation(ctx, b.company, messaging.ConversationCreate{ID: id, ProjectID: b.c.Telegram.Project, Agent: agent, Title: "Owner via Telegram", RequestID: "create-" + id}); err != nil {
			return "", err
		}
	}
	if _, err := b.c.Store.SendMessage(ctx, b.company, messaging.Send{ProjectID: b.c.Telegram.Project, Sender: "owner", Recipient: agent, ConversationID: id, MessageType: "question", Body: text, RequestID: key}); err != nil {
		return "", err
	}
	return "Sent to " + agent + ".", nil
}

// Digest: done, in progress, waiting for the owner, failures and retries, nodes and the cost of the last day.
func (b telegramBackend) Digest(ctx context.Context, now time.Time) (string, error) {
	since := now.Add(-24 * time.Hour)
	counts := map[string]int{}
	var after int64
	for {
		page, err := b.c.Store.Feed(ctx, b.company, storage.FeedFilter{}, after, 100)
		if err != nil {
			return "", err
		}
		for _, f := range page.Items {
			if f.CreatedAt.Before(since) {
				continue
			}
			switch {
			case f.Type == "task.exit.done":
				counts["done"]++
			case f.Type == "task.retry":
				counts["retries"]++
			case strings.HasPrefix(f.Type, "task.escalation."):
				counts["escalations"]++
			}
		}
		after = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	r, _ := http.NewRequestWithContext(ctx, "GET", "/status", nil)
	projects, err := b.c.status(r, "")
	if err != nil {
		return "", err
	}
	running := 0
	for _, p := range projects {
		running += len(p.Runs)
	}
	pending, err := b.c.Store.PendingCounts(ctx, b.company)
	if err != nil {
		return "", err
	}
	nodes, err := b.c.Store.Nodes(ctx, b.company)
	if err != nil {
		return "", err
	}
	offline := 0
	for _, n := range nodes {
		if !n.Online {
			offline++
		}
	}
	costs, err := b.c.Store.Costs(ctx, b.company, storage.CostFilter{Since: since.Unix()})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Daily digest %s\ndone: %d\nin progress: %d runs\nwaiting for you: %d\nfailures and retries: %d (escalations %d)\nnodes: %d, %d offline\ncost of the day: $%.2f over %d runs (%d without a price)",
		now.Format("2006-01-02"), counts["done"], running, pending.Total, counts["retries"], counts["escalations"], len(nodes), offline, costs.Total.CostUSD, costs.Total.Runs, costs.Total.Sources["none"]), nil
}

func (b telegramBackend) Applied(ctx context.Context, update int64) (bool, error) {
	return b.c.Store.NotifyApplied(ctx, b.company, telegramChannel, update)
}

func (b telegramBackend) MarkApplied(ctx context.Context, update int64) error {
	return b.c.Store.MarkNotifyApplied(ctx, b.company, telegramChannel, update)
}
