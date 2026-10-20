-- waits-events-cost-cli: timer waits, node pauses, the cost ledger and the Telegram notifier's state.

-- New trigger kinds: wait_ended (a wait's timer, D1; replaces wake) and delegated_closed (hierarchy). The
-- table is rebuilt to widen its CHECK; sequences are kept (handled_seq and wake_seq refer to them).
CREATE TABLE task_triggers_v3 (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 kind TEXT NOT NULL CHECK(kind IN ('assignment','created_ready','ready','owner_comment','answer','dependency_done','last_child_closed','child_escalated','artifact_approved','review_result','directive','wake','wait_ended','delegated_closed')),
 authority INTEGER NOT NULL DEFAULT 0,
 actor TEXT NOT NULL DEFAULT '',
 ref_id TEXT NOT NULL DEFAULT '',
 body TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 addressee TEXT NOT NULL DEFAULT ''
);
INSERT INTO task_triggers_v3(seq,company_id,task_id,kind,authority,actor,ref_id,body,created_at,addressee)
 SELECT seq,company_id,task_id,kind,authority,actor,ref_id,body,created_at,addressee FROM task_triggers;
DROP TABLE task_triggers;
ALTER TABLE task_triggers_v3 RENAME TO task_triggers;
CREATE INDEX task_triggers_task ON task_triggers(company_id,task_id,seq);

-- Wait episodes (D1): consecutive waits of a task without progress. A new wait cannot pass the first
-- deadline; count is limited by the max_waits policy. Progress, authority input or an answer ends it.
CREATE TABLE wait_episodes (
 company_id TEXT NOT NULL,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 deadline INTEGER NOT NULL,
 count INTEGER NOT NULL,
 started_at INTEGER NOT NULL,
 PRIMARY KEY(company_id,task_id)
);

-- Node pauses (owner CLI 5.2): scope node stops claims on that node only.
CREATE TABLE pauses_v3 (
 company_id TEXT NOT NULL,
 scope TEXT NOT NULL CHECK(scope IN ('company','project','agent','quota_key','node')),
 target TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 until INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(company_id,scope,target)
);
INSERT INTO pauses_v3(company_id,scope,target,reason,actor,created_at,until) SELECT company_id,scope,target,reason,actor,created_at,until FROM pauses;
DROP TABLE pauses;
ALTER TABLE pauses_v3 RENAME TO pauses;

-- Cost ledger (D5): exactly one row per ended run, built from the usage stored on the run. The triggers write
-- the tokens and a reported cost; Core prices the rest from prices.yaml (priced=0 marks rows to look at).
CREATE TABLE run_costs (
 company_id TEXT NOT NULL,
 run_id TEXT PRIMARY KEY,
 task_id TEXT NOT NULL DEFAULT '',
 project_id TEXT NOT NULL DEFAULT '',
 agent TEXT NOT NULL,
 provider TEXT NOT NULL DEFAULT '',
 model TEXT NOT NULL DEFAULT '',
 outcome TEXT NOT NULL DEFAULT '',
 input_tokens INTEGER NOT NULL DEFAULT 0,
 output_tokens INTEGER NOT NULL DEFAULT 0,
 cache_read_tokens INTEGER NOT NULL DEFAULT 0,
 cache_write_tokens INTEGER NOT NULL DEFAULT 0,
 cost_usd REAL NOT NULL DEFAULT 0,
 cost_source TEXT NOT NULL DEFAULT 'none' CHECK(cost_source IN ('reported','price_table','none')),
 recorded_at INTEGER NOT NULL,
 priced INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX run_costs_task ON run_costs(company_id,task_id);
CREATE INDEX run_costs_recorded ON run_costs(company_id,recorded_at);
-- The row of an ended run; model is the model with the most tokens, the costs those the runtime reported.
CREATE VIEW run_cost_source AS
SELECT r.company_id AS company_id, r.id AS run_id, coalesce(r.task_id,'') AS task_id, coalesce(json_extract(r.spec,'$.project'),'') AS project_id,
 r.agent AS agent, coalesce(json_extract(r.spec,'$.provider'),'') AS provider,
 coalesce((SELECT json_extract(u.value,'$.model') FROM json_each(CASE WHEN json_valid(r.usage) THEN r.usage ELSE '[]' END) u
  ORDER BY coalesce(json_extract(u.value,'$.input'),0)+coalesce(json_extract(u.value,'$.output'),0) DESC LIMIT 1),
  coalesce(json_extract(r.spec,'$.runtime_options.model'),'')) AS model,
 r.outcome AS outcome,
 coalesce((SELECT sum(coalesce(json_extract(u.value,'$.input'),0)) FROM json_each(CASE WHEN json_valid(r.usage) THEN r.usage ELSE '[]' END) u),0) AS input_tokens,
 coalesce((SELECT sum(coalesce(json_extract(u.value,'$.output'),0)) FROM json_each(CASE WHEN json_valid(r.usage) THEN r.usage ELSE '[]' END) u),0) AS output_tokens,
 coalesce((SELECT sum(coalesce(json_extract(u.value,'$.cache_read'),0)) FROM json_each(CASE WHEN json_valid(r.usage) THEN r.usage ELSE '[]' END) u),0) AS cache_read_tokens,
 coalesce((SELECT sum(coalesce(json_extract(u.value,'$.cache_write'),0)) FROM json_each(CASE WHEN json_valid(r.usage) THEN r.usage ELSE '[]' END) u),0) AS cache_write_tokens,
 coalesce(r.finished_at,0) AS finished_at
FROM runs r;
CREATE TRIGGER run_cost_ended AFTER UPDATE OF status ON runs WHEN NEW.status='ENDED' AND OLD.status!='ENDED'
BEGIN
 INSERT INTO run_costs(company_id,run_id,task_id,project_id,agent,provider,model,outcome,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,recorded_at)
  SELECT company_id,run_id,task_id,project_id,agent,provider,model,outcome,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,
   CASE WHEN finished_at>0 THEN finished_at ELSE CAST(strftime('%s','now') AS INTEGER) END FROM run_cost_source WHERE run_id=NEW.id
 ON CONFLICT(run_id) DO UPDATE SET outcome=excluded.outcome,model=excluded.model,input_tokens=excluded.input_tokens,output_tokens=excluded.output_tokens,
  cache_read_tokens=excluded.cache_read_tokens,cache_write_tokens=excluded.cache_write_tokens,priced=0;
END;
-- Usage or an outcome that arrives after the end (late terminal evidence) updates the row.
CREATE TRIGGER run_cost_late AFTER UPDATE OF usage, outcome ON runs WHEN NEW.status='ENDED' AND OLD.status='ENDED'
BEGIN
 UPDATE run_costs SET (outcome,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,priced)=
  (SELECT outcome,model,input_tokens,output_tokens,cache_read_tokens,cache_write_tokens,0 FROM run_cost_source WHERE run_id=NEW.id)
 WHERE run_id=NEW.id;
END;

-- Telegram notifier (D4): its small state (bound chat, cursors, reminders) and the updates it applied.
CREATE TABLE notify_state (
 company_id TEXT NOT NULL,
 channel TEXT NOT NULL,
 state TEXT NOT NULL,
 PRIMARY KEY(company_id,channel)
);
CREATE TABLE notify_updates (
 company_id TEXT NOT NULL,
 channel TEXT NOT NULL,
 update_id INTEGER NOT NULL,
 applied_at INTEGER NOT NULL,
 PRIMARY KEY(company_id,channel,update_id)
);

-- Reporting policy (D2): a missing mandatory report is a reminder in the agent's next brief (shown_run set
-- by the claim that briefed it).
CREATE TABLE report_reminders (
 company_id TEXT NOT NULL,
 id TEXT PRIMARY KEY,
 agent TEXT NOT NULL,
 task_id TEXT NOT NULL DEFAULT '',
 run_id TEXT NOT NULL DEFAULT '',
 text TEXT NOT NULL,
 created_at INTEGER NOT NULL,
 shown_run TEXT NOT NULL DEFAULT ''
);
CREATE INDEX report_reminders_agent ON report_reminders(company_id,agent,shown_run);
