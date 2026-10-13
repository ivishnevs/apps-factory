CREATE TABLE projects (
 id TEXT PRIMARY KEY, workspace TEXT NOT NULL UNIQUE, version INTEGER NOT NULL, body BLOB NOT NULL
);
CREATE TABLE tasks (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id),
 status TEXT NOT NULL, assignee TEXT NOT NULL, priority INTEGER NOT NULL,
 version INTEGER NOT NULL, body BLOB NOT NULL, created_at INTEGER NOT NULL
);
CREATE INDEX ready_tasks ON tasks(status, priority DESC, created_at, id);
CREATE TABLE task_dependencies (
 task_id TEXT NOT NULL REFERENCES tasks(id), depends_on TEXT NOT NULL REFERENCES tasks(id),
 PRIMARY KEY(task_id,depends_on), CHECK(task_id != depends_on)
);
ALTER TABLE runs ADD COLUMN task_id TEXT REFERENCES tasks(id);
ALTER TABLE runs ADD COLUMN started_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE runs ADD COLUMN finished_at INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX active_task_run ON runs(task_id)
 WHERE task_id IS NOT NULL AND status IN ('QUEUED','RUNNING','CANCELLING','UNKNOWN');
CREATE TABLE task_attempts (
 run_id TEXT PRIMARY KEY REFERENCES runs(id), task_id TEXT NOT NULL REFERENCES tasks(id),
 attempt INTEGER NOT NULL, UNIQUE(task_id,attempt)
);
CREATE TABLE task_comments (
 id TEXT PRIMARY KEY, task_id TEXT NOT NULL REFERENCES tasks(id), author TEXT NOT NULL,
 text TEXT NOT NULL, created_at INTEGER NOT NULL
);
CREATE TABLE task_artifacts (
 task_id TEXT NOT NULL REFERENCES tasks(id), run_id TEXT NOT NULL REFERENCES runs(id),
 id TEXT NOT NULL, version TEXT NOT NULL, uri TEXT NOT NULL,
 PRIMARY KEY(task_id,run_id,id,version)
);
CREATE TABLE entity_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 project_id TEXT NOT NULL, entity_id TEXT NOT NULL, body BLOB NOT NULL
);
CREATE TABLE mutation_results (
 scope TEXT NOT NULL, request_id TEXT NOT NULL, fingerprint TEXT NOT NULL, response BLOB NOT NULL,
 PRIMARY KEY(scope,request_id)
);
PRAGMA user_version = 2;
