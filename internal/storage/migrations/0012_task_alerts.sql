-- Stall alerts (reliable-recovery D8): one active alert per episode, raised and cleared by Core's sweep.
CREATE TABLE task_alerts (
 id TEXT PRIMARY KEY,
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 kind TEXT NOT NULL CHECK(kind IN ('stalled','idle')),
 ref_id TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('active','cleared')),
 raised_at INTEGER NOT NULL,
 cleared_at INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX active_alert ON task_alerts(task_id,kind,ref_id) WHERE status='active';
CREATE INDEX task_alerts_company ON task_alerts(company_id,kind,status);
