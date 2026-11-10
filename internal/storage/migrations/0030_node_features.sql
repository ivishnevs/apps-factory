-- Node features (work-and-conversation-runs): runtime options a node's drivers understand beyond the original
-- set. Core leaves an option out of a claimed run's spec when the claiming node does not list it, so a node that
-- predates the option keeps running with its runtime's default instead of refusing the run.
ALTER TABLE nodes ADD COLUMN features jsonb NOT NULL DEFAULT '[]'::jsonb;
