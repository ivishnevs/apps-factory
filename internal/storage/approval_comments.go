package storage

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"appfactory/internal/approvals"
	"appfactory/internal/config"
	"appfactory/internal/tasks"
)

// Approval comments handled (approval-comments-handled). The board also comments through its approval
// decisions ("approved, but make the phone bigger"). A decision the owner makes with a reason other than the
// default is recorded as a board comment on the approval's task, linked to the approval, and the
// owner-comments-handled rules apply to it as they are: the next brief lists it, every exit is refused until it
// has an outcome, the project lead gets the quiet copy. A done outcome of such a comment says what was changed,
// and the approval shows it wherever it is read.

// defaultDecisionReasons are approval reasons that carry no word beyond the decision itself: the default the web
// and the TUI send ("approved by owner") and bare acknowledgements. A rejection's reason always counts.
var defaultDecisionReasons = map[string]bool{
	"approved by owner": true, "approved": true, "approve": true, "ok": true, "okay": true, "lgtm": true, "yes": true,
	"ок": true, "да": true, "одобрено": true,
}

// DecisionComment is whether the owner's decision reason is a comment on the work.
func DecisionComment(action, reason string) bool {
	r := strings.ToLower(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(reason), ".!")))
	if r == "" {
		return false
	}
	return action == "reject" || action == approvals.ActionChanges || !defaultDecisionReasons[r]
}

// approvalComment records the owner's decision reason as a board comment on the approval's task and sets
// a.CommentID. An open task's runs owe its outcome (boardComment); a closed task runs no more, so the project
// lead gets it on its conversation (reason closed). Without a task, or on a conversation task (the owner's chat
// is messages there), the reason stays only on the decision.
func approvalComment(ctx context.Context, tx *Tx, a *approvals.Approval, action, reason string) error {
	if a.TaskID == "" || !DecisionComment(action, reason) {
		return nil
	}
	t, err := taskTx(ctx, tx, a.TaskID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	if t.Kind == tasks.KindConversation {
		return nil
	}
	category := config.LockedCategoryOf(reason)
	cid, err := commentWith(ctx, tx, t.ID, "owner", reason, category)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE task_comments SET approval_id=? WHERE company_id=? AND id=?", a.ID, tx.company, cid); err != nil {
		return err
	}
	a.CommentID = cid
	what := approvalWhat(*a) + " " + strings.ToLower(strings.ReplaceAll(a.Status, "_", " "))
	if t.Status == "DONE" || t.Status == "CANCELLED" {
		return giveComment(ctx, tx, cid, leadOf(ctx, tx, t.ProjectID), "closed", "the owner's comment on "+what+" of task "+t.ID+", which is "+t.Status+": act on it through a task or decline it")
	}
	return boardCommentNote(ctx, tx, t, cid, category, "on "+what)
}

// approvalWhat names an approval for people and agents.
func approvalWhat(a approvals.Approval) string {
	switch {
	case a.Choice():
		return "artifact choice approval " + a.ID
	case a.Kind == "artifact" && a.Artifact != nil:
		return "artifact approval " + a.ID + " of " + a.Artifact.ID + "@" + a.Artifact.Version
	case a.Kind == "command":
		return "command approval " + a.ID
	case a.Call.Name != "":
		return "approval " + a.ID + " of " + a.Call.Name
	}
	return "approval " + a.ID
}

// approvalCommentState fills a.Comment from the comment's outcomes.
func approvalCommentState(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, company string, a *approvals.Approval) error {
	a.Comment = nil
	if a.CommentID == "" {
		return nil
	}
	outcomes, err := commentOutcomes(ctx, q, company, a.CommentID)
	if err != nil {
		return err
	}
	a.Comment = &approvals.CommentState{ID: a.CommentID, Status: commentStatus(outcomes), Outcome: DescribeOutcomes(outcomes)}
	return nil
}

// commentStatus is a comment's handling in one word: done when anyone did it; declined when it was declined and
// nobody still has it; forwarded while it is on its way up the chain; open (not handled yet) otherwise.
func commentStatus(outcomes []CommentOutcome) string {
	open, declined, forwarded := false, false, false
	for _, o := range outcomes {
		switch o.Status {
		case "done":
			return "done"
		case "open":
			open = true
		case "declined":
			declined = true
		case "forwarded":
			forwarded = true
		}
	}
	switch {
	case declined && !open:
		return "declined"
	case forwarded:
		return "forwarded"
	}
	return "open"
}
