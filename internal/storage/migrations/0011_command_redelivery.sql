-- Redelivery of run commands (trigger-log-scheduling D5): how many commands a run has been sent and
-- when the last one was enqueued, so Core can resend a lost one with backoff.
ALTER TABLE runs ADD COLUMN command_count INTEGER NOT NULL DEFAULT 1;
ALTER TABLE runs ADD COLUMN command_at INTEGER NOT NULL DEFAULT 0;
UPDATE runs SET command_at=created_at;
CREATE INDEX queued_runs ON runs(company_id,status,command_at) WHERE status='QUEUED';
-- Backoff is measured from when a command was published, not when it was enqueued.
ALTER TABLE outbox ADD COLUMN sent_at INTEGER NOT NULL DEFAULT 0;
