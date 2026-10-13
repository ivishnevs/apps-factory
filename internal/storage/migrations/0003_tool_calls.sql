CREATE TABLE tool_calls (
 id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL REFERENCES runs(id),
 agent TEXT NOT NULL,
 project_id TEXT NOT NULL,
 tool TEXT NOT NULL,
 request_id TEXT NOT NULL,
 status INTEGER NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE INDEX tool_calls_run ON tool_calls(run_id,created_at);
