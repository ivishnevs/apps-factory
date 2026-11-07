package storage

import (
	"context"
	"strings"
	"testing"

	"appfactory/internal/approvals"
	"appfactory/internal/decisions"
	"appfactory/internal/factorytools"
	"appfactory/internal/messaging"
)

// TestDecisionInChatAndInbox (decision-approval-ux 2.1): a lead's proposal with alternatives lands in the owner's chat
// with the lead and in the Inbox; the owner picks an alternative; the proposal leaves the Inbox, the lead is told in
// the chat, and its own pending request to record the decision is closed.
func TestDecisionInChatAndInbox(t *testing.T) {
	s := engineStore(t)
	ctx := context.Background()
	co := DefaultCompany
	chat, err := s.CreateConversation(ctx, co, messaging.ConversationCreate{ID: "owner-demo-p", ProjectID: "p", Agent: "demo", Title: "Owner and demo", RequestID: "chat"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.ProposeDecision(ctx, co, "p", decisions.Propose{Topic: "class names", Value: "Warden", Alternatives: []string{"Sentinel", "Keeper"}, Rationale: "fits the setting", Actor: "demo", RequestID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	notice, err := s.Message(ctx, co, d.MessageID)
	if err != nil || notice.ConversationID != chat.ID || !strings.Contains(notice.Body, "Alternative 2: Keeper") {
		t.Fatalf("notice %+v %v", notice, err)
	}
	inbox, _ := s.Inbox(ctx, co, "")
	if len(inbox) != 1 || inbox[0].DecisionID != d.ID {
		t.Fatalf("inbox %+v", inbox)
	}
	// The lead also asked for approval of recording it (the old two-step flow).
	ap, err := s.RequestApproval(ctx, co, approvals.Request{ProjectID: "p", Agent: "demo", Call: factorytools.Call{Name: "factory_decision_record", Arguments: map[string]any{"decision_id": d.ID, "status": "approved"}}, Reason: "approve it", RequestID: "ap"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RecordDecision(ctx, co, d.ID, decisions.Record{Version: d.Version, Status: "approved", Value: "Keeper", Rationale: "Keeper reads better", Actor: "owner", RequestID: "rec"}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Decision(ctx, co, d.ID)
	if got.Status != "approved" || got.Value != "Keeper" {
		t.Fatalf("%+v", got)
	}
	if inbox, _ = s.Inbox(ctx, co, ""); len(inbox) != 0 {
		t.Fatalf("decided items stay in the inbox: %+v", inbox)
	}
	msgs, _ := s.Messages(ctx, co, chat.ID)
	told := false
	for _, m := range msgs {
		if m.Recipient == "demo" && m.DecisionID == d.ID && strings.Contains(m.Body, "approved") && strings.Contains(m.Body, "Keeper") {
			told = true
		}
	}
	if !told {
		t.Fatalf("lead not told in the chat: %+v", msgs)
	}
	if a, _ := s.Approval(ctx, co, ap.ID); a.Status != "REVOKED" {
		t.Fatalf("duplicate approval kept: %s", a.Status)
	}
}
