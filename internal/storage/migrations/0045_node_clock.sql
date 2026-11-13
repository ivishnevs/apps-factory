-- node-wake-recovery: clock_seen is the contact Core credits to a node whose heartbeats it refuses only for the
-- signature time window (at most one NodeOffline past last_seen); clock_skew_alerted keeps node.clock_skew to once per
-- episode.
ALTER TABLE nodes ADD COLUMN clock_seen bigint NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN clock_skew_alerted bigint NOT NULL DEFAULT 0;
