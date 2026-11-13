## 1. Approval holds the task

- [x] 1.1 Core refuses `done` while an artifact version produced by the task has an approval waiting for people; the hint says the task waits for the decision
- [x] 1.2 Command guard refuses `gh pr merge` and pushes to the default branch for the task's pull request while such an approval waits
- [x] 1.3 After review the task waits as `awaiting_approval`; web, TUI and Telegram show it
- [x] 1.4 Approved/chosen wakes the task to merge and exit `done`; changes requested returns it to the agent on the same branch; rejected wakes it to close the PR unmerged and end without a merge
- [x] 1.5 Delegate decisions move the task the same way; other tasks' and command approvals do not hold it

## 2. Interactive artifact preview

- [x] 2.1 Viewer and inbox preview iframes use `sandbox="allow-scripts"`; raw artifact content is served with `Content-Security-Policy: sandbox allow-scripts`
- [x] 2.2 Test that the served page cannot reach same-origin state (header and attribute checks)

## 3. Binding reviewer, manager by default

- [x] 3.1 Default reviewer: assignee's review-policy reviewer, else the team's default reviewer (new team setting), else its manager (`reports_to`), else team lead, else owner; never the assignee
- [x] 3.2 Submit goes to the task's reviewer; a different named reviewer is refused; a task without one takes the default
- [x] 3.3 Changing the reviewer is an authority action (assignee's managers, task creator, people)
- [x] 3.4 Path-based review policies still apply; surfaces show the actual reviewer

## 4. Verdict covers the submission

- [x] 4.1 `approved` verdict carries per-artifact checked / not checked with notes; Core refuses missing or not-checked artifacts with a hint
- [x] 4.2 The checks are stored and shown on the task with the reviewer's name

## 5. Tests and docs

- [x] 5.1 Storage and Core tests for each rule with fixtures (no company-specific data); no live runs (sections 1, 3, 4; section 2 has its own)
- [x] 5.2 Docs (owner-ui, review, artifact approvals) and spec deltas (sections 1, 3, 4)
