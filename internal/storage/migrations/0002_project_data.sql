-- Core-owned project data (core-owned-project-data): contents live in the blob store, addressed by SHA-256
-- per company; these tables hold what references them.

-- Every blob a company stored (the blob store holds the bytes).
CREATE TABLE blobs (
 company_id text NOT NULL REFERENCES companies(id),
 sha256 text NOT NULL,
 size bigint NOT NULL,
 created_at bigint NOT NULL,
 PRIMARY KEY (company_id, sha256)
);

-- OpenSpec documents of a project: every version of every path, the current one flagged; a deletion is a
-- version with deleted=1 and no content.
CREATE TABLE spec_documents (
 company_id text NOT NULL REFERENCES companies(id),
 project_id text NOT NULL,
 path text NOT NULL,
 version bigint NOT NULL,
 sha256 text NOT NULL DEFAULT '',
 size bigint NOT NULL DEFAULT 0,
 deleted bigint NOT NULL DEFAULT 0,
 current bigint NOT NULL DEFAULT 1,
 updated_by text NOT NULL,
 run_id text NOT NULL DEFAULT '',
 updated_at bigint NOT NULL,
 PRIMARY KEY (company_id, project_id, path, version),
 FOREIGN KEY (company_id, project_id) REFERENCES projects(company_id, id)
);
CREATE UNIQUE INDEX spec_documents_current ON spec_documents(company_id, project_id, path) WHERE current=1;
CREATE INDEX spec_documents_blob ON spec_documents(company_id, sha256);

-- Uploaded artifact versions record their content; external URIs keep sha256 ''.
ALTER TABLE artifact_versions ADD COLUMN sha256 text NOT NULL DEFAULT '';
ALTER TABLE artifact_versions ADD COLUMN size bigint NOT NULL DEFAULT 0;
CREATE INDEX artifact_versions_blob ON artifact_versions(company_id, sha256);

-- Run log chunks uploaded by nodes (redacted on the node before upload).
CREATE TABLE run_logs (
 company_id text NOT NULL REFERENCES companies(id),
 run_id text NOT NULL,
 seq bigint NOT NULL,
 sha256 text NOT NULL,
 size bigint NOT NULL,
 redacted bigint NOT NULL DEFAULT 0,
 final bigint NOT NULL DEFAULT 0,
 created_at bigint NOT NULL,
 PRIMARY KEY (company_id, run_id, seq),
 FOREIGN KEY (company_id, run_id) REFERENCES runs(company_id, id)
);

-- Report attachments (blobs uploaded by the run's adapter).
CREATE TABLE report_attachments (
 company_id text NOT NULL REFERENCES companies(id),
 report_id text NOT NULL,
 name text NOT NULL,
 sha256 text NOT NULL,
 size bigint NOT NULL,
 PRIMARY KEY (company_id, report_id, name),
 FOREIGN KEY (company_id, report_id) REFERENCES reports(company_id, id)
);

-- One archive task per OpenSpec change (D5), and one daily-check event per change without a task.
CREATE TABLE openspec_changes (
 company_id text NOT NULL REFERENCES companies(id),
 project_id text NOT NULL,
 change text NOT NULL,
 archive_task_id text NOT NULL DEFAULT '',
 orphan_reported_at bigint NOT NULL DEFAULT 0,
 PRIMARY KEY (company_id, project_id, change),
 FOREIGN KEY (company_id, project_id) REFERENCES projects(company_id, id)
);
