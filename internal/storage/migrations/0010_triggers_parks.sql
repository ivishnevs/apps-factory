-- Trigger log and parks (trigger-log-scheduling D1-D2), written in shadow mode: the scheduler still
-- reads task status, but every cause also records its trigger or park so Evaluate can read them.
CREATE TABLE task_triggers (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 kind TEXT NOT NULL CHECK(kind IN ('assignment','created_ready','ready','owner_comment','answer','dependency_done','last_child_closed','child_escalated','artifact_approved','review_result')),
 authority INTEGER NOT NULL DEFAULT 0,
 actor TEXT NOT NULL DEFAULT '',
 ref_id TEXT NOT NULL DEFAULT '',
 body TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL
);
CREATE INDEX task_triggers_task ON task_triggers(company_id,task_id,seq);
CREATE TABLE task_parks (
 id TEXT PRIMARY KEY,
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 kind TEXT NOT NULL CHECK(kind IN ('escalation','inspect','wait','ask','block','children')),
 ref_id TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL,
 park_seq INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('active','cleared','overridden')),
 created_at INTEGER NOT NULL,
 cleared_at INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX active_park ON task_parks(task_id,kind,ref_id) WHERE status='active';
CREATE INDEX task_parks_task ON task_parks(company_id,task_id,status);
ALTER TABLE tasks ADD COLUMN wake_seq INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN handled_seq INTEGER NOT NULL DEFAULT 0;
-- The trigger window a run was briefed on, recorded at claim.
ALTER TABLE runs ADD COLUMN brief_from INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN brief_to INTEGER NOT NULL DEFAULT 0;

-- Backfill: a READY task was released by a ready action that predates the log.
INSERT INTO task_triggers(company_id,task_id,kind,authority,actor,body,created_at)
SELECT company_id,id,'ready',0,'migration','backfilled for a READY task',strftime('%s','now') FROM tasks WHERE status='READY';
UPDATE tasks SET wake_seq=(SELECT max(seq) FROM task_triggers g WHERE g.task_id=tasks.id) WHERE status='READY';
-- Backfill the holds that exist as status, block_reason, wait_message_id or an UNKNOWN run.
INSERT INTO task_parks(id,company_id,task_id,kind,ref_id,reason,park_seq,status,created_at)
SELECT 'migrated-block-'||id,company_id,id,'block','',json_extract(body,'$.block_reason'),wake_seq,'active',strftime('%s','now')
FROM tasks WHERE status='BLOCKED' AND (json_extract(body,'$.block_reason') LIKE 'manual:%' OR json_extract(body,'$.block_reason') LIKE 'openspec:%');
INSERT INTO task_parks(id,company_id,task_id,kind,ref_id,reason,park_seq,status,created_at)
SELECT 'migrated-ask-'||t.id,t.company_id,t.id,'ask',m.id,'waiting for the answer to question '||m.id,t.wake_seq,'active',strftime('%s','now')
FROM tasks t JOIN messages m ON m.id=json_extract(t.body,'$.wait_message_id')
WHERE t.status NOT IN ('DONE','CANCELLED') AND m.status!='ANSWERED';
INSERT INTO task_parks(id,company_id,task_id,kind,ref_id,reason,park_seq,status,created_at)
SELECT 'migrated-inspect-'||t.id,t.company_id,t.id,'inspect',r.id,'run '||r.id||' ended with unknown outcome',t.wake_seq,'active',strftime('%s','now')
FROM tasks t JOIN runs r ON r.id=json_extract(t.body,'$.current_run_id')
WHERE t.status NOT IN ('DONE','CANCELLED') AND r.status='UNKNOWN';
