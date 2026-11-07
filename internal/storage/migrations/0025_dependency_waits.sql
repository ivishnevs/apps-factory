-- Dependency waits (dependency-wait-escalations): a task waiting on a planned BACKLOG dependency escalates only after
-- the dependency has not moved for the company's stall_backlog_after_minutes (default 24h); a background sweep that
-- fails on one task raises a sweep_failed alert instead of stopping Core.
ALTER TABLE companies ADD COLUMN stall_backlog_after_minutes bigint NOT NULL DEFAULT 1440;
ALTER TABLE task_alerts DROP CONSTRAINT task_alerts_kind_check;
ALTER TABLE task_alerts ADD CONSTRAINT task_alerts_kind_check CHECK (kind IN ('stalled','idle','no_progress','sweep_failed'));
