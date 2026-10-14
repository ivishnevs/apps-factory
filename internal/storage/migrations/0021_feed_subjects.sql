-- Company-wide feed events (no project) were queued with an empty subject token
-- (factory.events.feed..<type>), which JetStream refuses; the outbox publisher stopped at the first one.
UPDATE outbox SET subject=replace(subject,'factory.events.feed..','factory.events.feed._company.') WHERE sent=0 AND subject LIKE 'factory.events.feed..%';
-- The outbox publisher no longer stops at a message it cannot publish: the message is retried with a backoff
-- (attempts, next_at) and the error kept (last_error), while the rest of the queue moves on.
ALTER TABLE outbox ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE outbox ADD COLUMN next_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE outbox ADD COLUMN last_error TEXT NOT NULL DEFAULT '';
