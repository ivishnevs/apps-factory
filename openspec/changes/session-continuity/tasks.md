## 0. Verify

- [ ] 0.1 What Claude Code (Agent SDK `resume`) and Codex (`thread/resume`) do today when the recorded session's
      transcript or rollout is missing: the run fails, or a fresh session starts silently; record the error each gives.

## 1. Sessions remember their account

- [ ] 1.1 Session records carry the runtime account hash; `resumeSpec` starts a fresh session (FreshSessionNote)
      when it differs from the current one; records without a hash resume as today; tests.
- [ ] 1.2 `factory-node accounts accept RUNTIME` (and the Inbox Continue when node-runtime-accounts 3.3 lands)
      resets that runtime's sessions on the node; tests: switch account, accept, next run is fresh.

## 2. Lost session asks the owner

- [ ] 2.1 Drivers: locate a session (Claude transcript under the node's config directory and the session's cwd;
      Codex rollout under CODEX_HOME); a resume the runtime rejects is reported as `session_lost`.
- [ ] 2.2 Node: recorded session, same account, session missing → the run is not started and is reported as
      `session_lost`; tests.
- [ ] 2.3 Core: `session_lost` escalation (one per task and agent) with Continue / Wait; Continue requeues with a
      fresh session, Wait keeps the run queued and tied; closes on the pair's next successful run; tests.
- [ ] 2.4 Labels in web Inbox, TUI and Telegram; feed attention kind; docs (task-lifecycle, node-install).

## 3. Verify

- [ ] 3.1 On a node: run a task, log the runtime into another account, accept → the next run is fresh with the note.
- [ ] 3.2 On a node: run a task, delete its transcript → an Inbox question; Continue → fresh run; Wait → nothing runs
      until answered.
