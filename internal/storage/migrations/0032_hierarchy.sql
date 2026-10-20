-- Hierarchy (agent-hierarchy): task pauses by an authority over the task, and new trigger kinds (message on a
-- conversation task, delegated_closed). task_triggers (as 0031 left it) is recreated without a CHECK on kind
-- (Core validates the kinds it writes).
CREATE TABLE task_pauses (
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 actor TEXT NOT NULL,
 -- rank of the pauser: 0 the owner, otherwise the agent's depth in the tree (1 reports to the owner)
 rank INTEGER NOT NULL,
 reason TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 PRIMARY KEY(company_id,task_id)
);
CREATE TABLE task_triggers_v3 (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 kind TEXT NOT NULL,
 authority INTEGER NOT NULL DEFAULT 0,
 actor TEXT NOT NULL DEFAULT '',
 ref_id TEXT NOT NULL DEFAULT '',
 body TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 addressee TEXT NOT NULL DEFAULT ''
);
-- The table rebuild keeps its rows (sequences are referenced by handled_seq and wake_seq).
INSERT INTO task_triggers_v3(seq,company_id,task_id,kind,authority,actor,ref_id,body,created_at,addressee)
 SELECT seq,company_id,task_id,kind,authority,actor,ref_id,body,created_at,addressee FROM task_triggers;
DROP TABLE task_triggers;
ALTER TABLE task_triggers_v3 RENAME TO task_triggers;
CREATE INDEX task_triggers_task ON task_triggers(company_id,task_id,seq);
