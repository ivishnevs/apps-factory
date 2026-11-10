-- work-and-conversation-runs: nodes offer conversation slots on top of their work slots (a node that does not report
-- them has none), and a queued conversation run waits for the node holding its session at most the company's
-- conversation wait (owner 2026-11-08: 10 minutes) before another node starts it with a fresh session.
ALTER TABLE nodes ADD COLUMN conversation_slots bigint NOT NULL DEFAULT 0;
ALTER TABLE companies ADD COLUMN conversation_wait_minutes bigint NOT NULL DEFAULT 10;
