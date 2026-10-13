CREATE TABLE reports (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL, task_id TEXT, run_id TEXT,
 kind TEXT NOT NULL, source TEXT NOT NULL, source_key TEXT UNIQUE, body BLOB NOT NULL
);
CREATE INDEX reports_project ON reports(project_id,kind);
CREATE TABLE decisions (
 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id), topic TEXT NOT NULL,
 status TEXT NOT NULL, version INTEGER NOT NULL, body BLOB NOT NULL
);
CREATE UNIQUE INDEX approved_decision_topic ON decisions(project_id,topic) WHERE status='approved';
CREATE INDEX decisions_project ON decisions(project_id,status,topic);
