-- Node identity (node-identity 1.2): a node proves itself with its own keys. An owner issues a single-use
-- enrollment code (only its hash is kept); the node sends its public keys with the code and they stay pending
-- until an owner confirms their fingerprint. node_id is unique across companies, so a signature's key id finds
-- one node and its company.
CREATE TABLE node_enrollments (
 company_id text NOT NULL REFERENCES companies(id),
 id text NOT NULL,
 name text NOT NULL,
 hash text NOT NULL UNIQUE,
 created_at bigint NOT NULL,
 expires_at bigint NOT NULL,
 used_at bigint NOT NULL DEFAULT 0,
 PRIMARY KEY (company_id, id)
);

CREATE TABLE node_keys (
 node_id text PRIMARY KEY,
 company_id text NOT NULL REFERENCES companies(id),
 name text NOT NULL,
 sign_pub text NOT NULL UNIQUE,
 box_pub text NOT NULL,
 fingerprint text NOT NULL,
 status text NOT NULL CHECK (status IN ('pending', 'active')),
 created_at bigint NOT NULL,
 approved_at bigint NOT NULL DEFAULT 0
);
CREATE INDEX node_keys_company ON node_keys(company_id, created_at);
