// Package factorytools defines the shared, bounded Factory MCP contract.
package factorytools

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

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

// attachHelp says how a message shows what it is about (inbox-visual-previews).
const attachHelp = "Anything visual you ask or report about (screenshots, art, a design, a comparison) goes along as attachments: files of your worktree [{path, caption}] in the order to show them (several images with captions show side by side, e.g. classic | v1 | v2; the node uploads them, credentials redacted) or materials: existing ones [{artifact_id, artifact_version} or {report_id, path}, caption]. A path in the text is not enough: people answer where they read it, without opening the repository (a message to people naming .png/.jpg/.webp/.gif/.mp4 files without attachments answers with warning visual_without_attachment; send it again with them)."

func Definitions() []Tool {
	s := func() any { return map[string]any{"type": "string"} }
	n := map[string]any{"type": "integer"}
	arr := map[string]any{"type": "array", "items": s()}
	messageAttachment := map[string]any{"type": "object", "properties": map[string]any{"path": s(), "caption": s(), "sha256": s(), "size": n, "redacted": map[string]any{"type": "boolean"}}, "required": []string{"path"}, "additionalProperties": false}
	materialRef := map[string]any{"type": "object", "properties": map[string]any{"artifact_id": s(), "artifact_version": s(), "report_id": s(), "path": s(), "caption": s()}, "additionalProperties": false}
	out := []Tool{}
	// task-duplicate-warnings: covered work, replacement and the answer's warnings.
	coversSchema := map[string]any{"type": "array", "items": s(), "description": "The work this task covers: <change>#<n.m> (an OpenSpec tasks.md item), <change>#<n> (a group), pr#<number>, msg:<id8> (a review finding or request). Set it on follow-ups and carry-overs; without it Core infers keys from the title and description."}
	resourcesSchema := map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "integer", "minimum": 1}, "description": "Node resources every run of this task needs on top of its agent's, e.g. {\"mac-build\": 1}. Core enforces them at claim: tasks needing an exclusive resource (a node offers one) run one at a time and the others wait with reason resource_busy naming the holder. Use it for exclusive work (one integration gate on the Mac at a time) instead of announcing turns in messages. {} clears them (update)."}
	replacesSchema := map[string]any{"type": "array", "items": s(), "description": "Task ids this task replaces (a split or a narrower re-plan): Core cancels each one you may cancel and moves its dependents to this task (never a task assigned to a person: that is refused); one it does not cancel stays open and warns original_open."}
	duplicateHelp := "The answer carries warnings (never a refusal) when an open task covers the same work (same_work_item), has the same title and assignee (same_title) or is the original still open (original_open); same_change_open lists other open tasks of the assignee and change as context. Answer each strong warning: cancel the new task and update the existing one, or keep both with factory_task_update not_duplicate_of."
	add := func(name, op, description string, props map[string]any, required ...string) {
		if required == nil {
			required = []string{}
		}
		out = append(out, Tool{"factory_" + name, "Factory: " + description, map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}, "factory." + op})
	}
	add("project_get", "project.get", "Read the current project (identity comes from the run).", map[string]any{})
	add("settings_get", "settings.get", "Read the company or project rules (conventions, standing facts, fixed decisions); the brief carries them too.", map[string]any{"scope": map[string]any{"type": "string", "enum": []string{"company", "project"}}}, "scope")
	add("agent_list", "agent.list", "List available agent names, roles and task capabilities.", map[string]any{})
	add("nodes", "node.list", "List the company's nodes (machines that run agents): online or paused, work and conversation slots and how many are busy, labels the owner set and labels the node detected itself (macos or linux, arm64 or amd64, xcode, ios-sim, headless, gpu), usable runtimes and version. An agent's labels select nodes by both kinds; a node marked update_required gets no work.", map[string]any{})
	add("catalog_get", "catalog.read", "Read the company's catalog (agents, profiles, policies, prompts, skills registry, settings): without path the current version and its document list, with path one document's content.", map[string]any{"path": s(), "version": n})
	add("catalog_change", "config.change", "Propose a change of the company's catalog on top of version base: put adds or replaces documents (path → full content), delete removes paths. Always needs the owner's approval of this exact call: request it with factory_owner_request_approval (call = this tool and its arguments), finish the turn, and call again with the same arguments once APPROVED. The whole catalog is validated before it is saved; a stale base is refused.",
		map[string]any{"base": n, "put": map[string]any{"type": "object", "additionalProperties": true}, "delete": arr, "note": s(), "request_id": s()}, "base", "note", "request_id")
	add("task_get", "task.get", "Read a task in this project, including dependencies and submission.", map[string]any{"id": s()}, "id")
	b := func() any { return map[string]any{"type": "boolean"} }
	add("task_list", "task.list", "List tasks in this project, compact (id, title, status, assignee, openspec_change, covers, dependencies; full: true adds descriptions and the rest). Filters: status, open (not done or cancelled), assignee, openspec_change, covers (a work key such as world-models#7.1, pr#148), query (all words in the title). Before creating a batch of tasks, list the open tasks of their change or assignee once, not once per task.",
		map[string]any{"status": s(), "open": b(), "assignee": s(), "openspec_change": s(), "covers": s(), "query": s(), "full": b()})
	add("task_create", "task.create", "Create a BACKLOG task. Use a stable request_id for retries; make ready separately. project_id is your run's project (default) or a project you created with factory_project_create. review_by is who reviews its work: another agent, owner or member:USER; a task cannot leave the backlog without it (the team's review rules say whom to choose). assignee is an agent, or a person (member:USER) only on their word: assign_reason cites the id of their message (I'll do it myself); do not plan a person's work for an agent. "+duplicateHelp, map[string]any{"title": s(), "description": s(), "priority": n, "assignee": s(), "assign_reason": s(), "parent_task_id": s(), "openspec_change": s(), "kind": map[string]any{"type": "string", "enum": []string{"work", "spec"}, "description": "work (default): implements; its openspec_change must already be merged and is checked before every run. spec: writes or amends its openspec_change (the change need not exist yet; not checked). Required when you set openspec_change."}, "dependencies": arr, "project_id": s(), "review_by": s(), "covers": coversSchema, "replaces": replacesSchema, "resources": resourcesSchema, "request_id": s()}, "title", "request_id")
	add("project_create", "project.create", "Create a new project. Always needs the owner's approval of this exact call: request it with factory_owner_request_approval (call = this tool and its arguments, including request_id) and finish the turn; nothing exists before approval and a rejection leaves nothing. repo_mode local (default: a new git repository on the node), github (a new private GitHub repository under the owner's account) or existing (workspace: an absolute path on the node, and/or repo_url; audit true runs an onboarding audit first). The repository is set up when the project's first task runs. Afterwards create its tasks with factory_task_create and project_id.",
		map[string]any{"project_id": s(), "name": s(), "description": s(), "repo_mode": map[string]any{"type": "string", "enum": []string{"local", "github", "existing"}}, "workspace": s(), "repo_url": s(), "default_branch": s(), "audit": map[string]any{"type": "boolean"}, "request_id": s()}, "project_id", "name", "request_id")
	add("task_update", "task.update", "Update task metadata/dependencies with its current version. To make a task wait for other work, add that task to dependencies (not a block). model (authority only) overrides the assignee's model for this task; it must fit the assignee's runtime provider, empty clears it. review_by sets the reviewer (another agent, owner or member:USER), required before the task is made ready. covers replaces the work keys ([] lets Core infer them again); replaces cancels tasks this one replaces; not_duplicate_of answers a duplicate warning by keeping both tasks (works in any open status, version not needed then).", map[string]any{"id": s(), "version": n, "title": s(), "description": s(), "priority": n, "openspec_change": s(), "kind": map[string]any{"type": "string", "enum": []string{"work", "spec"}, "description": "work (default): implements; its openspec_change must already be merged and is checked before every run. spec: writes or amends its openspec_change (the change need not exist yet; not checked). Required when you set openspec_change."}, "dependencies": arr, "model": s(), "review_by": s(), "covers": coversSchema, "replaces": replacesSchema, "resources": resourcesSchema, "not_duplicate_of": map[string]any{"type": "array", "items": s(), "description": "Task ids that are not duplicates of this one although Core warned: the pair never warns again."}, "request_id": s()}, "id", "version", "request_id")
	add("task_assign", "task.assign", "Assign a task to an agent with its current version, or to a person (member:USER) only on their word: assign_reason cites the id of their message. A task assigned to a person is theirs: agents read it, message about it and depend on it, but do not change its status, assignee or description, cancel it or replace it.", map[string]any{"id": s(), "version": n, "assignee": s(), "assign_reason": s(), "request_id": s()}, "id", "version", "assignee", "request_id")
	actionHelp := map[string]string{
		"ready":  "Request task action ready with its current version.",
		"block":  "Request task action block with its current version: holds the task until the owner releases it. Never block a task to make it wait for other tasks: a task with open dependencies already waits and starts by itself when they are done (a block of such a task is refused); add the work as a dependency instead, and answer a stalled escalation about a planned wait with keep_waiting.",
		"cancel": "Request task action cancel with its current version.",
	}
	for _, action := range []string{"ready", "block", "cancel"} {
		add("task_"+action, "task."+action, actionHelp[action], map[string]any{"id": s(), "version": n, "reason": s(), "request_id": s()}, "id", "version", "request_id")
	}
	artifact := map[string]any{"type": "object", "properties": map[string]any{"id": s(), "version": s(), "uri": s()}, "required": []string{"id", "version", "uri"}, "additionalProperties": false}
	submission := map[string]any{"type": "object", "properties": map[string]any{"summary": s(), "commit": s(), "checks": arr, "artifacts": map[string]any{"type": "array", "items": artifact}}, "required": []string{"summary"}, "additionalProperties": false}
	add("task_submit", "task.submit", "Save structured submission for your current task. Review becomes available only after the runtime finishes successfully; this does not release the writer.", map[string]any{"id": s(), "version": n, "submission": submission, "request_id": s()}, "id", "version", "submission", "request_id")
	for _, action := range []string{"accept", "reject"} {
		add("task_"+action, "task.review", "Review another agent's IN_REVIEW task: "+action+". Rejection requires feedback.", map[string]any{"id": s(), "version": n, "feedback": s(), "request_id": s()}, "id", "version", "request_id")
	}
	question := map[string]any{"type": "object", "properties": map[string]any{"id": s(), "type": s(), "question": s(), "options": arr}, "required": []string{"id", "type", "question"}, "additionalProperties": false}
	// Visual attachments (inbox-visual-previews): files of the worktree ({path, caption}, uploaded by the node) and
	// references to existing materials, shown inline where people answer.
	visuals := func(props map[string]any) map[string]any {
		props["attachments"] = map[string]any{"type": "array", "items": messageAttachment}
		props["materials"] = map[string]any{"type": "array", "items": materialRef}
		return props
	}
	messageProps := func() map[string]any {
		return visuals(map[string]any{"recipient": s(), "task_id": s(), "conversation_id": s(), "body": s(), "blocking": map[string]any{"type": "boolean"}, "questions": map[string]any{"type": "array", "items": question}, "options": arr, "timeout_seconds": n, "request_id": s()})
	}
	sendProps := visuals(map[string]any{"recipient": s(), "task_id": s(), "conversation_id": s(), "body": s(), "message_type": s(), "request_id": s()})
	add("message_send", "message.send", "Send an asynchronous notification/review request/escalation. Core schedules the recipient after this project's writer is released. "+attachHelp, sendProps, "recipient", "body", "message_type", "request_id")
	add("question_forward", "question.forward", "Forward a question you cannot decide (its category is not delegated to you) to your manager, with a note; the asker keeps waiting. The question keeps its attachments; add your own with attachments/materials.", visuals(map[string]any{"message_id": s(), "version": n, "note": s(), "request_id": s()}), "message_id", "version", "request_id")
	add("message_ask", "message.ask", "Ask another agent. When you offer choices, put them in options (one button each; free text stays possible), not only in the body: a body that lists choices without options is refused (options: [] sends it as free text). A blocking question must concern your current task. After asking, finish the turn immediately; Core resumes the task after an explicit answer, never by polling inside a turn. "+attachHelp, messageProps(), "recipient", "body", "request_id")
	ownerProps := messageProps()
	delete(ownerProps, "recipient")
	add("owner_ask", "owner.ask", "Ask the owner (people see it in the chat, on the task and in the Inbox). When you offer choices, put them in options (one button each; they can still write their own answer), not only in the body: a body that lists choices without options is refused (options: [] sends it as free text). questions is a form of several questions instead. Finish the turn after a blocking question; do not wait or poll. "+attachHelp, ownerProps, "body", "request_id")
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
	add("artifact_register", "artifact.register", "Register an immutable artifact id/version/URI produced by your current task. Changing URI requires a new version; this does not approve or accept work.", map[string]any{"artifact": artifact, "includes": includesSchema(), "request_id": s()}, "artifact", "request_id")
	add("artifact_upload", "artifact.register", "Upload a file of your worktree as an immutable artifact version produced by your current task (URI factory://artifacts/<id>/<version>). The owner's approval binds to its content digest; different content needs a new version. sha256/size are filled in by the node.", map[string]any{"artifact_id": s(), "artifact_version": s(), "path": s(), "sha256": s(), "size": n, "includes": includesSchema(), "request_id": s()}, "artifact_id", "artifact_version", "path", "request_id")
	add("artifact_list", "artifact.list", "List registered artifact versions in this project.", map[string]any{})
	add("artifact_get", "artifact.get", "Read an exact artifact version and its latest owner approval decision. A version archived by the project's artifact retention keeps its metadata, its content is gone: the answer's archived note names the latest version to use.", map[string]any{"artifact_id": s(), "artifact_version": s()}, "artifact_id", "artifact_version")
	choiceOption := map[string]any{"type": "object", "properties": map[string]any{"artifact_id": s(), "artifact_version": s(), "label": map[string]any{"type": "string", "description": "Short, as people will say it: \"V1 Mascot\"."}, "note": map[string]any{"type": "string", "description": "Optional, one line: what sets this option apart."}}, "required": []string{"artifact_id", "artifact_version", "label"}, "additionalProperties": false}
	add("artifact_request_approval", "artifact.request_approval", "Ask for approval of a registered artifact version (audience internal for the team's own material such as concepts, references and textures; external, the default, for what leaves the company). Depending on the project's approval setting a teammate (the designer, the lead) or people decide it. This approves content, not task acceptance or tool permission. When people should choose among several versions (variations, alternatives), ask ONE choice approval with options instead of artifact_id/artifact_version, rather than an approval plus a question: the decider chooses one (it is approved, the others are not chosen and stay usable), asks for changes or rejects all. Usually leave blocking false and finish the producing task; downstream tasks can use required_artifacts. If blocking true, finish the turn immediately.",
		map[string]any{"artifact_id": s(), "artifact_version": s(), "options": map[string]any{"type": "array", "items": choiceOption, "description": "2 to 6 registered versions to choose from, each with a label and an optional note (a choice approval; use instead of artifact_id/artifact_version)."}, "recommended": map[string]any{"type": "string", "description": "Your recommended option of a choice: its number (from 1), label or artifact_id@version."}, "reason": s(), "audience": s(), "blocking": map[string]any{"type": "boolean"}, "timeout_seconds": n, "request_id": s()}, "reason", "request_id")
	dropOption := map[string]any{"type": "object", "properties": map[string]any{"option": map[string]any{"type": "string", "description": "The option's number (from 1), label or artifact_id@version."}, "reason": s()}, "required": []string{"option", "reason"}, "additionalProperties": false}
	add("approval_decide", "approval.decide", "Decide an approval that was delegated to you (you got it as a message): approve or reject with a reason, or escalate to hand it to people when unsure, out of your remit or in conflict with a recorded decision. As a team lead deciding from earlier answers, name the owner's earlier answers or recorded decisions you rely on in cites (message or decision ids, or quotes). A choice approval (several options) is never approved without choosing: action choose with choice only when what people said covers it, citing in cites their answers (message ids), recorded decisions (decision ids) or the company's or project's rules (\"project rules: <quote>\", \"company rules: <quote>\"), which Core checks; changes (reason: what to change, options: which) or reject (reason: why none is good enough) need no cite; or escalate, optionally with drop to leave out options not good enough (people still see them, at least one stays).",
		map[string]any{"approval_id": s(), "approval_ids": map[string]any{"type": "array", "items": s(), "description": "Several approvals delegated to you, escalated together with one brief (escalate only); use instead of approval_id."}, "action": map[string]any{"type": "string", "description": "approve, reject or escalate; for a choice approval choose, changes, reject or escalate."}, "reason": s(), "cites": arr,
			"choice": map[string]any{"type": "string", "description": "choose: the option's number (from 1), label or artifact_id@version."}, "options": map[string]any{"type": "array", "items": s(), "description": "changes: the options the changes are about (optional)."}, "drop": map[string]any{"type": "array", "items": dropOption, "description": "escalate a choice: options you leave out, each with its reason."}, "request_id": s()}, "action", "reason", "request_id")

	attachment := map[string]any{"type": "object", "properties": map[string]any{"path": s(), "sha256": s(), "size": n, "redacted": map[string]any{"type": "boolean"}}, "required": []string{"path"}, "additionalProperties": false}
	add("report_create", "report.create", "Persist a significant-action report: what, why, changes, result and any owner action. attachments name files of your worktree ({path}); the node uploads them with known credentials redacted. Identity/task/run come from this run; reports do not block execution or grant approval.", map[string]any{"kind": s(), "summary": s(), "purpose": s(), "changes": arr, "result": s(), "artifacts": map[string]any{"type": "array", "items": artifact}, "attachments": map[string]any{"type": "array", "items": attachment}, "needs_owner_action": map[string]any{"type": "boolean"}, "owner_action": s(), "request_id": s()}, "kind", "summary", "purpose", "changes", "result", "request_id")
	add("report_get", "report.get", "Read a full project report.", map[string]any{"report_id": s()}, "report_id")
	add("report_list", "report.list", "List report summaries. after is next_after from the previous page; limit 1..20. Read full details with report_get.", map[string]any{"kind": s(), "task_id": s(), "run_id": s(), "after": n, "limit": n})
	add("decision_propose", "decision.propose", "Propose a significant project decision with rationale; it reaches the owner in your chat and the Inbox. Put the other options in alternatives (the recommendation is value): the owner approves, picks an alternative, writes their own or rejects, and you get the outcome as a message. Do not also request approval of factory_decision_record for it. Proposing does not stop normal development; ask the owner separately when blocked. "+attachHelp, visuals(map[string]any{"category": s(), "topic": s(), "value": s(), "alternatives": arr, "rationale": s(), "artifacts": map[string]any{"type": "array", "items": artifact}, "request_id": s()}), "topic", "value", "rationale", "request_id")
	add("decision_decide", "decision.decide", "Atomically approve a significant decision in an explicitly policy-delegated category, or where the project's decision setting lets you (as the lead: only what the owner's earlier answers or recorded decisions cover, named in cites; money and destructive changes never). No owner notification or approval is implied. Cannot bypass owner review/rejection or replace an owner decision. Replacement of a policy decision requires its exact ID/version.", map[string]any{"category": s(), "topic": s(), "value": s(), "rationale": s(), "artifacts": map[string]any{"type": "array", "items": artifact}, "supersedes_id": s(), "supersedes_version": n, "cites": arr, "request_id": s()}, "category", "topic", "value", "rationale", "request_id")

	add("decision_get", "decision.get", "Read the full versioned decision, rationale and replacement history links.", map[string]any{"decision_id": s()}, "decision_id")
	add("decision_list", "decision.list", "Read decision summaries before asking owner again. Defaults to approved; status may be proposed, under_discussion, approved, superseded, rejected or empty for all. after/limit paginate (1..20).", map[string]any{"status": s(), "topic": s(), "after": n, "limit": n})
	add("decision_record", "decision.record", "Record explicit owner authorization on an existing proposal (under_discussion, approved or rejected). Always requires exact action approval. Replacing an approved topic requires supersedes_id and supersedes_version; never infer approval from chat.", map[string]any{"decision_id": s(), "version": n, "status": s(), "rationale": s(), "supersedes_id": s(), "supersedes_version": n, "request_id": s()}, "decision_id", "version", "status", "rationale", "request_id")
	add("project_context", "project.context", "Read current project and bounded approved decision context. Read full details through decision_get/list.", map[string]any{})
	add("feed_list", "feed.list", "Read the event feed of your subtree (you, the agents reporting to you and their reports): exits, escalations, run outcomes and retries, reports, missing reports. Optional task and attention filters; after/limit paginate (1..50).", map[string]any{"task": s(), "attention": map[string]any{"type": "boolean"}, "after": n, "limit": n})
	add("project_feed", "project.feed", "Read the project event feed: stable event pointers with short summaries, not full logs. after/limit paginate (1..20).", map[string]any{"after": n, "limit": n})

	for i := range out {
		if out[i].Name == "factory_task_cancel" {
			out[i].Description = "Factory: Cancel a task you have authority over (or created) with its current version; cascade: true also cancels its open children (required when it has any)."
			out[i].InputSchema["properties"].(map[string]any)["cascade"] = map[string]any{"type": "boolean"}
		}
	}
	out = append(out, authorityDefinitions()...)
	out = append(out, peopleBriefTool())
	withPeopleFields(out)
	return out
}

