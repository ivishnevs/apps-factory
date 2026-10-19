-- Node health events (node-agent D5, D7): the state behind one feed event per episode.
-- disk_alerts: bit 1 node.low_disk raised, bit 2 node.disk_critical raised; both re-armed when health.disk is ok.
ALTER TABLE nodes ADD COLUMN disk_alerts INTEGER NOT NULL DEFAULT 0;
-- core_paused: Core answers pause (critical disk); its changes are node.paused / node.resumed.
ALTER TABLE nodes ADD COLUMN core_paused INTEGER NOT NULL DEFAULT 0;
-- failed_checks: {check: detail} of the node's failing self-checks; a new key is node.check_failed{check}.
ALTER TABLE nodes ADD COLUMN failed_checks TEXT NOT NULL DEFAULT '{}';
-- starts: unix times of the node's process starts in the restart-loop window; restart_alerted: node.restart_loop
-- raised, re-armed after a window without a restart.
ALTER TABLE nodes ADD COLUMN starts TEXT NOT NULL DEFAULT '[]';
ALTER TABLE nodes ADD COLUMN restart_alerted INTEGER NOT NULL DEFAULT 0;
-- offline_alerted: the attention node.offline (offline longer than 10 min) was raised for this silence.
ALTER TABLE nodes ADD COLUMN offline_alerted INTEGER NOT NULL DEFAULT 0;
