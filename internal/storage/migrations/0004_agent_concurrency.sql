-- One live run per agent was a unique index, which refused a second claim with a duplicate key even when the
-- agent's max_concurrent_runs allowed it. The caps are checked at dispatch and claim under the company lock;
-- the index stays only as a lookup.
DROP INDEX IF EXISTS agent_slot;
CREATE INDEX agent_live_runs ON runs(company_id, agent) WHERE status IN ('CLAIMED','RUNNING','CANCELLING');
