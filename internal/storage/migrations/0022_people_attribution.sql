-- People attribution (people-attribution): the person who acted, next to the "owner" role agents report to. Rows
-- written before stay without a person (shown as the owner).
ALTER TABLE feed_events ADD COLUMN person text NOT NULL DEFAULT '';
ALTER TABLE task_comments ADD COLUMN person text NOT NULL DEFAULT '';
