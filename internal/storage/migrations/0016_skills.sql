-- skill-registry D2-D4: skill sources per company (scope 'factory' holds the built-in skills), their indexed skills
-- (one row per source, name and content hash; the content is a blob of the scope) and node sync jobs for private
-- sources.
CREATE TABLE skill_sources (
 scope text NOT NULL,
 id text NOT NULL,
 kind text NOT NULL CHECK (kind IN ('builtin','git','upload')),
 url text NOT NULL DEFAULT '',
 ref text NOT NULL DEFAULT '',
 subpath text NOT NULL DEFAULT '',
 access text NOT NULL DEFAULT 'public' CHECK (access IN ('public','node')),
 update_mode text NOT NULL DEFAULT 'pinned' CHECK (update_mode IN ('pinned','track')),
 commit_id text NOT NULL DEFAULT '',
 synced_at bigint NOT NULL DEFAULT 0,
 status text NOT NULL DEFAULT 'ok' CHECK (status IN ('ok','syncing','waiting_for_node','failed')),
 error text NOT NULL DEFAULT '',
 created_at bigint NOT NULL,
 PRIMARY KEY (scope, id)
);
CREATE TABLE skill_index (
 scope text NOT NULL,
 source_id text NOT NULL,
 name text NOT NULL,
 hash text NOT NULL,
 commit_id text NOT NULL DEFAULT '',
 blob text NOT NULL DEFAULT '',
 description text NOT NULL DEFAULT '',
 categories json NOT NULL DEFAULT '[]',
 frontmatter json NOT NULL DEFAULT '{}',
 files json NOT NULL DEFAULT '[]',
 error text NOT NULL DEFAULT '',
 current bool NOT NULL DEFAULT true,
 indexed_at bigint NOT NULL,
 PRIMARY KEY (scope, source_id, name, hash),
 FOREIGN KEY (scope, source_id) REFERENCES skill_sources(scope, id) ON DELETE CASCADE
);
CREATE INDEX skill_index_hash ON skill_index(scope, hash);
CREATE TABLE skill_sync_jobs (
 company_id text NOT NULL REFERENCES companies(id),
 id text NOT NULL,
 source_id text NOT NULL,
 node_id text NOT NULL DEFAULT '',
 status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','taken','done','failed')),
 error text NOT NULL DEFAULT '',
 created_at bigint NOT NULL,
 updated_at bigint NOT NULL,
 PRIMARY KEY (company_id, id)
);
