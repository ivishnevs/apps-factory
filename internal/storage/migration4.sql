CREATE TABLE conversations (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), body BLOB NOT NULL
);
CREATE TABLE messages (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id),
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 task_id TEXT REFERENCES tasks(id), sender TEXT NOT NULL, recipient TEXT NOT NULL,
 message_type TEXT NOT NULL, status TEXT NOT NULL, delivery_status TEXT NOT NULL,
 version INTEGER NOT NULL, body BLOB NOT NULL, created_at INTEGER NOT NULL,
 fingerprint TEXT NOT NULL
);
CREATE INDEX pending_messages ON messages(delivery_status,created_at,id);
CREATE INDEX conversation_messages ON messages(conversation_id,created_at,id);
CREATE UNIQUE INDEX unique_question ON messages(conversation_id,fingerprint) WHERE message_type='question';
CREATE UNIQUE INDEX unique_task_question ON messages(task_id,fingerprint) WHERE message_type='question' AND task_id IS NOT NULL;
CREATE TABLE message_runs (
 run_id TEXT PRIMARY KEY REFERENCES runs(id), message_id TEXT NOT NULL REFERENCES messages(id)
);
PRAGMA user_version=4;
