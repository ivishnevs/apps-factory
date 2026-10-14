-- Escalations (reliable-recovery D5): a park of kind escalation on the task plus a question with the
-- allowed answers to the target (a manager agent, else the owner). At most one open escalation per
-- (task, reason, ref).
CREATE TABLE escalations (
 company_id TEXT NOT NULL,
 id TEXT PRIMARY KEY,
 task_id TEXT NOT NULL REFERENCES tasks(id),
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
CREATE UNIQUE INDEX escalation_open ON escalations(company_id,task_id,reason,ref_id) WHERE status='open';
CREATE INDEX escalation_message ON escalations(message_id);
