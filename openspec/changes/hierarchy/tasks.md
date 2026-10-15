## 1. Tree and authority
- [x] 1.1 `spec.reports_to` in `internal/config` with validation (exists, no cycle, `owner` reserved) and derived communication defaults. Verify: config tests for a 3-level tree, a cycle and an unknown target. Done: `config.Chain/Subtree/DirectReports/Manager`, chain and direct reports always in can_message (`TestReportsTo`).
- [x] 1.2 Replace the interim rule in `internal/core/authority.go` with `chain`/`subtree`. Verify: authority tests for direct manager, grand-manager, delegator, sibling (none). Done: `Core.hasAuthority` (owner, chain of the assignee, delegator via origin_task) decides comment authority and `factory_task_direct`; escalations route to `reports_to(assignee)` (`TestAuthorityFollowsTheTree`).
- [ ] 1.3 Authority actions pause/resume/reassign/cancel/reprioritize (MCP + owner API/CLI) with subtree checks and `pauses` table in `Evaluate`; only direct/comment/answer/ready/reassign/artifact approval add authority triggers. Verify: a manager cannot reassign into another subtree (403); pause shows reason `paused:task`; reprioritize and pause leave a wait park active.

## 2. Questions up the chain
- [x] 2.1 Route `ask` to `reports_to`, `factory_question_forward`, answer permission by delegated category. Verify: developer → lead (not delegated) → top manager (delegated) answers; the developer's task wakes with the answer. Done: `factory_exit_ask` routes to `reports_to` (owner at the top), `factory_question_forward` passes it up with a note and route history, a categorized question needs the category delegated to answer, an answer from the chain is authority input (`TestQuestionsGoUpTheChain`).
- [x] 2.2 Locked categories in the `system` policy; delegation of them fails validation. Verify: config test; a `release` question goes straight to the owner inbox. Done: `config.LockedCategories` (release, money, deletion, legal) go straight to the owner; a policy delegating them (also by wildcard) fails validation (`TestLockedCategoriesCannotBeDelegated`).
- [ ] 2.3 `escalate_after` auto-forward for questions and recovery escalations. Verify: fake-clock test forwards after 4 h and leaves a note on the skipped manager's conversation.
- [ ] 2.4 Decision answers: free text always accepted, first answer wins (CAS on question version), "may be outdated" by task state version, questions of closed tasks cancelled. Verify: concurrent answers → one applied, one 409; closing the task cancels its question.

## 3. Conversation tasks
- [ ] 3.1 `kind = conversation` tasks per agent, created on catalog load; `done` keeps them open; one session; owner messages routed to `owner.default_report` unless addressed with `@name`. Verify: two owner messages produce two runs on the same thread; an `@name` message reaches the named direct report; validation fails with two direct reports and no default.
- [ ] 3.2 Retire message delivery runs; migrate pending deliveries to conversation triggers; owner `/conversations` mapped. Verify: `scripts/messaging-smoke.py --restart-core` passes.
- [ ] 3.3 `delegated_closed` trigger and subtree summary in manager briefs. Verify: brief test lists closed and escalated subtree tasks since the last run.

## 4. Templates and docs
- [ ] 4.1 Update `configs/agents/*` with `reports_to`, `configs/policies/*` with categories, and manager prompts. Verify: `factory config validate`. Partial: `reports_to` in configs/agents (calorie-* → calorie-manager, demo/claude-demo → manager).
- [ ] 4.2 Update `docs/messaging.md`, `docs/reports-decisions.md`, `docs/agent-configuration.md`. Verify: docs describe the tree, routing and locked categories.
