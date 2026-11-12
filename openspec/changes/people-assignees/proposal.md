Status: implemented 2026-11-12 (proposed 2026-11-07 by the owner: "tell the producer which tasks I do myself, or take them on the board; agents blocked by my task continue when I merge and move it to done"). See "Implementation" below.

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

## Implementation

Implemented 2026-11-12 (docs/task-lifecycle.md "People's tasks"). Decisions the proposal left open:

- **The person's moves** are `POST /tasks/{id}/actions` with `start`, `submit` (`summary`, `pull_request`) and `done`
  (`merged`), allowed to the assigned person and the company's owners (the owner's CLI token and the operator act as
  the owner); other members and agents get 403. `start` waits for the dependencies, like an agent's run.
- **"Done only with the merged pull request"**: Core does not talk to the forge (agents' merge facts come from their
  node), so the person confirms the merge (`merged: true`); Core requires the submitted pull request (in a project
  with a repository) and a recorded approval of it (`pr_approvals`, by the reviewer person or the agent's verdict).
- **Agent reviewer**: a person's submission to an agent `review_by` gives the task to the reviewer for a review run
  (as after `factory_exit_submit`); the verdict returns it to the person. The review run gets the pull request in its
  brief; the node has no Factory branch of a person's task to prepare, so an agent review of a person's pull request
  in a repository project is not exercised end to end yet (covered with fakes in a project without a repository).
- **"That person's word"** is the id of a message the person wrote (sender owner with their person id; for an
  owner, also an owner message without a person, i.e. the CLI), in `assign_reason` or the reassign `reason`.
  Directives carry no person, so they cannot be cited as a person's word.
- **Hands off**: agents are refused assign, title/description updates, `ready`, `block`, `cancel` and reassign of a
  person's task; dependency and priority changes stay allowed. `replaces` does not exist yet (task-duplicate-warnings).
- **Reminders and "your task is ready"** go to the people's inbox (recipient `owner`) and name the person: people have
  no personal inbox yet. A cancelled person's task still stalls its dependents (it can never be done).
- **"Mine" for the CLI**: `--assignee me` is the signed-in person; an owner token carries no person, so it means the
  company's owners.
