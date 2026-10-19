-- quota_paused (runtime-drivers D5, 3.2): an escalation whose scope is a quota key, not a task. task_id becomes
-- nullable (NULL for a quota key; ref_id names the key); at most one open escalation per (reason, key) without a task.
CREATE TABLE escalations_v2 (
 company_id TEXT NOT NULL,
 id TEXT PRIMARY KEY,
 task_id TEXT REFERENCES tasks(id),
 reason TEXT NOT NULL,
 ref_id TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL,
 answers TEXT NOT NULL,
 target TEXT NOT NULL,
 message_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('open','answered','cleared')),
 answer TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 closed_at INTEGER NOT NULL DEFAULT 0
);
INSERT INTO escalations_v2(company_id,id,task_id,reason,ref_id,summary,answers,target,message_id,status,answer,actor,created_at,closed_at)
 SELECT company_id,id,task_id,reason,ref_id,summary,answers,target,message_id,status,answer,actor,created_at,closed_at FROM escalations;
DROP TABLE escalations;
ALTER TABLE escalations_v2 RENAME TO escalations;
CREATE UNIQUE INDEX escalation_open ON escalations(company_id,task_id,reason,ref_id) WHERE status='open';
CREATE UNIQUE INDEX escalation_open_scope ON escalations(company_id,reason,ref_id) WHERE status='open' AND task_id IS NULL;
CREATE INDEX escalation_message ON escalations(message_id);
