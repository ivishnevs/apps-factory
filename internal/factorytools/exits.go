package factorytools

import "strings"

// ExitPrefix names the explicit exit tools (docs/explicit-exits-contract.md §2).
const ExitPrefix = "factory_exit_"

// ExitKind is the kind of an exit tool name ("submit" for factory_exit_submit), else "".
func ExitKind(name string) string {
	if !strings.HasPrefix(name, ExitPrefix) {
		return ""
	}
	return strings.TrimPrefix(name, ExitPrefix)
}

// ReplacedByExits are the tools the explicit exits replace in the agent list.
var ReplacedByExits = map[string]bool{"factory_task_submit": true, "factory_task_accept": true, "factory_task_reject": true}

// ExitDefinitions are the explicit exit tools and the manager directive tool. They take the run's current
// task from the run credential; every run ends with exactly one accepted exit.
func ExitDefinitions() []Tool {
	s := func() any { return map[string]any{"type": "string"} }
	arr := map[string]any{"type": "array", "items": s()}
	artifact := map[string]any{"type": "object", "properties": map[string]any{"id": s(), "version": s(), "uri": s()}, "required": []string{"id", "version", "uri"}, "additionalProperties": false}
	out := []Tool{}
	add := func(name, op, description string, props map[string]any, required ...string) {
		if required == nil {
			required = []string{}
		}
		out = append(out, Tool{name, "Factory: " + description, map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, "factory." + op})
	}
	finish := " Call exactly one factory_exit_* tool to end the run, then finish the turn."
	add(ExitPrefix+"submit", "task.exit.submit", "Submit your current task for review: summary, checks run and artifacts (the delivered result: final versions, not drafts or samples). The tree must be clean (commit, discard, or move notes to $FACTORY_SCRATCH)."+finish,
		map[string]any{"summary": s(), "checks": arr, "artifacts": map[string]any{"type": "array", "items": artifact}, "reviewer": s()}, "summary")
	add(ExitPrefix+"handoff", "task.exit.handoff", "Hand the current task to an agent (or yourself, to continue in a later run) with a note. As its reviewer, give verdict approved or changes_requested (with feedback) to return it to the implementer. As the implementer of an approved task whose merge failed (checks, branch protection, conflicts), hand it to yourself with merge_failed (why): it runs again after a backoff, and the third failed merge in a row escalates."+finish,
		map[string]any{"to": s(), "note": s(), "verdict": map[string]any{"type": "string", "enum": []string{"approved", "changes_requested"}}, "feedback": s(), "merge_failed": s()}, "to", "note")
	add(ExitPrefix+"done", "task.exit.done", "Finish the current task. Code tasks need their pull request merged first (rebase on the default branch, resolve conflicts, build, merge). Name the delivered result in artifacts (the final versions, not drafts or samples): the owner sees it first on the task."+finish,
		map[string]any{"summary": s(), "artifacts": map[string]any{"type": "array", "items": artifact}}, "summary")
	add(ExitPrefix+"ask", "task.exit.ask", "Stop and ask the owner a question the task cannot continue without; the task resumes with the answer. Choices go in options (one button each; free text stays possible): a question that lists choices without options is refused (options: [] for none)."+finish,
		map[string]any{"question": s(), "options": arr, "category": s(), "evidence": s()}, "question")
	add(ExitPrefix+"block", "task.exit.block", "Stop until other tasks of this project are done (on) or until this task's open children close (on_children)."+finish,
		map[string]any{"on": arr, "on_children": map[string]any{"type": "boolean"}, "reason": s()}, "reason")
	add(ExitPrefix+"wait", "task.exit.wait", "Stop until an event or a time. Waiting for a pending decision of this project: give decision_id; you run again only once it is recorded (approved, rejected) or withdrawn, with the outcome in your brief — never poll a decision with a timed wait. Waiting for a message from an agent or the owner (your manager's \"your turn\", promised details): give from (agent name or owner) and optionally topic; the next message from them to this task or to you in this project wakes you with it in your brief. An event wait has no timer; its optional deadline (RFC 3339, at most 14 days ahead) only escalates. Waiting for the outside world (a build being processed, an external review): wake_at (RFC 3339) or after (e.g. 30m, 2h), at least the minimum wait (15 minutes unless policy says otherwise), and a deadline (required, at most 14 days ahead) after which your manager decides; you run again at the wake time with wait_ended in your brief: check the outside world yourself and continue or wait again; consecutive waits cannot pass the first deadline."+finish,
		map[string]any{"reason": s(), "decision_id": s(), "from": s(), "topic": s(), "wake_at": s(), "after": s(), "deadline": s()}, "reason")
	// owner-comments-handled: every exit carries one outcome per board comment shown to the run.
	outcome := map[string]any{"type": "object", "properties": map[string]any{
		"comment_id": s(),
		"outcome":    map[string]any{"type": "string", "enum": []string{"done", "declined"}},
		"reason":     s(),
		"task_id":    s(),
	}, "required": []string{"comment_id", "outcome"}, "additionalProperties": false}
	for i := range out {
		props := out[i].InputSchema["properties"].(map[string]any)
		props["comments"] = map[string]any{"type": "array", "items": outcome, "description": "One outcome per board comment your brief lists: done (you did what it asks; a manager or the lead names task_id, the task created or changed for it) or declined with a reason the owner reads. A comment that came with an approval decision needs reason on done too: what you changed for it (the owner reads it on the approval). Every exit is refused while one has none."}
	}
	add("factory_comment_forward", "comment.forward", "Forward a board (owner) comment of your current task that is not yours to do (another role, a release, money) to your manager: it gets the comment verbatim with the task link and must handle it in turn; records your outcome forwarded. The project lead cannot forward: it acts through a task or declines.",
		map[string]any{"comment_id": s(), "note": s()}, "comment_id", "note")
	add("factory_task_direct", "task.direct", "Give a directive to a task of this project (managers). An urgent directive stops its running run; the next run starts with it.",
		map[string]any{"task_id": s(), "text": s(), "urgent": map[string]any{"type": "boolean"}, "request_id": s()}, "task_id", "text", "request_id")
	return out
}
