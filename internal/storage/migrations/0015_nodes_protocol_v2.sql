-- Runtime protocol v2 (docs/runtime-protocol-v2.md): registered nodes, two-phase claim, inventory heartbeat.
-- Core identity: one incarnation per database; a restored backup gets a new one (Store.Open).
CREATE TABLE core_meta (
 key TEXT PRIMARY KEY,
 value TEXT NOT NULL
);
CREATE TABLE nodes (
 company_id TEXT NOT NULL,
 id TEXT NOT NULL,
 name TEXT NOT NULL,
 epoch INTEGER NOT NULL,
 protocol_version INTEGER NOT NULL,
 slots INTEGER NOT NULL,
 drivers TEXT NOT NULL DEFAULT '[]',
 resources TEXT NOT NULL DEFAULT '{}',
 labels TEXT NOT NULL DEFAULT '[]',
 health TEXT NOT NULL DEFAULT '{}',
 registered_at INTEGER NOT NULL,
 last_seen INTEGER NOT NULL,
 online INTEGER NOT NULL DEFAULT 1,
 PRIMARY KEY(company_id,id)
);
-- A v2 run is CLAIMED while status='RUNNING' and run_started=0: it holds its agent, project and node
-- capacity like a running run, and must call /start before start_by.
ALTER TABLE runs ADD COLUMN protocol INTEGER NOT NULL DEFAULT 1;
ALTER TABLE runs ADD COLUMN node_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN epoch INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN claim_id TEXT NOT NULL DEFAULT '';
ALTER TABLE runs ADD COLUMN slot INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN start_by INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN run_started INTEGER NOT NULL DEFAULT 0;
-- end_applied: a terminal event of the run was applied (the heartbeat answer's acked).
ALTER TABLE runs ADD COLUMN end_applied INTEGER NOT NULL DEFAULT 0;
CREATE INDEX runs_node ON runs(company_id,node_id,epoch) WHERE node_id != '';
-- cleanup_listed counts heartbeat answers that listed a closed task's worktree for removal.
ALTER TABLE workspaces ADD COLUMN cleanup_listed INTEGER NOT NULL DEFAULT 0;
