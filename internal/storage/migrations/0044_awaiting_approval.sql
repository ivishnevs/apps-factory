-- approval-gates-merge: a reviewed task waits as awaiting_approval (ref_id: the artifact approval) while an artifact
-- version it produced waits for a decision; only that decision (or the approval leaving pending) clears the park.
ALTER TABLE task_parks DROP CONSTRAINT task_parks_kind_check;
ALTER TABLE task_parks ADD CONSTRAINT task_parks_kind_check CHECK (kind IN ('escalation','inspect','wait','ask','block','children','decision','message','awaiting_approval'));
