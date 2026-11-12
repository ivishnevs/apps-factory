## 1. Decisions with comments

- [ ] 1.1 An approval decided with a reason other than the default records a board comment on the approval's task,
      linked to the approval (approve and reject, artifact and command approvals); the comment-outcome rules apply;
      tests.
- [ ] 1.2 Approval reads (Inbox, task, Library) carry the comment's outcome; web and TUI show it under the approval.

## 2. Docs and verify

- [ ] 2.1 `docs/artifact-approvals.md`, `docs/approvals.md`, `docs/task-lifecycle.md`; then drop the approval-comment
      lines from the tvorog Launch Creative and Reviewer prompts.
- [ ] 2.2 Replay task 2509ee3e with fakes:
      - the board approves sample v2 with "the first screenshot without the callout";
      - Launch Creative's exit is refused until it records "done: frame 1 without the window";
      - the approval shows that outcome.
