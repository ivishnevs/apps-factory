-- node-packaging-updates (design C, tasks 2.2 and 3.4): nodes update themselves to the release Core hands out.
-- node_update_policy: auto (default, owner 2026-11-12) updates a node that is behind at its next drain; approve waits for
-- an owner or admin per node. A node reports its platform (the package it updates to) and its update (update_report:
-- downloading, testing, draining, switching or failed, with Core's time of the state). update_approved is the release
-- approved for the node (approve policy, or a retry after a failure) at update_approved_at; update_now the release the
-- owner wants installed at once (the drain interrupts live runs); update_asked the release node_update_available was
-- raised for.
ALTER TABLE companies ADD COLUMN node_update_policy text NOT NULL DEFAULT 'auto' CHECK (node_update_policy IN ('auto','approve'));
ALTER TABLE nodes ADD COLUMN platform text NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN update_report jsonb NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE nodes ADD COLUMN update_approved text NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN update_approved_at bigint NOT NULL DEFAULT 0;
ALTER TABLE nodes ADD COLUMN update_now text NOT NULL DEFAULT '';
ALTER TABLE nodes ADD COLUMN update_asked text NOT NULL DEFAULT '';
