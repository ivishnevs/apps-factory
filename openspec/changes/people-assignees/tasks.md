## 1. Model and scheduling

- [x] 1.1 `assignee: member:<user>` validated against the company's members; the scheduler never dispatches a
      person's task; state explanation "assigned to <person>"; no new status or wait reason; tests.
      (`tasks.PersonUser`, `Core.assigneeExists` on create, update, ready and reassign; `why` is `idle` "assigned to
      NAME" with the person as clearer; `internal/tasks/people_test.go`, `internal/core/people_test.go`.)
- [x] 1.2 The existing transitions (start, submit for review with a pull request, rework, done on merge) available to
      the assigned person and the owner, with the same rules as for an agent's run; reassignment stops a live run
      first; tests. (`POST /tasks/{id}/actions` `start|submit|done` → `Store.PersonAction`; submit needs the pull
      request in a repository project, an agent `review_by` gets a review run, done needs the approval and
      `merged: true`; other members and agents 403; giving a started task back to an agent makes it READY.)

## 2. Agents

- [x] 2.1 Agents may assign to a person only with that person's message or directive cited as the reason; agents'
      status, assignee, description, cancel and `replaces` changes on a person's task are refused with a hint; tests.
      (`assign_reason` on create/assign, the reassign `reason`: the id of a message that person wrote; 403 with the
      hint to message instead for assign, update of title/description, ready, block, cancel and reassign. `replaces`
      does not exist yet (task-duplicate-warnings is not implemented); its refusal belongs to that change.)
- [x] 2.2 Briefs and dependency waits name the person ("waits for <task>, with <person>"); manager prompt: "I'll do it
      myself" → create or reassign to the person, do not plan the work for an agent. (Dependency verdict and brief
      line; the owner-chat brief adds the rule and shows the writer as `member:<user>`; tool descriptions.)

## 3. Waiting and reminders

- [x] 3.1 A dependency on a person's task is never `stalled`; reminder to the person after `people.remind_after`
      (default 72h) without change while tasks wait on it, once per episode; "your task is ready" notice; tests.
      (`GET|PUT /settings/company/people`, migration 0033 `people_remind_after_minutes` and `person_reminders`; no
      `idle` alert for a person's task.)

## 4. UI

- [x] 4.1 Web board: people in the assignee picker, the same status moves for the assigned person, avatar on cards, "Mine"
      filter; TUI and CLI (`--assignee me`). (Web: reassign and new-task pickers, Start / Submit for review / Mark
      done, ringed avatar and name on cards, "Mine" in the assignee filter. TUI: `@user` chip, reassign to
      `member:USER`, filters `a:people` and `a:member:USER`. CLI: `tasks list --assignee me`, `tasks start`,
      `tasks submit-pr`, `tasks done --merged`.)

## 5. Verify

- [x] 5.1 Sandbox company: the owner assigns to themself a task two agent tasks depend on, moves it to review with a pull
      request; the task becomes DONE on approval and merge and both dependents start without any escalation or manual action.
      (Replayed with fakes: `TestPersonTaskReleasesDependents`, including four days of waiting with one reminder and
      no stalled escalation; `TestPersonTaskAgentReviewAndGiveBack` covers an agent reviewer and giving it back.)
