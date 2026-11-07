-- node-runtime-accounts 3.3: a person continues with a node's changed runtime account from the web; the node gets
-- the acceptance in its next poll answer.
CREATE TABLE node_account_accepts (
 company_id text NOT NULL REFERENCES companies(id),
 node_id text NOT NULL,
 runtime text NOT NULL,
 created_at bigint NOT NULL,
 PRIMARY KEY (company_id, node_id, runtime)
);
