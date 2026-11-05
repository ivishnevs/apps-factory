-- Team invitations (team-invitations): a person may belong to companies of other accounts. A membership keeps the
-- company's account (its billing owner) and refers to the user alone. An invitation is a single-use link an owner
-- creates for a role; only the hash of its token is kept.
ALTER TABLE memberships DROP CONSTRAINT memberships_account_id_user_id_fkey;
ALTER TABLE memberships ADD CONSTRAINT memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

CREATE TABLE invitations (
 id text PRIMARY KEY,
 company_id text NOT NULL REFERENCES companies(id),
 token_hash text NOT NULL UNIQUE,
 role text NOT NULL CHECK (role IN ('admin','member')),
 created_by text NOT NULL,
 created_at bigint NOT NULL,
 expires_at bigint NOT NULL,
 accepted_by text NOT NULL DEFAULT '',
 accepted_at bigint NOT NULL DEFAULT 0,
 revoked_at bigint NOT NULL DEFAULT 0
);
CREATE INDEX invitations_company ON invitations(company_id, created_at);
