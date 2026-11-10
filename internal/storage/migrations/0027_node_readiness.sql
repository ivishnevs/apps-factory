-- node-readiness: a node's environment failure charges the node, not the task. A run that ends setup_failed for a
-- node-side cause (repository access, worktree, runtime binary, sandbox, missing tool) records its kind in
-- runs.node_fault and does not count as an attempt; the node gets a per-project readiness fault and no runs of the
-- project until its check passes again.
ALTER TABLE runs ADD COLUMN node_fault text NOT NULL DEFAULT '';
-- readiness: the node reports per-project checks in its heartbeat (older nodes do not).
ALTER TABLE nodes ADD COLUMN readiness boolean NOT NULL DEFAULT false;
-- One row per node and project the node was checked for or failed a run of. faults '[]' is ready. Core asks the node
-- to check again by raising generation; a report older than min_generation is ignored (it predates a run's fault);
-- checked_generation is the newest generation the node answered (older: the check is pending).
CREATE TABLE node_readiness (
 company_id text NOT NULL REFERENCES companies(id),
 node_id text NOT NULL,
 project_id text NOT NULL,
 generation bigint NOT NULL DEFAULT 1,
 min_generation bigint NOT NULL DEFAULT 0,
 checked_generation bigint NOT NULL DEFAULT 0,
 recheck_at bigint NOT NULL DEFAULT 0,
 faults jsonb NOT NULL DEFAULT '[]',
 source text NOT NULL DEFAULT 'probe' CHECK (source IN ('probe','run')),
 run_id text NOT NULL DEFAULT '',
 since bigint NOT NULL DEFAULT 0,
 checked_at bigint NOT NULL DEFAULT 0,
 PRIMARY KEY (company_id, node_id, project_id)
);
-- The owner's inbox alert of a task no node is ready to run.
ALTER TABLE task_alerts DROP CONSTRAINT task_alerts_kind_check;
ALTER TABLE task_alerts ADD CONSTRAINT task_alerts_kind_check CHECK (kind IN ('stalled','idle','no_progress','sweep_failed','no_ready_node'));
