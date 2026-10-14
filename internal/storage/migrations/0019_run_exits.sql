-- Explicit exits (explicit-exits-authority D1-D4, docs/explicit-exits-contract.md): at most one accepted
-- exit per run; a retried exit returns the stored result.
CREATE TABLE run_exits (
 company_id TEXT NOT NULL,
 run_id TEXT PRIMARY KEY REFERENCES runs(id),
 task_id TEXT NOT NULL REFERENCES tasks(id),
 kind TEXT NOT NULL CHECK(kind IN ('submit','handoff','done','ask','block','wait')),
 arguments TEXT NOT NULL,
 ack_seq INTEGER NOT NULL,
 facts TEXT NOT NULL DEFAULT '',
 exit_sha TEXT NOT NULL DEFAULT '',
 result TEXT NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE INDEX run_exits_task ON run_exits(company_id,task_id,created_at);
-- Reviewer approvals bound to a pull request (D4): done after a head change may need a new approval.
CREATE TABLE pr_approvals (
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 pr_number INTEGER NOT NULL,
 approved_head TEXT NOT NULL,
 approver TEXT NOT NULL,
 run_id TEXT NOT NULL,
 created_at INTEGER NOT NULL
);
CREATE INDEX pr_approvals_task ON pr_approvals(company_id,task_id,pr_number);
-- New trigger kinds: directive (authority input, D6) and wake (a wait exit's time). The table is rebuilt to
-- widen its CHECK; sequences are kept (handled_seq and wake_seq refer to them).
CREATE TABLE task_triggers_v2 (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 kind TEXT NOT NULL CHECK(kind IN ('assignment','created_ready','ready','owner_comment','answer','dependency_done','last_child_closed','child_escalated','artifact_approved','review_result','directive','wake')),
 authority INTEGER NOT NULL DEFAULT 0,
 actor TEXT NOT NULL DEFAULT '',
 ref_id TEXT NOT NULL DEFAULT '',
 body TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL
);
INSERT INTO task_triggers_v2(seq,company_id,task_id,kind,authority,actor,ref_id,body,created_at)
 SELECT seq,company_id,task_id,kind,authority,actor,ref_id,body,created_at FROM task_triggers;
DROP TABLE task_triggers;
ALTER TABLE task_triggers_v2 RENAME TO task_triggers;
CREATE INDEX task_triggers_task ON task_triggers(company_id,task_id,seq);
