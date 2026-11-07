Status: proposed 2026-11-07 by the owner ("tell the producer which tasks I do myself, or take them on the board; agents
blocked by my task continue when I merge and move it to done"). Nothing implemented.

## Why

People cannot take part in the work without breaking the flow. A task's assignee must be a configured agent
(`state.go`: "assignee … is not a configured agent"), so when the owner wants to do a piece personally, there are three bad
options: let an agent do it anyway; cancel the task and do the work outside Factory, which leaves the tasks that
depend on it waiting on a cancelled task (a `stalled` escalation at once); or block it by hand and remember to
unblock it. None of them tells the agents that the work is being done by someone, and none of them releases the
dependents when it is finished.

Factory already knows people (members, `member:USER` reviewers, people-attribution) and already releases dependents
when a dependency is DONE. What is missing is a task that belongs to a person.

## What Changes

The principle (owner 2026-11-07): **no new statuses, no separate workflow.** A person's task is an ordinary task in
the same statuses and the same transitions; the only difference is who is in the `assignee` field.

- **A person as assignee.** `assignee` accepts `member:<user>` (the owner too) in the same field: the board's assignee
  picker lists people next to agents, and the producer sets it the same way. Core does not dispatch runs for such a
  task; everything else (statuses, dependencies, review, merge rule) is unchanged. The existing state explanation
  says "assigned to <person>" instead of "not a configured agent".
- **Two ways to take a task.** In the board's assignee picker (only when no run is live; a live run is stopped
  first, as for any reassignment). Through the manager: the person tells the producer "I'll do X myself" and the
  producer reassigns the task to them, citing the message. An agent may assign a task to a person only on that
  person's word.
- **The person moves it through the same statuses** an agent's run would: READY → IN_PROGRESS → IN_REVIEW (with the
  pull request) → DONE, through REWORK if the reviewer asks for changes. The rules are the agents' rules: `review_by`
  reviews, DONE only with the merged pull request. Giving it back is reassigning it to an agent.
- **Dependents continue on their own.** Tasks that depend on a person's task wait in the normal dependency wait and
  start when it is DONE: no escalation, no manual block.
- **Agents keep their hands off.** Agents may read, comment on and depend on a person's task; they may not change
  its assignee, status or description, or cancel or replace it. The person and the owner may.
- **Reminders, not escalations.** A dependency on a person's task is not a stall. If the person's task has not
  changed for a company-configurable time (default 3 days) while other tasks wait on it, the person gets one
  reminder per episode naming the waiting tasks.
- **Finding your tasks.** Board filter "Mine", the person's avatar on cards, `factory tasks list --assignee me`.

Not in scope: time tracking, workspaces or branches managed for people (the person works in their own clone),
conflict detection between a person's branch and agents' branches.

## Impact

Task model (assignee kinds, validation against members), scheduler and wait reasons (`internal/tasks/state.go`),
the existing status transitions opened to the assigned person (authority checks), stall detection (no stall on a person's task, reminders), agent briefs and the
manager prompt ("I'll do it myself"), Factory MCP schemas (`assignee: member:<user>`, refusals for agents), web
board and TUI, notifications. Specs: `task-engine`, `factory-mcp`. Related: `dependency-wait-escalations` (a person's
task is never a stall), `task-duplicate-warnings`, `people-attribution`, `review-coverage`.
