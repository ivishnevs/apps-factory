-- Duplicate warnings (task-duplicate-warnings): a strong warning between two open tasks (same_work_item,
-- same_title, original_open) that nobody answered is a row with status open; it shows on both tasks until one
-- closes (the row is deleted) or a side marks the pair not_duplicate (the row stays, so the pair never warns
-- again). task_id is the task whose create or update raised it; reasons and keys are comma-separated.
CREATE TABLE task_duplicates (
 company_id text NOT NULL REFERENCES companies(id),
 task_id text NOT NULL,
 other_id text NOT NULL,
 reasons text NOT NULL DEFAULT '',
 keys text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'open' CHECK (status IN ('open','not_duplicate')),
 created_by text NOT NULL DEFAULT '',
 created_at bigint NOT NULL,
 decided_by text NOT NULL DEFAULT '',
 decided_at bigint NOT NULL DEFAULT 0,
 PRIMARY KEY (company_id, task_id, other_id),
 CHECK (task_id <> other_id),
 FOREIGN KEY (company_id, task_id) REFERENCES tasks(company_id, id),
 FOREIGN KEY (company_id, other_id) REFERENCES tasks(company_id, id)
);
CREATE INDEX task_duplicates_other ON task_duplicates(company_id, other_id);
