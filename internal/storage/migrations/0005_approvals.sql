CREATE TABLE approvals (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), agent TEXT NOT NULL,
 task_id TEXT NOT NULL, fingerprint TEXT NOT NULL, message_id TEXT NOT NULL UNIQUE REFERENCES messages(id),
 status TEXT NOT NULL, body BLOB NOT NULL,
 UNIQUE(project_id,agent,task_id,fingerprint)
);
ALTER TABLE tool_calls ADD COLUMN approval_id TEXT REFERENCES approvals(id);
