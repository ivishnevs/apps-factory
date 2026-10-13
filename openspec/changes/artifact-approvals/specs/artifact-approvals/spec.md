# Requirements

- Artifact id/version/URI SHALL be immutable within a project and attach to producer provenance.
- Owner approval SHALL bind the exact artifact version and SHALL remain separate from task review.
- Tasks MAY require approved versions before execution; arbitrary roles and ordering SHALL work.
- Approval of v1 SHALL NOT satisfy a v2 requirement. URI mismatch SHALL fail closed.
- Core SHALL persist inbox, decisions and eligible waiters atomically and survive restart.
- Dispatch and claim SHALL validate pinned artifact approvals; revoke SHALL stop unclaimed work.
- Deny and capability checks SHALL apply to implicit registration through task submission.
- Manual blocks and UNKNOWN quarantine SHALL NOT be cleared by artifact decisions.
- Artifact decisions SHALL NOT authorize Factory action calls.
- Core SHALL retain immutable approval inputs for already started runs; revoke does not undo work.
