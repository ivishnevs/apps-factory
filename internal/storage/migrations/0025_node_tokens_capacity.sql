-- Per-machine node tokens (node-agent 1.1): issued by the owner, company-scoped, stored as a SHA-256 hash.
-- node_id is bound at issue or by the token's first register; afterwards only that node may use the token.
CREATE TABLE node_tokens (
 company_id TEXT NOT NULL,
 id TEXT NOT NULL,
 name TEXT NOT NULL DEFAULT '',
 node_id TEXT NOT NULL DEFAULT '',
 hash TEXT NOT NULL UNIQUE,
 created_at INTEGER NOT NULL,
 revoked_at INTEGER NOT NULL DEFAULT 0,
 last_used_at INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(company_id,id)
);
-- node_held (node-agent 2.2): the node's last inventory listed the run unended (claimed, starting or
-- running), so it holds its slot and resource classes until the node reports it ended (after its seal).
ALTER TABLE runs ADD COLUMN node_held INTEGER NOT NULL DEFAULT 0;
