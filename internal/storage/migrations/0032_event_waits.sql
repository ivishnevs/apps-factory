-- decision-waits: a task parks on an event instead of a timer. Kind decision waits for a decision (ref_id) to be
-- recorded; kind message waits for the next message from a sender (ref_id: an agent or owner). topic says what it
-- waits for in words; deadline (Unix seconds, 0: none) only escalates, once (escalated_at), and never wakes the task.
ALTER TABLE task_parks DROP CONSTRAINT task_parks_kind_check;
ALTER TABLE task_parks ADD CONSTRAINT task_parks_kind_check CHECK (kind IN ('escalation','inspect','wait','ask','block','children','decision','message'));
ALTER TABLE task_parks ADD COLUMN topic text NOT NULL DEFAULT '';
ALTER TABLE task_parks ADD COLUMN deadline bigint NOT NULL DEFAULT 0;
ALTER TABLE task_parks ADD COLUMN escalated_at bigint NOT NULL DEFAULT 0;
CREATE INDEX task_parks_event ON task_parks(company_id, kind, ref_id) WHERE status='active';
