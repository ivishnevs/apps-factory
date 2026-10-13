# Task Engine requirements

## Tasks
Core SHALL validate lifecycle actions, same-project DAG dependencies and optimistic versions.
Dependencies SHALL only unblock scheduling after DONE, not runtime completion or submission.
Duplicate mutation IDs SHALL replay the original response without repeated effects.

## Scheduling
Dispatch SHALL atomically persist task IN_PROGRESS, a unique attempt/run and outbox command.
Scheduler SHALL use priority and availability without fixed role sequences. Restart SHALL recover
from persisted state. Unknown outcomes SHALL quarantine the workspace and block automatic retry.

## Review
Completed runs SHALL submit to IN_REVIEW; owner/authorized reviewer SHALL explicitly accept/reject.
Rejection SHALL preserve feedback and allow a new attempt only after an explicit ready action.
