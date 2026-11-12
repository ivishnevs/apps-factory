## 1. Decisions with comments

- [x] 1.1 An approval decided with a reason other than the default records a board comment on the approval's task,
      linked to the approval (approve and reject, artifact and command approvals); the comment-outcome rules apply;
      tests.
      Migration 0040 `task_comments.approval_id`; the approval keeps `comment_id`
      (`internal/storage/approval_comments.go`). Default = `approved by owner` or a bare ok/lgtm/yes/approved/ок/да/
      одобрено; a rejection's reason always counts. The decision's `approval_decided` trigger is the one wake-up; the
      brief line says "[on artifact approval … approved]"; a done outcome of such a comment needs `reason` (what was
      changed). A DONE/CANCELLED task's comment goes to the project lead's conversation (reason `closed`); no task, a
      conversation task, a delegate agent's decision or a revoke record no comment.
      Tests: `internal/core/approval_comments_test.go`.
- [x] 1.2 Approval reads (Inbox, task, Library) carry the comment's outcome; web and TUI show it under the approval.
      `GET /approvals`, `GET /approvals/{id}` and the artifact approval carry `comment: {id, status, outcome}`;
      web: task materials row, file viewer of a task's file (the Library listing reads no approvals, so it shows none), Inbox "Your comments on approvals" (decisions of
      the last week, not counted in the badge); task comments say "on approval …" with the done note (web and TUI).

## 2. Docs and verify

- [ ] 2.1 `docs/artifact-approvals.md`, `docs/approvals.md`, `docs/task-lifecycle.md`; then drop the approval-comment
      lines from the tvorog Launch Creative and Reviewer prompts.
      Docs done (also `docs/factory-mcp.md` and the exit tools' `comments` description). Blocker: the tvorog Launch
      Creative and Reviewer prompts are not in this repository (they live in the prod company's catalog); the owner
      drops the line there once this ships.
- [x] 2.2 Replay task 2509ee3e with fakes:
      - the board approves sample v2 with "the first screenshot without the callout";
      - Launch Creative's exit is refused until it records "done: frame 1 without the window";
      - the approval shows that outcome.
      `TestApprovalCommentReplay` (agent demo stands in for Launch Creative), `TestApprovalCommentKinds`.
