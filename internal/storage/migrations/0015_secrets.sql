-- core-secret-store D1: one company key per company, wrapped by Core's master key, and the company's secrets,
-- each AES-256-GCM under the company key with company|name|version as additional data (the nonce leads the
-- ciphertext). No value is stored in the clear anywhere.
CREATE TABLE company_keys (
 company_id text PRIMARY KEY REFERENCES companies(id) ON DELETE CASCADE,
 wrapped bytea NOT NULL,
 key_version bigint NOT NULL DEFAULT 1,
 created_at bigint NOT NULL
);
CREATE TABLE company_secrets (
 company_id text NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
 name text NOT NULL,
 kind text NOT NULL CHECK (kind IN ('string','file')),
 version bigint NOT NULL,
 ciphertext bytea NOT NULL,
 updated_by text NOT NULL,
 updated_at bigint NOT NULL,
 PRIMARY KEY (company_id, name)
);
