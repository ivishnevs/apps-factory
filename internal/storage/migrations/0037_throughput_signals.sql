-- Throughput signals: the stall sweep compares queued work with idle node capacity, using the queued reasons and the
-- dependency graph. A cause it sees is a row here from the sweep that first saw it (since) until a sweep no longer
-- sees it: one episode. task_id anchors the episode's owner alert (kind throughput) on one affected task, chosen when
-- the cause first appears, so the alert stays one alert while the affected tasks change.
CREATE TABLE throughput_causes (
 company_id text NOT NULL REFERENCES companies(id),
 project_id text NOT NULL,
 key text NOT NULL,
 task_id text NOT NULL,
 since bigint NOT NULL,
 PRIMARY KEY (company_id, project_id, key),
 FOREIGN KEY (company_id, task_id) REFERENCES tasks(company_id, id)
);

-- How long work may wait while capacity is idle or unusable before the project's manager gets the notice and the
-- owner the alert (default 30 minutes).
ALTER TABLE companies ADD COLUMN throughput_after_minutes bigint NOT NULL DEFAULT 30;

-- The owner's alert for a throughput cause. The kind is added to whatever the constraint allows now (other changes
-- extend it too).
DO $$
DECLARE def text;
BEGIN
 SELECT pg_get_constraintdef(oid) INTO def FROM pg_constraint WHERE conname='task_alerts_kind_check' AND conrelid='task_alerts'::regclass;
 IF def IS NOT NULL AND position('throughput' in def)=0 THEN
  ALTER TABLE task_alerts DROP CONSTRAINT task_alerts_kind_check;
  EXECUTE 'ALTER TABLE task_alerts ADD CONSTRAINT task_alerts_kind_check ' || replace(def, 'ARRAY[', 'ARRAY[''throughput''::text, ');
 END IF;
END $$;
