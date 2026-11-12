-- People as assignees (people-assignees): a task assigned to a person (member:USER) is never a stall. When it has
-- not changed for the company's people_remind_after_minutes (default 72h) while other tasks wait on it, the person
-- gets one reminder per episode; an episode is the task's updated_at, so a change of the task starts a new one.
ALTER TABLE companies ADD COLUMN people_remind_after_minutes bigint NOT NULL DEFAULT 4320;
CREATE TABLE person_reminders (
 company_id text NOT NULL REFERENCES companies(id),
 task_id text NOT NULL,
 episode bigint NOT NULL,
 sent_at bigint NOT NULL,
 PRIMARY KEY (company_id, task_id, episode),
 FOREIGN KEY (company_id, task_id) REFERENCES tasks(company_id, id)
);
