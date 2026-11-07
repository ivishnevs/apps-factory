-- Session affinity (session-affinity): a run continuing a session is taken only by the node holding the session
-- (the node of the session's last run) while that node is active; when it becomes inactive (offline, paused,
-- removed, unable to run the agent) a release timer starts, and after the company's timeout another node may take
-- the run and starts fresh. session_locks keeps the timers; the holder itself is read from runs.
ALTER TABLE companies ADD COLUMN session_affinity_minutes bigint NOT NULL DEFAULT 90;
CREATE TABLE session_locks (
 company_id text NOT NULL REFERENCES companies(id),
 session_id text NOT NULL,
 node_id text NOT NULL,
 inactive_since bigint NOT NULL,
 PRIMARY KEY (company_id, session_id)
);
