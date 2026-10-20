-- Run status is the phase (reliable-recovery D1): QUEUED → CLAIMED → RUNNING → (CANCELLING) → ENDED; how a
-- run ended is only its outcome. CLAIMED (a v2 run claimed but not started) is stored instead of derived.
DROP TRIGGER run_outcome;
DROP INDEX agent_slot;
DROP INDEX workspace_writer;
DROP INDEX session_turn;
DROP INDEX active_task_run;
-- The outcome derivation of the retired run_outcome trigger, for ended runs that still lack one.
UPDATE runs SET outcome=CASE
  WHEN status='COMPLETED' THEN 'succeeded'
  WHEN status='CANCELLED' THEN 'revoked'
  WHEN result LIKE 'timed_out%' THEN 'timed_out'
  WHEN result LIKE 'lost:%' THEN 'lost'
  WHEN result LIKE 'OpenSpec preflight%' OR result LIKE 'skills:%' OR result LIKE 'workspace_diverged%' THEN 'setup_failed'
  ELSE 'failed' END
 WHERE status IN ('COMPLETED','FAILED','CANCELLED') AND outcome='';
UPDATE runs SET status='ENDED' WHERE status IN ('COMPLETED','FAILED','CANCELLED');
UPDATE runs SET status='CLAIMED' WHERE status='RUNNING' AND protocol=2 AND run_started=0;
CREATE UNIQUE INDEX agent_slot ON runs(company_id,agent) WHERE status IN ('CLAIMED','RUNNING','CANCELLING');
CREATE UNIQUE INDEX workspace_writer ON runs(workspace)
 WHERE workspace != '' AND (status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING') OR unreleased=1);
CREATE UNIQUE INDEX session_turn ON runs(session_id)
 WHERE session_id != '' AND (status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING') OR unreleased=1);
CREATE UNIQUE INDEX active_task_run ON runs(task_id)
 WHERE task_id IS NOT NULL AND (status IN ('QUEUED','CLAIMED','RUNNING','CANCELLING') OR unreleased=1);
-- Every status change names a phase, and an ended run its outcome (set by the code that ends it).
CREATE TRIGGER run_phase BEFORE UPDATE OF status ON runs
WHEN NEW.status NOT IN ('QUEUED','CLAIMED','RUNNING','CANCELLING','ENDED') OR (NEW.status='ENDED' AND NEW.outcome='')
BEGIN
 SELECT RAISE(ABORT,'run status must be a phase; an ENDED run needs its outcome');
END;
-- A task held BLOCKED on a run before 0029 whose run has ended and is released (its runtime gone) was never
-- given that end: it is released as recoverTask would release it (READY after the backoff, the outcome's
-- counters). Side-effect tasks (the task's flag or the run's recovery: inspect rule) and ends that would
-- exhaust the attempts need an escalation and stay for the owner (factory tasks ready).
CREATE TEMP TABLE released_run_holds AS
SELECT t.id AS task_id,r.id AS run_id,r.outcome!='lost' AS attempt,r.result AS result FROM tasks t JOIN runs r ON r.id=json_extract(t.body,'$.current_run_id')
WHERE t.status='BLOCKED' AND json_extract(t.body,'$.block_reason') LIKE 'run:%'
 AND coalesce(json_extract(t.body,'$.side_effects'),0)=0
 AND r.status='ENDED' AND r.unreleased=0 AND r.recovered=0
 AND r.outcome IN ('failed','timed_out','idle_timeout','max_turns','setup_failed','lost')
 AND (r.outcome='lost' OR coalesce(json_extract(t.body,'$.attempts_no_progress'),0)+1<3)
 AND NOT EXISTS (SELECT 1 FROM json_each(r.spec,'$.config_snapshot.rules') x
  WHERE json_extract(x.value,'$.type')='recovery' AND json_extract(x.value,'$.mode')='inspect');
UPDATE tasks SET status='READY',version=version+1,body=(SELECT json_set(json_remove(tasks.body,'$.block_reason'),
  '$.status','READY',
  '$.version',tasks.version+1,
  '$.updated_at',CAST(strftime('%s','now') AS INTEGER),
  '$.result',h.result,
  '$.fail_streak',coalesce(json_extract(tasks.body,'$.fail_streak'),0)+1,
  '$.not_before',CAST(strftime('%s','now') AS INTEGER)+60*CASE coalesce(json_extract(tasks.body,'$.fail_streak'),0) WHEN 0 THEN 1 WHEN 1 THEN 5 ELSE 15 END,
  '$.attempts_no_progress',coalesce(json_extract(tasks.body,'$.attempts_no_progress'),0)+h.attempt)
 FROM released_run_holds h WHERE h.task_id=tasks.id)
WHERE id IN (SELECT task_id FROM released_run_holds);
UPDATE runs SET recovered=1 WHERE id IN (SELECT run_id FROM released_run_holds);
DROP TABLE released_run_holds;
-- retry_eligible was always false since FAILED retired (0029).
UPDATE tasks SET body=json_remove(body,'$.retry_eligible') WHERE json_extract(body,'$.retry_eligible') IS NOT NULL;
