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

- **A person as assignee.** `assignee` accepts `member:<user>` (the owner too). Core never schedules or dispatches
  runs for such a task; its wait reason is `with_person` ("with Ilya"), not `unassigned` or a stall.
- **Two ways to take a task.** On the board: "Take it" on any task that has no live run (assigns the signed-in
  person; a task IN_PROGRESS with a live run asks to stop the run first). Through the manager: the person tells the
  producer "I'll do X myself"; the producer creates or reassigns the task to `member:<user>`. An agent may assign a
  task to a person only on that person's word (a message or directive of that person in its brief, recorded on the
  task as the reason).
- **The person moves it.** Start (IN_PROGRESS), Done, Give back (to an agent, which the person picks or the
  manager's default). Done accepts an optional pull request: with a pull request on a connected repository the task
  goes to IN_REVIEW and becomes DONE when the pull request is merged (the owner's rule "done only with a merged
  PR"); without one it is DONE at once. `review_by` works as for agents, so a person may ask an agent (game-qa) or
  another person to review.
- **Dependents continue on their own.** Agents' tasks that depend on a person's task wait in the normal dependency
  wait and start when it is DONE: no escalation, no manual block. The waiting agent's brief and the board say
  "waits for <task>, with <person>".
- **Agents keep their hands off.** Agents may read, comment on and depend on a person's task; they may not change
  its assignee, status or description, cancel it or replace it (task-duplicate-warnings `replaces` warns instead).
  The person and the owner may.
- **Reminders, not escalations.** A person's task that has not changed for a company-configurable time (default 3
  days) while other tasks wait on it sends one reminder per episode to that person (inbox and Telegram), naming the
  waiting tasks. A person's task becoming unblocked (its own dependencies done) notifies the person once.
- **Finding your tasks.** Board filter "Mine", a person column/avatar on cards, `factory tasks list --assignee me`.

Not in scope: time tracking, workspaces or branches managed for people (the person works in their own clone),
conflict detection between a person's branch and agents' branches.

## Impact

Task model (assignee kinds, validation against members), scheduler and wait reasons (`internal/tasks/state.go`),
task actions for people (`take`, `start`, `done` with a pull request, `give_back`) and their authority checks, merge
watcher (person tasks with a pull request), stall detection (`with_person`, reminders), agent briefs and the
manager prompt ("I'll do it myself"), Factory MCP schemas (`assignee: member:<user>`, refusals for agents), web
board and TUI, notifications. Specs: `task-engine`, `factory-mcp`. Related: `dependency-wait-escalations` (a person's
task is never a stall), `task-duplicate-warnings`, `people-attribution`, `review-coverage`.
