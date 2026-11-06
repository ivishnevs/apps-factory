## 1. Decisions (owner)

- [ ] 1.1 Default affinity wait (10 minutes proposed); one company setting or per agent.

## 2. Core

- [ ] 2.1 Record the node of a session (run's node when the session id is recorded); migration.
- [ ] 2.2 Offers skip runs whose session holder is another node that still holds the affinity; claim refuses with
      retry `affinity`.
- [ ] 2.3 Release: holder offline beyond the timeout, removed, paused, unable to run the agent, or waited longer than
      the affinity wait; the next node starts fresh and the affinity moves.
- [ ] 2.4 Verdict reason `affinity` with the holder and the remaining wait; tests for every release case and for
      two nodes alternating on a conversation.

## 3. Visibility

- [ ] 3.1 Task: "waiting for NODE, it holds this agent's session"; runs: resumed or fresh, and why.
- [ ] 3.2 Count fresh starts per company over a week before and after.
