-- Parks replace stored waits (trigger-log-scheduling D2, 3.1): a blocking question is an active ask park and
-- an unapproved required artifact a derived reason; neither is stored as status BLOCKED or wait_message_id.
-- Reads derive status BLOCKED with block_reason and wait_message_id from them for compatibility.
-- An ask park remembers the run that asked, so the end of that run knows it waits for an answer.
ALTER TABLE task_parks ADD COLUMN run_id TEXT NOT NULL DEFAULT '';
UPDATE task_parks SET run_id=coalesce((SELECT json_extract(t.body,'$.current_run_id') FROM tasks t
 WHERE t.id=task_parks.task_id AND json_extract(t.body,'$.wait_message_id')=task_parks.ref_id),'')
WHERE kind='ask';
-- Tasks held by a question or an artifact approval are READY: the park or the artifacts hold them.
UPDATE tasks SET status='READY',body=json_set(json_remove(body,'$.block_reason'),'$.status','READY')
WHERE status='BLOCKED' AND (json_extract(body,'$.block_reason') LIKE 'message:%' OR json_extract(body,'$.block_reason') LIKE 'artifact:%');
UPDATE tasks SET body=json_remove(body,'$.wait_message_id') WHERE json_extract(body,'$.wait_message_id') IS NOT NULL;
