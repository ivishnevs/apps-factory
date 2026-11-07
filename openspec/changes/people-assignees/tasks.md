## 1. Model and scheduling

- [ ] 1.1 `assignee: member:<user>` validated against the company's members; the scheduler never dispatches a
      person's task; state explanation "assigned to <person>"; no new status or wait reason; tests.
- [ ] 1.2 The existing transitions (start, submit for review with a pull request, rework, done on merge) available to
      the assigned person and the owner, with the same rules as for an agent's run; reassignment stops a live run
      first; tests.

## 2. Agents

- [ ] 2.1 Agents may assign to a person only with that person's message or directive cited as the reason; agents'
      status, assignee, description, cancel and `replaces` changes on a person's task are refused with a hint; tests.
- [ ] 2.2 Briefs and dependency waits name the person ("waits for <task>, with <person>"); manager prompt: "I'll do it
      myself" → create or reassign to the person, do not plan the work for an agent.

## 3. Waiting and reminders

- [ ] 3.1 A dependency on a person's task is never `stalled`; reminder to the person after `people.remind_after`
      (default 72h) without change while tasks wait on it, once per episode; "your task is ready" notice; tests.

## 4. UI

- [ ] 4.1 Web board: people in the assignee picker, the same status moves for the assigned person, avatar on cards, "Mine"
      filter; TUI and CLI (`--assignee me`).

## 5. Verify

- [ ] 5.1 Sandbox company: the owner assigns to themself a task two agent tasks depend on, moves it to review with a pull
      request; the task becomes DONE on approval and merge and both dependents start without any escalation or manual action.
