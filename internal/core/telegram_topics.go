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
		if x.Approval != nil && x.Approval.Choice() {
			// A choice (artifact-choice-approvals): one button per option, then changes and reject.
			it.Text += "\n\n" + choiceText(*x.Approval)
			it.Options = choiceButtons(*x.Approval)
			break
		}
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
	if a.Choice() && a.Status == "PENDING" {
		return "", errors.New("this is a choice: use its buttons, or reply with the changes you want")
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

// Choice approvals in Telegram (artifact-choice-approvals): the options as lines, a button per option, then
// "Changes…" and "Reject". A reply asks for changes with its text; a reply starting with "reject" rejects all with
// the rest as the reason.
const (
	choiceChanges = "Changes…"
	choiceReject  = "Reject"
)

func choiceText(a approvals.Approval) string {
	var sb strings.Builder
	for i, o := range a.Options {
		mark := "▫️"
		if a.Recommended == i+1 {
			mark = "👉"
		}
		fmt.Fprintf(&sb, "%s %d. %s", mark, i+1, o.Label)
		if o.Note != "" {
			sb.WriteString(" — " + o.Note)
		}
		if o.Dropped != "" {
			fmt.Fprintf(&sb, " (dropped by %s: %s)", o.DroppedBy, o.Dropped)
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\nTap an option to choose it. Reply to this message with the changes you want, or with \"reject: <reason>\" to reject all.")
	return sb.String()
}

func choiceButtons(a approvals.Approval) []string {
	out := make([]string, 0, len(a.Options)+2)
	for _, o := range a.Options {
		out = append(out, o.Label)
	}
	return append(out, choiceChanges, choiceReject)
}

// choice decides a choice approval from Telegram: button choice is an option, "Changes…" or "Reject" (both ask for a
// reply); a reply (choice < 0) asks for changes, or rejects all when it starts with "reject".
func (b telegramBackend) choice(ctx context.Context, a approvals.Approval, choice int, text, key string) (string, error) {
	if a.Status != "PENDING" {
		return "This approval is already " + strings.ToLower(strings.ReplaceAll(a.Status, "_", " ")) + ".", nil
	}
	in := approvals.Decision{Version: a.Version, RequestID: key}
	switch n := len(a.Options); {
	case choice >= 0 && choice < n:
		in.Action, in.Option = approvals.ActionChoose, choice+1
	case choice == n:
		return "Reply to the approval's message with the changes you want.", nil
	case choice == n+1:
		return "Reply to the approval's message with \"reject: <reason>\" to reject all options.", nil
	case choice >= 0:
		return "", fmt.Errorf("choose one of the options")
	default:
		t := strings.TrimSpace(text)
		if rest, ok := cutPrefixFold(t, "reject"); ok {
			in.Action, in.Reason = approvals.ActionReject, strings.TrimSpace(strings.TrimLeft(rest, ":—- "))
			if in.Reason == "" {
				return "", errors.New("rejecting all needs the reason: reply \"reject: <reason>\"")
			}
		} else if t == "" {
			return "", errors.New("reply with the changes you want")
		} else {
			in.Action, in.Reason = approvals.ActionChanges, t
		}
	}
	b.c.approvalMu.Lock()
	defer b.c.approvalMu.Unlock()
	got, err := b.c.Store.DecideApproval(ctx, b.company, a.ID, in)
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			return "This approval changed meanwhile (replaced or decided): see the App Factory inbox.", nil
		}
		return "", err
	}
	switch in.Action {
	case approvals.ActionChoose:
		return "Chosen: " + got.Options[in.Option-1].Label + ".", nil
	case approvals.ActionReject:
		return "Rejected all: " + in.Reason, nil
	}
	return "Changes requested: " + in.Reason, nil
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && strings.EqualFold(s[:len(prefix)], prefix) {
		return s[len(prefix):], true
	}
	return s, false
}
