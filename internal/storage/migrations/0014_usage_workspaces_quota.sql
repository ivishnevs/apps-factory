-- Run usage (runtime-drivers 2.5): the last cumulative Event.usage of the run, as JSON.
ALTER TABLE runs ADD COLUMN usage TEXT NOT NULL DEFAULT '';
-- Task workspaces (task-worktrees D1): metadata reported by the worker; files stay on the node.
CREATE TABLE workspaces (
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 mode TEXT NOT NULL CHECK(mode IN ('shared','worktree','scratch')),
 branch TEXT NOT NULL DEFAULT '',
 node_id TEXT NOT NULL DEFAULT '',
 exit_sha TEXT NOT NULL DEFAULT '',
 sealed_head TEXT NOT NULL DEFAULT '',
 sealed_at INTEGER NOT NULL DEFAULT 0,
 run_id TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(company_id,task_id)
);
-- Quota pauses (runtime-drivers 3.2): scope quota_key with an expiry; `until` 0 means until resumed.
CREATE TABLE pauses_v2 (
 company_id TEXT NOT NULL,
 scope TEXT NOT NULL CHECK(scope IN ('company','project','agent','quota_key')),
 target TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 until INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(company_id,scope,target)
);
INSERT INTO pauses_v2(company_id,scope,target,reason,actor,created_at) SELECT company_id,scope,target,reason,actor,created_at FROM pauses;
DROP TABLE pauses;
ALTER TABLE pauses_v2 RENAME TO pauses;
