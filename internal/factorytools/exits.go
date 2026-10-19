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
	add(ExitPrefix+"submit", "task.exit.submit", "Submit your current task for review: summary, checks run and artifacts. The tree must be clean (commit, discard, or move notes to $FACTORY_SCRATCH)."+finish,
		map[string]any{"summary": s(), "checks": arr, "artifacts": map[string]any{"type": "array", "items": artifact}, "reviewer": s()}, "summary")
	add(ExitPrefix+"handoff", "task.exit.handoff", "Hand the current task to an agent (or yourself, to continue in a later run) with a note. As its reviewer, give verdict approved or changes_requested (with feedback) to return it to the implementer. As the implementer of an approved task whose merge failed (checks, branch protection, conflicts), hand it to yourself with merge_failed (why): it runs again after a backoff, and the third failed merge in a row escalates."+finish,
		map[string]any{"to": s(), "note": s(), "verdict": map[string]any{"type": "string", "enum": []string{"approved", "changes_requested"}}, "feedback": s(), "merge_failed": s()}, "to", "note")
	add(ExitPrefix+"done", "task.exit.done", "Finish the current task. Code tasks need their pull request merged first (rebase on the default branch, resolve conflicts, build, merge)."+finish,
		map[string]any{"summary": s()}, "summary")
	add(ExitPrefix+"ask", "task.exit.ask", "Stop and ask the owner a question the task cannot continue without; the task resumes with the answer."+finish,
		map[string]any{"question": s(), "options": arr, "category": s(), "evidence": s()}, "question")
	add(ExitPrefix+"block", "task.exit.block", "Stop until other tasks of this project are done (on) or until this task's open children close (on_children)."+finish,
		map[string]any{"on": arr, "on_children": map[string]any{"type": "boolean"}, "reason": s()}, "reason")
	add(ExitPrefix+"wait", "task.exit.wait", "Stop until a time (RFC 3339 wake_at), for example to wait for an external system."+finish,
		map[string]any{"reason": s(), "wake_at": s(), "deadline": s()}, "reason", "wake_at")
	add("factory_task_direct", "task.direct", "Give a directive to a task of this project (managers). An urgent directive stops its running run; the next run starts with it.",
		map[string]any{"task_id": s(), "text": s(), "urgent": map[string]any{"type": "boolean"}, "request_id": s()}, "task_id", "text", "request_id")
	return out
}
