-- Tenancy: every domain row belongs to a company. Existing rows join the 'default' company.
-- Row IDs stay globally unique on SQLite; composite (company_id, id) keys arrive with Postgres.
CREATE TABLE companies (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at INTEGER NOT NULL);
INSERT INTO companies VALUES('default','Default',strftime('%s','now'));
ALTER TABLE runs ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE outbox ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE events ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE projects ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE tasks ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE task_dependencies ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE task_attempts ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE task_comments ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE task_artifacts ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE entity_events ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE tool_calls ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE conversations ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE messages ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE message_runs ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE approvals ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE artifact_versions ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE reports ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE decisions ADD COLUMN company_id TEXT NOT NULL DEFAULT 'default';
-- Agent names are per company catalog.
DROP INDEX agent_slot;
CREATE UNIQUE INDEX agent_slot ON runs(company_id,agent) WHERE status IN ('RUNNING','CANCELLING');
CREATE INDEX runs_company ON runs(company_id,created_at);
CREATE INDEX tasks_company ON tasks(company_id,status,priority DESC,created_at,id);
CREATE INDEX projects_company ON projects(company_id,id);
CREATE INDEX messages_company ON messages(company_id,delivery_status,created_at);
CREATE INDEX approvals_company ON approvals(company_id,status);
-- Request IDs are chosen by clients, so idempotency keys are per company.
CREATE TABLE mutation_results_v2 (
 company_id TEXT NOT NULL, scope TEXT NOT NULL, request_id TEXT NOT NULL, fingerprint TEXT NOT NULL, response BLOB NOT NULL,
 PRIMARY KEY(company_id,scope,request_id)
);
INSERT INTO mutation_results_v2 SELECT 'default',scope,request_id,fingerprint,response FROM mutation_results;
DROP TABLE mutation_results;
ALTER TABLE mutation_results_v2 RENAME TO mutation_results;
