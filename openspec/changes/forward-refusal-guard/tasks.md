## 1. A pending question keeps its recipient awake

- [x] 1.1 Core: a PENDING question addressed to an agent is not marked handled by a run that did not answer, forward
      or withdraw it; the recipient's task or conversation stays runnable (retry backoff and chain limits apply).
- [x] 1.2 The next brief names the pending question and the last refusal of an attempt to act on it.
- [x] 1.3 Tests: forward refused then exit accepted → the recipient runs again; crash after the trigger → runs again;
      answered or forwarded → no extra run; a question to a person is unaffected.

## 2. No exit past an unresolved refusal

- [x] 2.1 Record fixable refusals of send/forward/ask/approval tools per run with their `request_id`; clear one when a
      later call with the same `request_id` succeeds.
- [x] 2.2 Exits `done`, `handoff`, `submit`, `wait` refused with `unresolved_refusal` (call, refusal, how to fix);
      `ask` and `block` accepted; decision refusals (`question_limit`, closed task, authority) do not hold.
- [x] 2.3 Tests, including a forward and an exit in one turn (the 2026-11-15 case replayed with fixtures).
- [x] 2.4 After a decision refusal, an accepted `done`/`handoff`/`submit`/`wait` is marked on the task and sent to the
      agent's manager as a non-blocking notice (an attention feed item when the manager is a person); tests for an
      agent manager and an owner manager.

## 3. Brief required in the schema for agents reporting to a person

- [x] 3.1 Tool list per run: when the agent's manager is a person, `brief` is required on the people-facing tools and
      their description starts with it; tests for agents with an agent manager, an owner manager and a member manager.

## 4. Docs

- [x] 4.1 Explicit-exits contract and messaging docs: pending questions, `unresolved_refusal`, brief in the schema.
