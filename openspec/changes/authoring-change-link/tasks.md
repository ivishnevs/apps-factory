## 1. Link kinds

- [ ] 1.1 `change_link: implements|authors` on tasks, the API and lead tools; default `authors` for a lead's task
      when the change does not exist yet; tests.
- [ ] 1.2 Node preflight skips `authors` links; tests.
- [ ] 1.3 Submit and done of an `authors` task require the change on its branch; tests.

## 2. Escalation

- [ ] 2.1 `change_missing` answers `retry`, `unlink`, `cancel`; `retry` offered only when the default branch or the
      link changed since the last check; a repeat on the same commit and link keeps the open escalation; tests.

## 3. Agents and verify

- [ ] 3.1 Producer prompt: spec tasks link their change as `authors`.
- [ ] 3.2 Replay `f31bb427`: a lead's "Spec:" task for a change that does not exist runs, writes the change and
      finishes; an implementation task linking a missing change escalates once, and `unlink` continues it.
