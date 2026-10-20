-- Reliable recovery (D1, D3): UNKNOWN runs and FAILED tasks are retired.
-- A run Core judges lost ends FAILED with outcome lost (timed_out when Core ended it past its timeout).
-- unreleased: its runtime may still run (a v2 node went silent), so it keeps its workspace, session and
-- task slot until the node proves it gone (a newer epoch, a heartbeat without it, its stop) or the owner
-- resolves it. It replaces released, which was the same fact inverted and only meaningful on UNKNOWN.
DROP INDEX workspace_writer;
DROP INDEX session_turn;
DROP INDEX active_task_run;
ALTER TABLE runs ADD COLUMN unreleased INTEGER NOT NULL DEFAULT 0;
UPDATE runs SET unreleased=1 WHERE status='UNKNOWN' AND released=0;
UPDATE runs SET status='FAILED', outcome=CASE WHEN outcome='' THEN 'lost' ELSE outcome END,
 finished_at=CASE WHEN finished_at=0 THEN strftime('%s','now') ELSE finished_at END
 WHERE status='UNKNOWN';
ALTER TABLE runs DROP COLUMN released;
CREATE UNIQUE INDEX workspace_writer ON runs(workspace)
 WHERE workspace != '' AND (status IN ('QUEUED','RUNNING','CANCELLING') OR unreleased=1);
CREATE UNIQUE INDEX session_turn ON runs(session_id)
 WHERE session_id != '' AND (status IN ('QUEUED','RUNNING','CANCELLING') OR unreleased=1);
CREATE UNIQUE INDEX active_task_run ON runs(task_id)
 WHERE task_id IS NOT NULL AND (status IN ('QUEUED','RUNNING','CANCELLING') OR unreleased=1);
-- The outcome derivation without UNKNOWN.
DROP TRIGGER run_outcome;
CREATE TRIGGER run_outcome AFTER UPDATE OF status ON runs
WHEN NEW.status IN ('COMPLETED','FAILED','CANCELLED') AND NEW.outcome=''
BEGIN
 UPDATE runs SET outcome=CASE
  WHEN NEW.status='COMPLETED' THEN 'succeeded'
  WHEN NEW.status='CANCELLED' THEN 'revoked'
  WHEN NEW.result LIKE 'timed_out%' THEN 'timed_out'
  WHEN NEW.result LIKE 'lost:%' THEN 'lost'
  WHEN NEW.result LIKE 'OpenSpec preflight%' OR NEW.result LIKE 'skills:%' OR NEW.result LIKE 'workspace_diverged%' THEN 'setup_failed'
  ELSE 'failed' END
 WHERE id=NEW.id;
END;
-- A FAILED task becomes READY with a ready trigger; an open escalation keeps its park, so an escalated task
-- still waits for its answer, and anything else retries.
INSERT INTO task_triggers(company_id,task_id,kind,authority,actor,body,created_at)
SELECT company_id,id,'ready',0,'migration','FAILED retired (reliable-recovery): the task retries',strftime('%s','now') FROM tasks WHERE status='FAILED';
UPDATE tasks SET status='READY', wake_seq=(SELECT max(seq) FROM task_triggers g WHERE g.task_id=tasks.id),
 body=json_set(body,'$.status','READY','$.retry_eligible',json('false'),'$.block_reason','')
 WHERE status='FAILED';
-- The company's time zone (IANA name) sets the day of the daily retry budget (D2).
ALTER TABLE companies ADD COLUMN time_zone TEXT NOT NULL DEFAULT 'UTC';
