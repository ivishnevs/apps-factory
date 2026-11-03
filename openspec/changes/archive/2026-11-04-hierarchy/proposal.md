## Why
The factory has one level: a manager-like agent and executors, with routing configured by explicit `can_message` lists and every unresolved question ending at the owner. A larger team needs leads under a top manager (for example one lead per discipline), managers that steer and unblock their own people, and an owner who is asked only what nobody below may decide. Risks this change removes: most owner questions are routine and could be decided lower down, while only releases and money really need the owner; a coordinator running in parallel with itself duplicates work; and questions to a silent lead simply wait.

## What Changes
- `spec.reports_to` in agent YAML (an agent or `owner`): a tree of any depth; role names stay template data. Cycles and unknown targets fail validation.
- **Authority over a subtree**: managers up the chain direct, pause/resume, reassign (within their subtree), cancel and reprioritize their reports' tasks. Only direct, authority comments, answers, ready/run, reassign and approved artifact versions are triggers; pause and reprioritize never override parks. This replaces the interim role-based authority of `explicit-exits-authority`.
- **Questions go up**: `ask` routes to the asker's manager; a principal answers only categories delegated to it by policy (the existing `factory.decision.decide.CATEGORY` allow rules, generalized to the chain), otherwise forwards up. **Locked categories** (store submission/release, money, deletion, legal) always go to the owner and cannot be delegated. A manager silent for `escalate_after` → the question or escalation moves up automatically. Free-text answers are always allowed, the first answer wins, stale questions are flagged, questions of closed tasks are cancelled.
- **Persistent conversation tasks** between each agent and its boss (owner ↔ each direct report included; unaddressed owner messages go to a default direct report): long-lived tasks of kind `conversation` that resume the same session. Messages, questions and escalations addressed to an agent are triggers on its conversation task; separate message delivery runs are retired.
- **Watchers**: managers follow their subtree through the event feed and are woken only by what concerns them (a question or escalation to them, a delegated task closed).

## Out of scope
Event feed delivery and Telegram (`waits-events-cost-cli`), cross-company hierarchies, org-chart UI.

## Dependencies
`trigger-log-scheduling`, `reliable-recovery` (escalations), `explicit-exits-authority` (authority set, exits).

## Capabilities
### New Capabilities
- `agent-hierarchy`: Reporting tree, subtree authority, upward questions with category delegation and locked categories, silent-manager escalation, conversation tasks and watchers.

### Modified Capabilities
None (replaces fixed manager routing in messaging and decision-autonomy behaviour described in earlier changes).

## Impact
`internal/config` (`reports_to`, tree validation, derived communication defaults), `internal/policies` (locked categories, `escalate_after`), `internal/core` (`authority.go`, `messages.go` routing, new authority actions), `internal/storage` (`messages.go` → conversation triggers, `pauses`, question routing), `internal/factorytools` (pause/reassign/cancel/reprioritize/forward tools), `cmd/factory` (conversations), `configs/agents/*`, `configs/policies/*`, `prompts/*`, docs `messaging.md`, `reports-decisions.md`, `agent-configuration.md`.
