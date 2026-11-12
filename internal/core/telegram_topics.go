package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/messaging"
	"appfactory/internal/notify/telegram"
	"appfactory/internal/storage"
)

// topicItem is how an item of a people topic reads in Telegram (people-topics D10): the topic's brief when the item
// opened it, else the topic's title and the item's note; its images go as an album before it, its buttons are its
// actions. An approval is approved with its button and rejected by replying with the reason.
func (b telegramBackend) topicItem(ctx context.Context, f storage.FeedItem) telegram.Item {
	it := telegram.Item{Seq: f.Sequence}
	v, err := b.c.Store.PeopleTopic(ctx, b.company, f.EntityID)
	if err != nil {
		return it
	}
	idx := -1
	for i, x := range v.Items {
		if x.Ref == f.Ref {
			idx = i
		}
	}
	if idx < 0 || !v.Items[idx].Waits {
		return it // decided or replaced before the bot came to it
	}
	x := v.Items[idx]
	msgID := x.Ref
	switch {
	case x.Approval != nil:
		msgID = x.Approval.MessageID
	case x.Decision != nil:
		msgID = x.Decision.MessageID
	}
	var sb strings.Builder
	title := v.Brief.Title
	if title == "" && x.Message != nil {
		title = shortLine(x.Message.Text)
	}
	label := ""
	if x.BroughtBy != "" && x.BroughtBy != "factory" {
		label = b.label(x.BroughtBy)
	}
	icon := map[string]string{"approval": "✅", "decision": "🗳", "review": "🔍"}[x.Kind]
	if icon == "" {
		icon = "❓"
	}
	fmt.Fprintf(&sb, "%s %s\n%s · 🗂 %s", icon, label, v.ProjectID, title)
	opened := idx == 0 || v.CreatedAt >= x.AddedAt
	if opened {
		if v.Brief.Ask != "" {
			fmt.Fprintf(&sb, "\n\n%s", v.Brief.Ask)
		}
		if v.Brief.Recommendation != "" {
			fmt.Fprintf(&sb, "\n\n👉 %s", v.Brief.Recommendation)
		}
		if v.Brief.After != "" {
			fmt.Fprintf(&sb, "\n\n⏭ %s", v.Brief.After)
		}
		if v.Brief.Scope != "" {
			fmt.Fprintf(&sb, "\n\n✖ %s", v.Brief.Scope)
		}
	}
	if note := strings.TrimSpace(x.Note.What); note != "" {
		fmt.Fprintf(&sb, "\n\n➕ %s", note)
	}
	if ch := strings.TrimSpace(x.Note.Changes); ch != "" {
		fmt.Fprintf(&sb, "\nChanged: %s", ch)
	}
	if x.Message != nil && x.Message.Text != "" && (x.Kind == "question" || v.Brief.Empty()) {
		// A question's own words are what people answer; other items speak through the brief.
		fmt.Fprintf(&sb, "\n\n%s", x.Message.Text)
	}
	if v.Waiting > 1 {
		fmt.Fprintf(&sb, "\n\nWaits for you in this topic: %d (all of them in the App Factory inbox).", v.Waiting)
	}
	it.Text = sb.String()
	it.Target = telegram.Target{Question: msgID, Agent: x.BroughtBy, Project: v.ProjectID, Label: label}
	it.OwnersOnly = true
	switch x.Kind {
	case "approval":
		it.Options = []string{"Approve"}
		it.Text += "\n\nReply to this message to reject it with your reason."
	case "question", "escalation", "decision":
		if len(x.Actions) == 1 && x.Actions[0] == "answer" {
			it.Text += "\n\nReply to this message to answer."
		} else {
			it.Options = x.Actions
		}
		if x.Kind == "decision" && x.Decision != nil {
			it.Options = append([]string{x.Decision.Value}, x.Decision.Alternatives...)
		}
	}
	return it
}

func shortLine(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if r := []rune(s); len(r) > 120 {
		s = string(r[:119]) + "…"
	}
	return s
}

// approval decides an approval from Telegram: approve with the button, reject with a reply as the reason.
func (b telegramBackend) approval(ctx context.Context, m messaging.Message, action, reason, key string) (string, error) {
	a, err := b.c.Store.Approval(ctx, b.company, m.ApprovalID)
	if err != nil {
		return "", err
	}
	if a.Status != "PENDING" {
		why := strings.ToLower(a.Status)
		if a.Status == "SUPERSEDED" {
			why = "replaced by a newer version: decide that one"
		}
		return "This approval is already " + why + ".", nil
	}
	if action == "reject" && strings.TrimSpace(reason) == "" {
		return "", errors.New("a rejection needs its reason: reply with it")
	}
	if action == "approve" {
		if !mayApprove(storage.RoleOwner, a) {
			return "", errors.New("approve this one in the App Factory inbox")
		}
		reason = "approved by owner"
	}
	b.c.approvalMu.Lock()
	defer b.c.approvalMu.Unlock()
	if _, err = b.c.Store.DecideApproval(ctx, b.company, a.ID, approvals.Decision{Version: a.Version, Action: action, Reason: reason, RequestID: key}); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return "This approval changed meanwhile (replaced or decided): see the App Factory inbox.", nil
		}
		return "", err
	}
	if action == "approve" {
		return "Approved.", nil
	}
	return "Rejected: " + reason, nil
}
