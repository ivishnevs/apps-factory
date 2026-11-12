package storage

import (
	"context"
	"strings"

	"appfactory/internal/messaging"
	"appfactory/internal/tasks"
)

// Promised details (inbox-badge-parity): the owner answers a question with "I'll send details". The question stays
// open in the Inbox as details promised (no answer deadline escalates it, no auto-forward moves it), and a blocking
// question's task leaves its ask park for a park on the owner's next message (decision-waits): no timer reruns it.
// The details close it: an answer to the question, or any owner message that wakes the task. Withdrawing the promise
// answers it with "continue without them"; authority input that overrides the wait supersedes it.

// promiseDetailsTx records the owner's promise on the open question m.
func promiseDetailsTx(ctx context.Context, tx *Tx, m messaging.Message, in messaging.Reply) (messaging.Message, error) {
	switch {
	case in.Sender != "owner":
		return m, inputError("only the owner promises details")
	case m.ApprovalID != "":
		return m, inputError("an approval is decided, not answered later")
	case m.Details == messaging.DetailsPromised:
		return m, ErrConflict
	case in.RequestID == "" || len(in.Body) > 4096:
		return m, inputError("request_id and a note up to 4 KiB required")
	}
	m.Details, m.DetailsNote, m.DetailsAt = messaging.DetailsPromised, strings.TrimSpace(in.Body), tx.now().Unix()
	if m.DeliveryStatus == "PENDING" {
		m.DeliveryStatus = "NONE" // the owner took it over: its recipient agent is not asked any more
	}
	if err := saveMessage(ctx, tx, &m, "message.details_promised"); err != nil {
		return m, err
	}
	if !m.Blocking || m.TaskID == "" {
		return m, nil // nothing waits on the answer: the question only stays open
	}
	t, err := taskTx(ctx, tx, m.TaskID)
	if err != nil || t.Status == "DONE" || t.Status == "CANCELLED" {
		return m, err
	}
	if err = clearParks(ctx, tx, t, tasks.ParkAsk, m.ID, "cleared"); err != nil {
		return m, err
	}
	reason := "the owner promised the details"
	if m.DetailsNote != "" {
		reason += " (" + shortText(m.DetailsNote, 512) + ")"
	}
	return m, parkOnMessageTx(ctx, tx, t, "owner", "the details for question "+m.ID, reason, 0)
}

// settlePromisedDetails closes the task's questions with promised details: sent when an owner message reached the
// waiting task, withdrawn (superseded, why) when authority input overrode its wait.
func settlePromisedDetails(ctx context.Context, tx *Tx, task, details, why string) error {
	qs, err := listDocs[messaging.Message](ctx, tx, "SELECT body FROM messages WHERE company_id=? AND task_id=? AND message_type='question' AND status IN ('PENDING','ESCALATED') AND (body->>'details')=? ORDER BY seq", tx.company, task, messaging.DetailsPromised)
	if err != nil {
		return err
	}
	for _, m := range qs {
		m.Details = details
		if details == messaging.DetailsSent {
			m.Status = "ANSWERED"
		} else {
			m.Status, m.EscalationReason = "WITHDRAWN", why
		}
		if m.DeliveryStatus == "PENDING" {
			m.DeliveryStatus = "NONE"
		}
		if err = saveMessage(ctx, tx, &m, "message.details_"+details); err != nil {
			return err
		}
	}
	return nil
}
