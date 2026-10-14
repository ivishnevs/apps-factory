-- Automatic retry (reliable-recovery D2–D3, owner decision 2026-10-14: autonomy first).
-- released: a lost (UNKNOWN) run whose runtime is known to be gone (v1 worker, or a v2 node that
-- registered a newer epoch or no longer lists it); only an unreleased UNKNOWN run still holds its
-- project, workspace and session, since its runtime may still be running.
ALTER TABLE runs ADD COLUMN released INTEGER NOT NULL DEFAULT 0;
DROP INDEX project_writer;
CREATE UNIQUE INDEX project_writer ON runs(project)
 WHERE status IN ('QUEUED','RUNNING','CANCELLING') OR (status='UNKNOWN' AND released=0);
DROP INDEX workspace_writer;
CREATE UNIQUE INDEX workspace_writer ON runs(workspace)
 WHERE workspace != '' AND (status IN ('QUEUED','RUNNING','CANCELLING') OR (status='UNKNOWN' AND released=0));
DROP INDEX session_turn;
CREATE UNIQUE INDEX session_turn ON runs(session_id)
 WHERE session_id != '' AND (status IN ('QUEUED','RUNNING','CANCELLING') OR (status='UNKNOWN' AND released=0));
DROP INDEX active_task_run;
CREATE UNIQUE INDEX active_task_run ON runs(task_id)
 WHERE task_id IS NOT NULL AND (status IN ('QUEUED','RUNNING','CANCELLING') OR (status='UNKNOWN' AND released=0));
-- Daily budget: budget-counted outcomes per task per day (UTC until companies get a time zone).
CREATE TABLE task_budget (
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL,
 day TEXT NOT NULL,
 used INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(company_id,task_id,day)
);
