// Package factorytools defines the shared, bounded Factory MCP contract.
package factorytools

import "fmt"

type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Operation   string         `json:"-"`
}
type Call struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

func Definitions() []Tool {
	s := func() any { return map[string]any{"type": "string"} }
	n := map[string]any{"type": "integer"}
	arr := map[string]any{"type": "array", "items": s()}
	out := []Tool{}
	add := func(name, op, description string, props map[string]any, required ...string) {
		if required == nil {
			required = []string{}
		}
		out = append(out, Tool{"factory_" + name, "Factory: " + description, map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, "factory." + op})
	}
	add("project_get", "project.get", "Read the current project (identity comes from the run).", map[string]any{})
	add("settings_get", "settings.get", "Read the company or project rules (conventions, standing facts, fixed decisions); the brief carries them too.", map[string]any{"scope": map[string]any{"type": "string", "enum": []string{"company", "project"}}}, "scope")
	add("agent_list", "agent.list", "List available agent names, roles and task capabilities.", map[string]any{})
	add("task_get", "task.get", "Read a task in this project, including dependencies and submission.", map[string]any{"id": s()}, "id")
	add("task_list", "task.list", "List tasks in this project, optionally filtered by status.", map[string]any{"status": s()})
	add("task_create", "task.create", "Create a BACKLOG task. Use a stable request_id for retries; make ready separately.", map[string]any{"title": s(), "description": s(), "priority": n, "assignee": s(), "parent_task_id": s(), "openspec_change": s(), "dependencies": arr, "request_id": s()}, "title", "request_id")
	add("task_update", "task.update", "Update task metadata/dependencies with its current version. model (authority only) overrides the assignee's model for this task; it must fit the assignee's runtime provider, empty clears it.", map[string]any{"id": s(), "version": n, "title": s(), "description": s(), "priority": n, "openspec_change": s(), "dependencies": arr, "model": s(), "request_id": s()}, "id", "version", "request_id")
	add("task_assign", "task.assign", "Assign a task to an agent with its current version.", map[string]any{"id": s(), "version": n, "assignee": s(), "request_id": s()}, "id", "version", "assignee", "request_id")
	for _, action := range []string{"ready", "block", "cancel"} {
		add("task_"+action, "task."+action, "Request task action "+action+" with its current version.", map[string]any{"id": s(), "version": n, "reason": s(), "request_id": s()}, "id", "version", "request_id")
	}
	artifact := map[string]any{"type": "object", "properties": map[string]any{"id": s(), "version": s(), "uri": s()}, "required": []string{"id", "version", "uri"}, "additionalProperties": false}
	submission := map[string]any{"type": "object", "properties": map[string]any{"summary": s(), "commit": s(), "checks": arr, "artifacts": map[string]any{"type": "array", "items": artifact}}, "required": []string{"summary"}, "additionalProperties": false}
	add("task_submit", "task.submit", "Save structured submission for your current task. Review becomes available only after the runtime finishes successfully; this does not release the writer.", map[string]any{"id": s(), "version": n, "submission": submission, "request_id": s()}, "id", "version", "submission", "request_id")
	for _, action := range []string{"accept", "reject"} {
		add("task_"+action, "task.review", "Review another agent's IN_REVIEW task: "+action+". Rejection requires feedback.", map[string]any{"id": s(), "version": n, "feedback": s(), "request_id": s()}, "id", "version", "request_id")
	}
	add("skills_list", "skills.list", "List skill metadata available to this agent.", map[string]any{})
	add("skills_search", "skills.search", "Search available skill metadata.", map[string]any{"q": s(), "category": s()})
	add("skills_read", "skills.read", "Read SKILL.md or a confined skill resource lazily.", map[string]any{"skill": s(), "resource": s()}, "skill")
	question := map[string]any{"type": "object", "properties": map[string]any{"id": s(), "type": s(), "question": s(), "options": arr}, "required": []string{"id", "type", "question"}, "additionalProperties": false}
	messageProps := func() map[string]any {
		return map[string]any{"recipient": s(), "task_id": s(), "conversation_id": s(), "body": s(), "blocking": map[string]any{"type": "boolean"}, "questions": map[string]any{"type": "array", "items": question}, "timeout_seconds": n, "request_id": s()}
	}
	sendProps := map[string]any{"recipient": s(), "task_id": s(), "conversation_id": s(), "body": s(), "message_type": s(), "request_id": s()}
	add("message_send", "message.send", "Send an asynchronous notification/review request/escalation. Core schedules the recipient after this project's writer is released.", sendProps, "recipient", "body", "message_type", "request_id")
	add("question_forward", "question.forward", "Forward a question you cannot decide (its category is not delegated to you) to your manager, with a note; the asker keeps waiting.", map[string]any{"message_id": s(), "version": n, "note": s(), "request_id": s()}, "message_id", "version", "request_id")
	add("message_ask", "message.ask", "Ask another agent. A blocking question must concern your current task. After asking, finish the turn immediately; Core resumes the task after an explicit answer, never by polling inside a turn.", messageProps(), "recipient", "body", "request_id")
	ownerProps := messageProps()
	delete(ownerProps, "recipient")
	add("owner_ask", "owner.ask", "Ask the owner via durable inbox, optionally with structured questions. Finish the turn after a blocking question; do not wait or poll.", ownerProps, "body", "request_id")
	add("message_get", "message.get", "Read a message in this project.", map[string]any{"message_id": s()}, "message_id")
	add("message_list", "message.list", "Read the current or specified conversation's messages.", map[string]any{"conversation_id": s()})
	add("message_reply", "message.reply", "Explicitly answer a pending question addressed to you, with its latest version. Answer all structured question IDs. A final-text reply alone is not an answer.", map[string]any{"message_id": s(), "version": n, "body": s(), "answers": map[string]any{"type": "object", "additionalProperties": true}, "request_id": s()}, "message_id", "version", "body", "request_id")

	callSchema := map[string]any{"type": "object", "properties": map[string]any{"name": s(), "arguments": map[string]any{"type": "object", "additionalProperties": true}}, "required": []string{"name", "arguments"}, "additionalProperties": false}
	add("owner_request_approval", "owner.request_approval", "Request owner approval for an exact supported mutation call (including request_id). Deny policies cannot be overridden. Blocking requests require your current task: finish the turn, then resume after decision; rejection is not permission.", map[string]any{"call": callSchema, "reason": s(), "blocking": map[string]any{"type": "boolean"}, "timeout_seconds": n, "request_id": s()}, "call", "reason", "request_id")
	add("approval_get", "approval.get", "Read your scoped approval and exact approved call. Never poll inside a turn.", map[string]any{"approval_id": s()}, "approval_id")

	for i := range out {
		if out[i].Name == "factory_task_create" || out[i].Name == "factory_task_update" {
			out[i].InputSchema["properties"].(map[string]any)["required_artifacts"] = map[string]any{"type": "array", "items": artifact}
		}
	}
	add("artifact_register", "artifact.register", "Register an immutable artifact id/version/URI produced by your current task. Changing URI requires a new version; this does not approve or accept work.", map[string]any{"artifact": artifact, "request_id": s()}, "artifact", "request_id")
	add("artifact_list", "artifact.list", "List registered artifact versions in this project.", map[string]any{})
	add("artifact_get", "artifact.get", "Read an exact artifact version and its latest owner approval decision.", map[string]any{"artifact_id": s(), "artifact_version": s()}, "artifact_id", "artifact_version")
	add("artifact_request_approval", "artifact.request_approval", "Ask owner to approve a registered artifact version. This approves content, not task acceptance or tool permission. Usually leave blocking false and finish the producing task; downstream tasks can use required_artifacts. If blocking true, finish the turn immediately.", map[string]any{"artifact_id": s(), "artifact_version": s(), "reason": s(), "blocking": map[string]any{"type": "boolean"}, "timeout_seconds": n, "request_id": s()}, "artifact_id", "artifact_version", "reason", "request_id")

	add("report_create", "report.create", "Persist a significant-action report: what, why, changes, result and any owner action. Identity/task/run come from this run; reports do not block execution or grant approval.", map[string]any{"kind": s(), "summary": s(), "purpose": s(), "changes": arr, "result": s(), "artifacts": map[string]any{"type": "array", "items": artifact}, "needs_owner_action": map[string]any{"type": "boolean"}, "owner_action": s(), "request_id": s()}, "kind", "summary", "purpose", "changes", "result", "request_id")
	add("report_get", "report.get", "Read a full project report.", map[string]any{"report_id": s()}, "report_id")
	add("report_list", "report.list", "List report summaries. after is next_after from the previous page; limit 1..20. Read full details with report_get.", map[string]any{"kind": s(), "task_id": s(), "run_id": s(), "after": n, "limit": n})
	add("decision_propose", "decision.propose", "Propose a significant project decision with rationale and notify owner. This does not approve it or stop normal development; use owner questions separately when blocked.", map[string]any{"category": s(), "topic": s(), "value": s(), "rationale": s(), "artifacts": map[string]any{"type": "array", "items": artifact}, "request_id": s()}, "topic", "value", "rationale", "request_id")
	add("decision_decide", "decision.decide", "Atomically approve a significant decision only in an explicitly policy-delegated category. No owner notification or approval is implied. Cannot bypass owner review/rejection or replace an owner decision. Replacement of a policy decision requires its exact ID/version.", map[string]any{"category": s(), "topic": s(), "value": s(), "rationale": s(), "artifacts": map[string]any{"type": "array", "items": artifact}, "supersedes_id": s(), "supersedes_version": n, "request_id": s()}, "category", "topic", "value", "rationale", "request_id")

	add("decision_get", "decision.get", "Read the full versioned decision, rationale and replacement history links.", map[string]any{"decision_id": s()}, "decision_id")
	add("decision_list", "decision.list", "Read decision summaries before asking owner again. Defaults to approved; status may be proposed, under_discussion, approved, superseded, rejected or empty for all. after/limit paginate (1..20).", map[string]any{"status": s(), "topic": s(), "after": n, "limit": n})
	add("decision_record", "decision.record", "Record explicit owner authorization on an existing proposal (under_discussion, approved or rejected). Always requires exact action approval. Replacing an approved topic requires supersedes_id and supersedes_version; never infer approval from chat.", map[string]any{"decision_id": s(), "version": n, "status": s(), "rationale": s(), "supersedes_id": s(), "supersedes_version": n, "request_id": s()}, "decision_id", "version", "status", "rationale", "request_id")
	add("project_context", "project.context", "Read current project and bounded approved decision context. Read full details through decision_get/list.", map[string]any{})
	add("feed_list", "feed.list", "Read the event feed of your subtree (you, the agents reporting to you and their reports): exits, escalations, run outcomes and retries, reports, missing reports. Optional task and attention filters; after/limit paginate (1..50).", map[string]any{"task": s(), "attention": map[string]any{"type": "boolean"}, "after": n, "limit": n})
	add("project_feed", "project.feed", "Read the project event feed: stable event pointers with short summaries, not full logs. after/limit paginate (1..20).", map[string]any{"after": n, "limit": n})

	add("openspec_init", "openspec.init", "Create missing OpenSpec directories without overwriting documents or config. Requires the project's writer slot or your active run. Author and archive changes with the official OpenSpec workflow.", map[string]any{"request_id": s()}, "request_id")
	add("openspec_list", "openspec.list", "List a project OpenSpec directory lazily; dir is relative to openspec (default .). after is previous next_after; limit 1..20.", map[string]any{"dir": s(), "after": s(), "limit": n})
	add("openspec_read", "openspec.read", "Read a current OpenSpec document relative to openspec, up to 8192 UTF-8 bytes. Continue using next_offset and the returned sha256; changed content conflicts. Documents remain in workspace files.", map[string]any{"path": s(), "offset": n, "limit": n, "sha256": s()}, "path")
	add("openspec_check", "openspec.check", "Check an active change reference and nonempty proposal. This is not semantic/schema validation; use official OpenSpec tools for that.", map[string]any{"change": s()}, "change")
	return out
}

// Validate checks required fields, unknown keys and nested JSON types before routing.
func Validate(t Tool, args map[string]any) error { return validate(t.InputSchema, args) }
func validate(schema map[string]any, value any) error {
	switch schema["type"] {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("object required")
		}
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]string); ok {
			for _, k := range req {
				if _, ok := obj[k]; !ok {
					return fmt.Errorf("%s required", k)
				}
			}
		}
		for k, v := range obj {
			p, ok := props[k]
			if !ok {
				if schema["additionalProperties"] == true {
					continue
				}
				return fmt.Errorf("unknown argument %s", k)
			}
			if err := validate(p.(map[string]any), v); err != nil {
				return fmt.Errorf("%s: %w", k, err)
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("boolean required")
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("string required")
		}
	case "integer":
		v, ok := value.(float64)
		if !ok || v != float64(int(v)) {
			return fmt.Errorf("integer required")
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			return fmt.Errorf("array required")
		}
		for _, v := range a {
			if err := validate(schema["items"].(map[string]any), v); err != nil {
				return err
			}
		}
	}
	return nil
}
