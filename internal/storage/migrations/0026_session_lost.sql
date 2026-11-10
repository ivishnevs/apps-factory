-- session-continuity: a node reports session_lost when the session it should resume is gone from the runtime.
ALTER TABLE runs DROP CONSTRAINT runs_outcome_check;
ALTER TABLE runs ADD CONSTRAINT runs_outcome_check CHECK (outcome IN ('','succeeded','no_exit','failed','timed_out','idle_timeout','max_turns','setup_failed','change_missing','session_lost','lost','revoked','paused_out','stale','unclaimed'));
