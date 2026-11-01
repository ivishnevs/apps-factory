-- Company owner tokens (company-self-service 4.1): a company created through Core gets its own owner token, so
-- its owner administers that company only; the operator's FACTORY_TOKEN creates companies. Only a hash is kept.
CREATE TABLE owner_tokens (
 company_id text NOT NULL REFERENCES companies(id),
 id text NOT NULL,
 name text NOT NULL DEFAULT '',
 hash text NOT NULL UNIQUE,
 created_at bigint NOT NULL,
 revoked_at bigint NOT NULL DEFAULT 0,
 last_used_at bigint NOT NULL DEFAULT 0,
 PRIMARY KEY (company_id, id)
);