// Validate checks required fields, unknown keys and nested JSON types before routing. It reports
// every problem at once (all missing required arguments, all unknown arguments with the accepted
// names, all type errors), so a model that guessed the arguments fixes them in one retry.
func Validate(t Tool, args map[string]any) error {
	var p problems
	validate(t.InputSchema, args, "", &p)
	return p.err()
}

type problems struct {
	missing, unknown, invalid []string
	accepted                  []string // "<object>: a, b" for each object with unknown arguments
}

func (p *problems) err() error {
	var parts []string
	if len(p.missing) > 0 {
		parts = append(parts, "missing required arguments: "+strings.Join(p.missing, ", "))
	}
	if len(p.unknown) > 0 {
		parts = append(parts, "unknown arguments: "+strings.Join(p.unknown, ", ")+" (accepted "+strings.Join(p.accepted, "; ")+")")
	}
	if len(p.invalid) > 0 {
		parts = append(parts, "invalid arguments: "+strings.Join(p.invalid, "; "))
	}
	if len(parts) == 0 {
		return nil
	}
	return errors.New(strings.Join(parts, "; "))
}

func validate(schema map[string]any, value any, path string, p *problems) {
	bad := func(msg string) {
		if path == "" {
			p.invalid = append(p.invalid, msg)
		} else {
			p.invalid = append(p.invalid, path+": "+msg)
		}
	}
	join := func(k string) string {
		if path == "" {
			return k
		}
		return path + "." + k
	}
	switch schema["type"] {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			bad("object required")
			return
		}
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]string); ok {
			for _, k := range req {
				if _, ok := obj[k]; !ok {
					p.missing = append(p.missing, join(k))
				}
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		unknown := false
		for _, k := range keys {
			sub, ok := props[k]
			if !ok {
				if each, ok := schema["additionalProperties"].(map[string]any); ok {
					validate(each, obj[k], join(k), p) // a map whose values all follow one schema
					continue
				}
				if schema["additionalProperties"] != true {
					p.unknown = append(p.unknown, join(k))
					unknown = true
				}
				continue
			}
			validate(sub.(map[string]any), obj[k], join(k), p)
		}
		if unknown {
			names := make([]string, 0, len(props))
			for k := range props {
				names = append(names, k)
			}
			sort.Strings(names)
			where, list := "arguments", strings.Join(names, ", ")
			if path != "" {
				where = path
			}
			if list == "" {
				list = "none"
			}
			p.accepted = append(p.accepted, where+": "+list)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			bad("boolean required")
		}
	case "string":
		if _, ok := value.(string); !ok {
			bad("string required")
		}
	case "integer":
		v, ok := value.(float64)
		if !ok || v != float64(int(v)) {
			bad("integer required")
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			bad("array required")
			return
		}
		for i, v := range a {
			validate(schema["items"].(map[string]any), v, fmt.Sprintf("%s[%d]", path, i), p)
		}
	}
}
