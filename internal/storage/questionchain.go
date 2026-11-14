package storage

import (
	"context"
	"fmt"

	"appfactory/internal/messaging"
)

// Question chains (question-chain-limit): the guard against two agents asking each other in a loop counts the
// questions between one pair of agents about one task in a recent window, and refuses the one past the limit.

const (
	questionChainLimit  = 8
	questionChainWindow = 24 * 60 * 60
	// oldChainLimitReason is the escalation reason of the lifetime limit this replaced; such questions stay answerable by
	// their recipient (D3).
	oldChainLimitReason = "automatic clarification limit (8) reached"
)

// checkQuestionChain refuses m with question_limit when it would be the ninth question between its sender and
// recipient about its task (or, without a task, in its conversation) within the window and since the owner last wrote
// on the task. Questions to or from people and Factory's own questions are not counted (D1).
func checkQuestionChain(ctx context.Context, tx *Tx, m messaging.Message) error {
	if !waitsOnAgent(m.Sender) || !waitsOnAgent(m.Recipient) {
		return nil
	}
	since := tx.now().Unix() - questionChainWindow
	scope, ref := "conversation_id=?", m.ConversationID
	if m.TaskID != "" {
		scope, ref = "task_id=?", m.TaskID
		var owner int64
		if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(created_at),0) FROM messages WHERE company_id=? AND task_id=? AND sender='owner'", tx.company, m.TaskID).Scan(&owner); err != nil {
			return err
		}
		since = max(since, owner)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE company_id=? AND `+scope+` AND message_type='question' AND created_at>=?
 AND ((sender=? AND recipient=?) OR (sender=? AND recipient=?))`, tx.company, ref, since, m.Sender, m.Recipient, m.Recipient, m.Sender).Scan(&count); err != nil {
		return err
	}
	if count < questionChainLimit {
		return nil
	}
	about := "this conversation"
	if m.TaskID != "" {
		about = "task " + m.TaskID
	}
	return refuse("question_limit", fmt.Sprintf("you and %s have exchanged %d questions about %s in the last 24 hours (since the owner last wrote on it), "+
		"which looks like a loop; nothing was sent. Decide what is within your authority and continue, put everything you still need from %s into one message "+
		"(factory_message_send, not a question), or ask your manager or the owner (factory_exit_ask).", m.Recipient, count, about, m.Recipient))
}
