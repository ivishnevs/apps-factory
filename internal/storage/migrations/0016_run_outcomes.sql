-- Run outcomes (reliable-recovery D1), recorded next to the status; scheduling still follows the status.
ALTER TABLE runs ADD COLUMN outcome TEXT NOT NULL DEFAULT '' CHECK(outcome IN ('','succeeded','no_exit','failed','timed_out','idle_timeout','max_turns','setup_failed','lost','revoked','paused_out','stale','unclaimed'));
-- last_activity_at: the run's last applied event (or its start); the idle detector stops runs silent for too long.
ALTER TABLE runs ADD COLUMN last_activity_at INTEGER NOT NULL DEFAULT 0;
-- stop_reason: Core asked the worker to stop the run for this reason (idle_timeout), without an owner cancel.
ALTER TABLE runs ADD COLUMN stop_reason TEXT NOT NULL DEFAULT '';
-- The one place that derives an outcome from how a run ended, unless the code that ended it set one.
CREATE TRIGGER run_outcome AFTER UPDATE OF status ON runs
WHEN NEW.status IN ('COMPLETED','FAILED','CANCELLED','UNKNOWN') AND NEW.outcome=''
BEGIN
 UPDATE runs SET outcome=CASE
  WHEN NEW.status='COMPLETED' THEN 'succeeded'
  WHEN NEW.status='CANCELLED' THEN 'revoked'
  WHEN NEW.status='UNKNOWN' AND NEW.result LIKE 'timed_out%' THEN 'timed_out'
  WHEN NEW.status='UNKNOWN' THEN 'lost'
  WHEN NEW.result LIKE 'lost:%' THEN 'lost'
  WHEN NEW.result LIKE 'OpenSpec preflight%' OR NEW.result LIKE 'skills:%' OR NEW.result LIKE 'workspace_diverged%' THEN 'setup_failed'
  ELSE 'failed' END
 WHERE id=NEW.id;
END;
UPDATE runs SET outcome=CASE
  WHEN status='COMPLETED' THEN 'succeeded'
  WHEN status='CANCELLED' THEN 'revoked'
  WHEN status='UNKNOWN' THEN 'lost'
  WHEN result LIKE 'OpenSpec preflight%' THEN 'setup_failed'
  ELSE 'failed' END
 WHERE status IN ('COMPLETED','FAILED','CANCELLED','UNKNOWN');
