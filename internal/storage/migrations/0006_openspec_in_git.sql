-- OpenSpec lives in the project's repository (openspec-in-git): Core stores no OpenSpec documents and creates
-- no archive tasks. openspec_changes keeps, per change, the state the last done exit reported (D3) and the
-- notices sent to the manager (D2). No data migration: stored trees are dropped.
DROP TABLE spec_documents;
ALTER TABLE openspec_changes DROP COLUMN archive_task_id;
ALTER TABLE openspec_changes
 ADD COLUMN state text NOT NULL DEFAULT '',
 ADD COLUMN unchecked bigint NOT NULL DEFAULT 0,
 ADD COLUMN reported_task_id text NOT NULL DEFAULT '',
 ADD COLUMN reported_at bigint NOT NULL DEFAULT 0,
 ADD COLUMN notice_task_id text NOT NULL DEFAULT '';

-- change_missing: the linked change is not on the task's checkout (D3).
ALTER TABLE runs DROP CONSTRAINT runs_outcome_check;
ALTER TABLE runs ADD CONSTRAINT runs_outcome_check CHECK (outcome IN ('','succeeded','no_exit','failed','timed_out','idle_timeout','max_turns','setup_failed','change_missing','lost','revoked','paused_out','stale','unclaimed'));
