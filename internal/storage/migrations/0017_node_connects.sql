-- node-install D5: a node's connection request, approved by a signed-in owner or admin for one of their companies.
-- No company until approved; the keys move to node_keys (active) on approval.
CREATE TABLE node_connects (
 id text PRIMARY KEY,
 node_id text NOT NULL,
 name text NOT NULL,
 os text NOT NULL DEFAULT '',
 arch text NOT NULL DEFAULT '',
 address text NOT NULL DEFAULT '',
 sign_pub text NOT NULL,
 box_pub text NOT NULL,
 fingerprint text NOT NULL,
 code text NOT NULL,
 status text NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
 company_id text NOT NULL DEFAULT '',
 created_at bigint NOT NULL,
 expires_at bigint NOT NULL,
 decided_at bigint NOT NULL DEFAULT 0
);
CREATE INDEX node_connects_expiry ON node_connects(expires_at);
