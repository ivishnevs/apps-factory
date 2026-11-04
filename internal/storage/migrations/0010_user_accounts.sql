-- User accounts (user-accounts D4, D5, D7): people sign in through OIDC; an identity is (provider, subject) and
-- belongs to one user; web sessions are server-side (only the hash of the cookie's random id is kept);
-- memberships gain the admin role; owner tokens record who issued them.
ALTER TABLE memberships DROP CONSTRAINT memberships_role_check;
ALTER TABLE memberships ADD CONSTRAINT memberships_role_check CHECK (role IN ('owner','admin','member'));

ALTER TABLE users ADD COLUMN name text NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN last_company_id text NOT NULL DEFAULT '';

CREATE TABLE identities (
 provider text NOT NULL,
 subject text NOT NULL,
 user_id text NOT NULL REFERENCES users(id),
 email text NOT NULL DEFAULT '',
 created_at bigint NOT NULL,
 PRIMARY KEY (provider, subject)
);
CREATE INDEX identities_user ON identities(user_id);

CREATE TABLE sessions (
 id_hash text PRIMARY KEY,
 user_id text NOT NULL REFERENCES users(id),
 created_at bigint NOT NULL,
 expires_at bigint NOT NULL,
 last_seen_at bigint NOT NULL,
 revoked_at bigint NOT NULL DEFAULT 0
);
CREATE INDEX sessions_user ON sessions(user_id);

ALTER TABLE owner_tokens ADD COLUMN created_by text NOT NULL DEFAULT '';
