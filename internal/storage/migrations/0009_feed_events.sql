-- Event feed (trigger-log-scheduling D10): one row per user-visible change, written in the
-- transaction of its cause. Attention rules arrive with waits-events-cost-cli.
CREATE TABLE feed_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 company_id TEXT NOT NULL,
 id TEXT NOT NULL UNIQUE,
 at TEXT NOT NULL,
 kind TEXT NOT NULL,
 project_id TEXT NOT NULL,
 entity_id TEXT NOT NULL,
 task_id TEXT NOT NULL DEFAULT '',
 run_id TEXT NOT NULL DEFAULT '',
 agent TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL DEFAULT '',
 summary TEXT NOT NULL DEFAULT '',
 attention INTEGER NOT NULL DEFAULT 0,
 ref TEXT NOT NULL DEFAULT ''
);
CREATE INDEX feed_project ON feed_events(company_id,project_id,seq);
CREATE INDEX feed_task ON feed_events(company_id,task_id,seq) WHERE task_id!='';
-- Backfill keeps entity_events sequences, so feed cursors handed out before the upgrade stay valid.
INSERT INTO feed_events(seq,company_id,id,at,kind,project_id,entity_id,task_id,run_id,agent,actor,status,summary)
SELECT seq, company_id, id,
 coalesce(json_extract(body,'$.occurred_at'),''),
 coalesce(json_extract(body,'$.type'),''),
 project_id, entity_id,
 CASE WHEN json_extract(body,'$.type') LIKE 'task.%' THEN entity_id
      ELSE coalesce(json_extract(body,'$.payload.task_id'),'') END,
 CASE WHEN json_extract(body,'$.type') LIKE 'run.%' THEN entity_id
      ELSE coalesce(json_extract(body,'$.payload.run_id'), json_extract(body,'$.payload.current_run_id'),'') END,
 coalesce(json_extract(body,'$.payload.assignee'), json_extract(body,'$.payload.agent'),''),
 coalesce(json_extract(body,'$.payload.sender'), json_extract(body,'$.payload.recorded_by'), json_extract(body,'$.payload.created_by'),''),
 coalesce(json_extract(body,'$.payload.status'),''),
 substr(coalesce(nullif(json_extract(body,'$.payload.summary'),''), nullif(json_extract(body,'$.payload.title'),''), json_extract(body,'$.payload.topic'),''),1,4096)
FROM entity_events ORDER BY seq;
