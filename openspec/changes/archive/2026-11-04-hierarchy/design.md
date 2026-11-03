## Context
Agents have `role`, `communication.can_message/can_create_tasks/can_assign_tasks` and policy rules with `target: self|manager|owner`. Questions to agents start separate message delivery runs; unanswered ones escalate to the owner. Owner conversations keep the manager's thread. Delegated decisions require `factory.decision.decide.CATEGORY` allow rules in captured and current policy. After `explicit-exits-authority`, `core.Authority(task)` uses an interim "role = manager" rule.

## Goals / Non-Goals
Goals: any-depth tree as data; authority follows the tree; the owner sees only what nobody below may decide; nothing waits forever on a silent manager; one session per manager↔boss dialog.
Non-goals: matrix reporting (one `reports_to` per agent), per-project trees (the tree is per company).

## Decisions

### D1. Tree
`spec.reports_to: <agent name> | owner` (default `owner`). Validation (`config.LoadCatalog`, `factory config validate`): target exists in the same company, no cycles, `owner` is reserved. `chain(a)` = `reports_to` links up to the owner; `subtree(a)` = agents whose chain contains `a`. Defaults derived from the tree: an agent may message its chain and its direct reports and may create/assign tasks for itself and its subtree; explicit `communication` lists may add peers but never remove the chain.

### D2. Authority and actions
`Authority(task)` = owner (rank 0) ∪ `chain(assignee)` (rank = depth) ∪ assignee of `created_from_task`. Actions, each with expected `version` and `request_id`, available to the owner (CLI/API) and to agents in the authority set (MCP):
| Tool | Rule |
|---|---|
| `factory_task_direct` | from `explicit-exits-authority` |
| `factory_task_pause` / `resume` | adds/removes a `pauses(scope=task\|agent, ref, by)` row; agent scope only for agents in the actor's subtree; resume by the pauser or a higher rank |
| `factory_task_reassign` | new assignee ∈ `subtree(actor) ∪ {actor}` (owner: anyone); `assignment` trigger; a live run is revoked |
| `factory_task_cancel` | `cascade: true` required with open children; live run revoked |
| `factory_task_reprioritize` | priority within the existing bounds |
Every action is an event for the chain. Only direct, a comment by an authority principal, an answer, ready/run, reassign and an approved artifact version are authority triggers (`explicit-exits-authority` D6) that wake the task and override interruptible parks. Pause/resume and reprioritize are actions, not triggers: pause adds a `paused:task|agent` condition, resume removes it (the task runs only if it has unhandled triggers), reprioritize changes ordering; none of them overrides a park. Cancel ends the task. The owner writing directly to an executor is allowed and always produces an event visible to the executor's chain.

### D3. Questions go up
`factory_exit_ask {question, options, category}` creates a question (`messages`, type question) with `route_to = reports_to(asker)` and an `ask` park on the asker's task. A principal P may answer if P is the owner, or the category is delegated to P (explicit allow `factory.decision.decide.<category>` in P's captured and current policy, as today). Otherwise P calls `factory_question_forward` (or answers "I can't decide this" which forwards). **Locked categories** — `release` (store submission/publishing), `money` (purchases, paid services), `deletion`, `legal` — route straight to the owner; a policy that delegates them fails validation. They extend the reserved `system` policy. Options are suggestions: a free-text answer is always allowed. The first answer wins: answering is a compare-and-set on the question's version, later answers get 409 with the recorded answer. Each question stores the asking task's state version; if the task changed since (new triggers, status or assignee), the answerer sees "may be outdated" before answering. When the asking task closes, its open questions are cancelled. An answer is an `answer` trigger on the asking task (authority if the answerer is in its authority set, which the chain always is) and clears the `ask` park. The route history is kept on the question.

### D4. Silent manager
A question or escalation routed to a non-owner principal and unanswered for `escalate_after` (policy, default 4 h, per category allowed) is forwarded one level up automatically, with a note to the skipped manager's conversation. At the owner it stays (reminders and digest: `waits-events-cost-cli`). Escalations of `reliable-recovery` (`retries_exhausted`, `budget_exhausted`, `inspect`, `no_exit`) are routed to `reports_to(assignee)` with the same rule.

### D5. Conversation tasks
Each agent has one task of `kind = conversation` per company, assignee = the agent, `counterpart = reports_to` (owner or agent). It is created on catalog load, never closed by an agent, and keeps one runtime session (`session_turn` index already prevents concurrent turns). Triggers on it: messages from the counterpart (authority), questions and escalations routed to the agent (non-authority but waking), `delegated_closed` when a task the agent delegated closes and the agent has no open task waiting on it. Its runs end with `factory_exit_done`, meaning "handled up to `ack_seq`"; the task stays open. The owner ↔ direct-report dialog is the direct report's conversation task; existing owner `/conversations` map onto conversation tasks. An owner with several direct reports sets a default one (company setting `owner.default_report`, required when there is more than one); owner messages without an address (CLI, Telegram) go to the default report's conversation, and a message starting with `@name` goes to that direct report's conversation. Message delivery runs (`message_id` RunSpecs, `ScheduleMessages`) are retired; a migration converts pending deliveries into triggers on the recipients' conversation tasks.
Note (node): a conversation run (`RunSpec.conversation`) holds neither the shared workspace's writer lock nor a task worktree; it works in a per (agent, project) conversation directory under the node's state_dir, where its project data is materialized and its session resumes, and reads the project workspace by path (`FACTORY_PROJECT_WORKSPACE`), so it runs beside the project's writer run.

### D6. Watchers
Managers do not get runs for every event in their subtree. They see subtree events in their next brief ("since your last run": closed, escalated, failed tasks under you) and in the event feed; they are woken only by D5 triggers.

## Risks / Trade-offs
- Long chains add latency to questions → category delegation lets lower managers answer; `escalate_after` bounds waiting.
- One conversation task per agent can grow a long session → driver resume policy may start a fresh session with a summary when the runtime reports context limits; the conversation history stays in the DB.
- Removing message delivery runs changes messaging behaviour → API shape kept; verification re-runs the messaging smoke with conversation tasks.

## Amendments

- 2026-10-20 (D5): conversation runs exit without repository facts (they run in their own conversation directory and own no branch or PR); Core accepts done, ask and wait from them in a project with a repository.
