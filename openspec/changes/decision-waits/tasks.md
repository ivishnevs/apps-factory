## 1. Engine

- [ ] 1.1 Park kind `decision` (`decision_id`, optional deadline that escalates, never wakes to poll); recording or
      withdrawing the decision wakes every task parked on it with the outcome in the brief; tests.
- [ ] 1.2 Park kind `message` (`from` agent or owner, optional `topic`): the next message from that sender to the task
      or its agent in the project wakes it with the message in the brief; no timer polls; promised owner details
      (inbox-badge-parity 3.3) use it; tests.

## 2. Surfaces

- [ ] 2.1 Decision card lists waiting tasks; board card "Waits for your decision: <topic>" with a link; "need you"
      counts it; dependents show the root.
- [ ] 2.2 Park tool schema and descriptions, agent instructions; hint on a timed wait that names a proposed decision.

## 3. Verify

- [ ] 3.1 Replay VP-2: parked on the decision, no runs until the owner records it, then one run merges (approved) or
      reverts (rejected).
