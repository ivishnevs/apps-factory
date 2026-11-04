-- http-node-transport D5: the highest per-run event sequence Core applied from the run's node; D9: offers read
-- queued runs per company, oldest first.
ALTER TABLE runs ADD COLUMN events_seq bigint NOT NULL DEFAULT 0;
CREATE INDEX runs_queued ON runs(company_id, created_at, seq) WHERE status='QUEUED';
