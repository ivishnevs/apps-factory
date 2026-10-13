-- Owner pauses (waits-events-cost-cli 5.2): no new dispatch in a paused scope. The kill switch is a
-- company pause plus revoking every live run; a revoked run returns its task to READY instead of
-- cancelling it (reliable-recovery `revoked`, without the new outcome model yet).
CREATE TABLE pauses (
 company_id TEXT NOT NULL,
 scope TEXT NOT NULL CHECK(scope IN ('company','project','agent')),
 target TEXT NOT NULL DEFAULT '',
 reason TEXT NOT NULL DEFAULT '',
 actor TEXT NOT NULL DEFAULT '',
 created_at INTEGER NOT NULL,
 PRIMARY KEY(company_id,scope,target)
);
ALTER TABLE runs ADD COLUMN revoked INTEGER NOT NULL DEFAULT 0;
